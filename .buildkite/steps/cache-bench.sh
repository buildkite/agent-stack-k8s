#!/usr/bin/env bash
set -eufo pipefail

# A-1412 — S3 cache transfer benchmark (TMPFS vs EBS + (de)compression variant).
#
# Why this run exists (follow-up to the 2 GB / memory-capped EBS run):
#   * The mem-capped EBS run showed downloads bottleneck on the gp3 disk
#     (~125 MB/s cold, ~200-220 MB/s warm). Open question (Ming): when the disk
#     is NOT the bottleneck, can the *download path* hit ~1 GB/s, or is there a
#     code/network ceiling? -> add a RAM-backed tmpfs target.
#   * tmpfs is RAM and is charged to the cgroup, so there is deliberately NO
#     memory *limit* on this pod (see pipeline.yaml). A 2 GB tmpfs object + SDK
#     buffers + the temp download archive must all fit; the 16 GB node has room
#     and we skip any sweep config whose buffers wouldn't fit (BUDGET_MB).
#   * Also measure (de)compression so "overall" throughput is download + the
#     decompress/write step, not transfer alone:
#       - decompress+write: agent restore wall-clock minus download time, on
#         tmpfs vs EBS (the write medium is what differs).
#       - compress: a zstd proxy (the agent compresses with zstd internally),
#         writing the archive to tmpfs vs EBS.
#
# Objects are ~2 GB *incompressible* (so the transfer really is ~2 GB; note this
# also means (de)compression is near CPU-passthrough for this object — the
# realistic compressible/many-file picture is the go_mod result in the doc).
#
# Experiments (strictly serial, single pinned node):
#   1. Download-speed ceiling on TMPFS — concurrency x part_size sweep + raw tools.
#   2. Overall restore throughput (download + decompress + write): tmpfs vs EBS.
#   3. Compression proxy (zstd): tmpfs vs EBS.

EBS_DIR=/tmp/bench            # container overlay on the gp3 root volume -> "EBS"
TMPFS_DIR=/mnt/tmpfs          # emptyDir{medium: Memory} from pipeline.yaml -> "RAM"
mkdir -p "$EBS_DIR" "$TMPFS_DIR"
RESULTS_CSV=cache-bench-results.csv
RESULTS_MD=cache-bench-results.md
echo "experiment,phase,tool,target,concurrency,part_size_mb,bytes,wall_s,download_mbps,overall_mbps,note" > "$RESULTS_CSV"

echo "--- :mag: storage & memory assertions"
echo "TMPDIR=${TMPDIR:-/tmp}"
df -hT "$EBS_DIR" 2>/dev/null || df -h "$EBS_DIR"
df -hT "$TMPFS_DIR" 2>/dev/null || df -h "$TMPFS_DIR"
mount | grep -E "$TMPFS_DIR|tmpfs" || true
echo "instance: $(grep -c ^processor /proc/cpuinfo) vCPU visible"
echo -n "cgroup memory limit (expect 'max'/very large — no cap this run): "
cat /sys/fs/cgroup/memory.max 2>/dev/null \
  || cat /sys/fs/cgroup/memory/memory.limit_in_bytes 2>/dev/null \
  || echo "unknown"
free -m 2>/dev/null || true

# Buffer budget for the download sweep: the AWS SDK holds ~concurrency*part_size
# of RAM in flight. Cap configs to a fraction of available memory so a big config
# can't OOM the node (esp. with a 2-4 GB tmpfs object also resident).
MEM_AVAIL_MB=$(awk '/MemAvailable/{printf "%d", $2/1024}' /proc/meminfo 2>/dev/null || echo 8000)
BUDGET_MB=$(( MEM_AVAIL_MB * 40 / 100 ))
echo "MemAvailable=${MEM_AVAIL_MB}MB -> sweep buffer budget=${BUDGET_MB}MB (skip configs above this)"

echo "--- :wrench: tools (aws-cli & jq already installed by assume-role.sh)"
command -v curl    >/dev/null 2>&1 || apk add --quiet --no-progress curl
command -v openssl >/dev/null 2>&1 || apk add --quiet --no-progress openssl
command -v zstd    >/dev/null 2>&1 || apk add --quiet --no-progress zstd
if ! command -v s5cmd >/dev/null 2>&1; then
  S5_VER=2.3.0
  curl -sL "https://github.com/peak/s5cmd/releases/download/v${S5_VER}/s5cmd_${S5_VER}_Linux-64bit.tar.gz" \
    | tar xz -C /usr/local/bin s5cmd
fi
s5cmd version; aws --version; zstd --version

echo "--- :earth_asia: region + 2GB objects"
BUCKET=buildkite-agent-stack-k8s-cache
# Role lacks s3:GetBucketLocation; resolve region via EC2 IMDS, fall back us-east-1.
AWS_REGION="${AWS_REGION:-}"
if [ -z "$AWS_REGION" ]; then
  IMDS_TOKEN="$(curl -sf -X PUT 'http://169.254.169.254/latest/api/token' \
    -H 'X-aws-ec2-metadata-token-ttl-seconds: 60' 2>/dev/null || true)"
  AWS_REGION="$(curl -sf -H "X-aws-ec2-metadata-token: ${IMDS_TOKEN}" \
    'http://169.254.169.254/latest/meta-data/placement/region' 2>/dev/null || true)"
fi
[ -z "$AWS_REGION" ] && AWS_REGION=us-east-1
export AWS_REGION AWS_DEFAULT_REGION="$AWS_REGION"

PAYLOAD_MB=2000
OBJ_BYTES=$((PAYLOAD_MB * 1000 * 1000))    # 2,000,000,000 bytes (~1.86 GiB)

# Fast, incompressible bytes via an AES-CTR keystream over /dev/zero (far faster
# than /dev/urandom; won't shrink under zstd so the transfer really is ~2 GB).
# Subshell with pipefail off so openssl's expected SIGPIPE isn't fatal.
gen_blob () { ( set +o pipefail; openssl enc -aes-256-ctr -pass pass:a1412bench -nosalt -in /dev/zero 2>/dev/null | head -c "$OBJ_BYTES" ); }

# Cache target dir for each cache name (must match .buildkite/cache.yml).
target_dir () { case "$1" in bench_big) echo /root/.cache/bench-big;; bench_big_tmpfs) echo /mnt/tmpfs/bench-big;; esac; }

record () { # experiment phase tool target conc part bytes wall_s download_mbps overall_mbps note
  echo "$1,$2,$3,$4,$5,$6,$7,$8,$9,${10},${11}" >> "$RESULTS_CSV"
  echo "[$1/$2] $3 -> $4 (c=${5:-?} p=${6:-?}): dl=${9:-–} MB/s overall=${10:-–} MB/s (${8:-?}s) ${11}"
}

mbps () { awk -v b="$1" -v s="$2" 'BEGIN{ if (s+0>0) printf "%.2f", b/1000000/s }'; } # bytes seconds

# Raw-tool synthetic object (role can't ListBucket to find the real archive).
OBJ_KEY="benchmark/cache-bench-tmpfs-${BUILDKITE_JOB_ID:-local}"
OBJ="s3://$BUCKET/$OBJ_KEY"
echo "Generating + uploading 2GB synthetic raw object -> $OBJ"
gen_blob > "$EBS_DIR/payload"
ls -l "$EBS_DIR/payload"
if ! aws s3 cp --only-show-errors "$EBS_DIR/payload" "$OBJ"; then
  echo "^^^ aws s3 cp failed; retrying once with s5cmd"; s5cmd cp "$EBS_DIR/payload" "$OBJ"
fi
cleanup () { aws s3 rm "$OBJ" >/dev/null 2>&1 || true; }
trap cleanup EXIT

# Seed + save both agent cache entries (EBS-target and tmpfs-target). Save is a
# no-op upload if the key already exists from a prior build; restores still pull.
for name in bench_big bench_big_tmpfs; do
  d=$(target_dir "$name"); mkdir -p "$d"
  echo "Seeding + saving $name -> $d"
  cp "$EBS_DIR/payload" "$d/blob"
  sw_start=$EPOCHREALTIME
  buildkite-agent cache save --name "$name"
  sw_end=$EPOCHREALTIME
  sw=$(awk "BEGIN{printf \"%.3f\", $sw_end-$sw_start}")
  record setup save agent "$([ "$name" = bench_big ] && echo ebs || echo tmpfs)" "" "" "$OBJ_BYTES" "$sw" "" "$(mbps "$OBJ_BYTES" "$sw")" "save+upload wall (no-op if key cached)"
done
rm -f "$EBS_DIR/payload"

timed () { # dest_path -- cmd...   -> prints elapsed seconds (cmd output -> fd2)
  local dest="$1"; shift; [ "$1" = "--" ] && shift
  rm -f "$dest"
  local start end; start=$EPOCHREALTIME; "$@" 1>&2; end=$EPOCHREALTIME
  awk "BEGIN{printf \"%.3f\", $end-$start}"
}

# Agent restore. phase=download records transfer_speed only; phase=restore also
# records overall (bytes/wall) so decompress+write = wall - download_time shows.
agent_restore () { # experiment phase cache_name tmpdir conc part
  local exp="$1" phase="$2" name="$3" tmp="$4" conc="$5" part="$6"
  local tgt; tgt=$([ "$name" = bench_big ] && echo ebs || echo tmpfs)
  if [ -n "$conc" ]; then
    local need=$(( conc * part ))
    if [ "$need" -gt "$BUDGET_MB" ]; then
      record "$exp" "$phase" agent "$tgt" "$conc" "$part" "$OBJ_BYTES" "" "" "" "SKIPPED buffers ${need}MB>budget ${BUDGET_MB}MB"
      return 0
    fi
    export BUILDKITE_AGENT_CACHE_STORE_URL="s3://$BUCKET?concurrency=$conc&part_size_mb=$part"
  else
    export BUILDKITE_AGENT_CACHE_STORE_URL="s3://$BUCKET"
  fi
  rm -rf "$(target_dir "$name")"
  echo ">>> BENCHMARK-MARKER $exp $phase target=$tgt concurrency=${conc:-default} part_size_mb=${part:-default}"
  local log start end rc sp dl_s wall ov
  log=$(mktemp); start=$EPOCHREALTIME
  set +e
  TMPDIR="$tmp" buildkite-agent cache restore --name "$name" 2>&1 | tee "$log"
  rc=${PIPESTATUS[0]}
  set -e
  end=$EPOCHREALTIME
  wall=$(awk "BEGIN{printf \"%.3f\", $end-$start}")
  if [ "$rc" -ne 0 ]; then
    record "$exp" "$phase" agent "$tgt" "$conc" "$part" "$OBJ_BYTES" "$wall" "" "" "FAILED rc=$rc (likely OOM)"
    export BUILDKITE_AGENT_CACHE_STORE_URL="s3://$BUCKET"; return 0
  fi
  sp=$(grep -oE 'transfer_speed=[0-9.]+' "$log" | cut -d= -f2 | sed -n 1p || true)
  if [ "$phase" = download ]; then
    [ -z "$sp" ] && sp=$(mbps "$OBJ_BYTES" "$wall")
    record "$exp" download agent "$tgt" "$conc" "$part" "$OBJ_BYTES" "$wall" "$sp" "" "transfer_speed (download-only)"
  else
    ov=$(mbps "$OBJ_BYTES" "$wall")
    record "$exp" restore agent "$tgt" "$conc" "$part" "$OBJ_BYTES" "$wall" "${sp:-}" "$ov" "overall=dl+decompress+write"
  fi
  export BUILDKITE_AGENT_CACHE_STORE_URL="s3://$BUCKET"
}

# ---------------------------------------------------------------------------
echo "+++ :one: Experiment 1 — DOWNLOAD ceiling on TMPFS (RAM, disk removed)"
agent_restore exp1-tmpfs-dl download bench_big_tmpfs "$TMPFS_DIR" "" ""        # baseline c5/p5
agent_restore exp1-tmpfs-dl download bench_big_tmpfs "$TMPFS_DIR" 5 5
for c in 8 16 32; do for p in 16 32 64; do agent_restore exp1-tmpfs-dl download bench_big_tmpfs "$TMPFS_DIR" "$c" "$p"; done; done
# raw tools to tmpfs (corroborate the ceiling, no agent SDK involved)
s=$(timed "$TMPFS_DIR/o" -- s5cmd cp "$OBJ" "$TMPFS_DIR/o");             record exp1-tmpfs-dl download s5cmd-default tmpfs ""   ""  "$OBJ_BYTES" "$s" "$(mbps "$OBJ_BYTES" "$s")" "" "raw"; rm -f "$TMPFS_DIR/o"
s=$(timed "$TMPFS_DIR/o" -- s5cmd cp -c 16 -p 32 "$OBJ" "$TMPFS_DIR/o"); record exp1-tmpfs-dl download s5cmd-tuned   tmpfs "16" "32" "$OBJ_BYTES" "$s" "$(mbps "$OBJ_BYTES" "$s")" "" "raw"; rm -f "$TMPFS_DIR/o"
s=$(timed "$TMPFS_DIR/o" -- aws s3 cp --only-show-errors "$OBJ" "$TMPFS_DIR/o"); record exp1-tmpfs-dl download awscli tmpfs "" "" "$OBJ_BYTES" "$s" "$(mbps "$OBJ_BYTES" "$s")" "" "raw"; rm -f "$TMPFS_DIR/o"

# ---------------------------------------------------------------------------
echo "+++ :two: Experiment 2 — OVERALL restore throughput (download + decompress + write): tmpfs vs EBS"
# Default config + a tuned config, each on both media. overall = bytes/restore_wall.
agent_restore exp2-overall restore bench_big_tmpfs "$TMPFS_DIR" "" ""
agent_restore exp2-overall restore bench_big      "$EBS_DIR"   "" ""
agent_restore exp2-overall restore bench_big_tmpfs "$TMPFS_DIR" 16 32
agent_restore exp2-overall restore bench_big      "$EBS_DIR"   16 32

# ---------------------------------------------------------------------------
echo "+++ :three: Experiment 3 — compression proxy (zstd -T0, agent uses zstd internally)"
# Free the cache target dirs first so tmpfs has room for the zstd scratch files.
rm -rf /mnt/tmpfs/bench-big /root/.cache/bench-big
# Regenerate the blob on each medium, compress then decompress, timing each.
# Incompressible data -> ratio ~1.0, so this is the CPU+IO cost of (de)compression.
# rm the input between steps to keep the tmpfs peak ~4 GB.
for pair in "ebs:$EBS_DIR" "tmpfs:$TMPFS_DIR"; do
  tgt=${pair%%:*}; dir=${pair#*:}
  gen_blob > "$dir/zin"
  cs=$(timed "$dir/zin.zst" -- zstd -T0 -q -f "$dir/zin" -o "$dir/zin.zst")
  ratio=$(awk -v a="$(wc -c < "$dir/zin")" -v b="$(wc -c < "$dir/zin.zst")" 'BEGIN{ if (b+0>0) printf "%.3f", a/b; else printf "?" }' 2>/dev/null || echo "?")
  rm -f "$dir/zin"
  record exp3-compress compress zstd "$tgt" "" "" "$OBJ_BYTES" "$cs" "" "$(mbps "$OBJ_BYTES" "$cs")" "zstd ratio=${ratio}"
  ds=$(timed "$dir/zout" -- zstd -d -T0 -q -f "$dir/zin.zst" -o "$dir/zout")
  record exp3-decompress decompress zstd "$tgt" "" "" "$OBJ_BYTES" "$ds" "" "$(mbps "$OBJ_BYTES" "$ds")" "zstd -d"
  rm -f "$dir/zin.zst" "$dir/zout"
done

echo "--- :bar_chart: render results"
{
  echo "# A-1412 cache transfer benchmark — TMPFS vs EBS + (de)compression"
  echo
  echo "- Object: **${PAYLOAD_MB} MB (~1.86 GiB) incompressible**."
  echo "- **No memory cap** this run (tmpfs is RAM and is charged to the cgroup); 16 GB node."
  echo "- Region: $AWS_REGION | Node: $(hostname) | Instance: m7a.xlarge | Root vol: gp3 250GiB / 125 MB/s / 3000 IOPS"
  echo "- Sweep buffer budget: ${BUDGET_MB} MB (configs above this are SKIPPED to avoid OOM)."
  echo
  echo '| experiment | phase | tool | target | conc | part_mb | wall_s | download_MBps | overall_MBps | note |'
  echo '|---|---|---|---|---|---|---|---|---|---|'
  tail -n +2 "$RESULTS_CSV" | awk -F, '{printf "| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",$1,$2,$3,$4,$5,$6,$8,$9,$10,$11}'
} > "$RESULTS_MD"
cat "$RESULTS_MD"
buildkite-agent annotate --style info --context cache-bench < "$RESULTS_MD"
