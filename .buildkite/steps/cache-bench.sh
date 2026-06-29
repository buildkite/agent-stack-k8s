#!/usr/bin/env bash
set -eufo pipefail

# A-1412 — DEFINITIVE S3 cache transfer benchmark (closes the investigation).
#
# Goal (measurement only, NO agent code change): pin down WHERE the cache-restore
# ceiling is and give a recommendation. Three prior rounds + a code read
# (see Obsidian [[2026-06-29-A-1412-cache-transfer-findings]] and
# [[2026-06-30-A-1412-agent-s3-download-code-read]]) established:
#   * the concurrency/part_size sweep is ~flat;
#   * raw s5cmd is ~10x the agent into the same RAM target;
#   * BUT the agent's `transfer_speed` metric is GET-only and the code does a
#     synchronous full-object CopyObject self-copy per restore (inside Download(),
#     after the metric timer stops), plus single-stream zstd extract to disk.
#
# This run is built to give the clean, decisive numbers by DECOMPOSING the
# restore into stages using the agent's OWN progress log lines
# (downloading -> cleaning -> extracting -> complete). We prepend a wall-clock
# timestamp to every streamed log line, so:
#   download_stage_s = t(cleaning) - t(downloading)   # GET + the CopyObject self-copy
#   clean_s          = t(extracting) - t(cleaning)
#   extract_s        = t(complete)  - t(extracting)    # zstd decompress + write N files
#   get_only_s       = bytes / (transfer_speed * 1e6)  # the metric's (shorter) span
# The gap (download_stage_s - get_only_s) is the self-copy overhead the metric hides.
#
# Experiments (single pinned node, strictly serial, REPEATS repeats each):
#   1. CEILING / tmpfs-vs-EBS (synthetic 2 GB incompressible blob): agent restore
#      wall-clock to tmpfs vs EBS, with same-run s5cmd (default + tuned) and aws
#      s3 cp to the SAME target. -> Does removing the disk lift the agent? Is
#      s5cmd materially faster on the same instance? (the core decision)
#   2. SETTINGS SWEEP (synthetic 2 GB blob, tmpfs): agent c5/p5 default +
#      {8,16,32}x{16,32,64}. Reports GET-only (transfer_speed) AND wall. -> How
#      much does tuning concurrency/part_size actually recover?
#   3. REAL-WORKLOAD DECOMPOSITION (the real go_mod cache: many small files):
#      agent restore to EBS (production reality) and tmpfs, split into
#      download_s vs extract_s. -> For the cache the ticket is about, what
#      fraction is transfer vs unpacking tens of thousands of files?
#
# PAGE-CACHE CAVEAT (stated honestly): tmpfs is RAM, so this pod has NO memory
# *limit*. A 2 GB object therefore fits in page cache and EBS writes are
# write-back buffered -> the EBS legs here are NOT cold-disk numbers. The
# DECISIVE comparison is agent-vs-s5cmd to the same *tmpfs* target in the same
# run: an S3 network GET is immune to local page cache. Cold-disk EBS numbers
# come from the prior memory-capped round (builds #3811-3813, ~123 MB/s cold).

REPEATS="${REPEATS:-3}"
# Per-experiment gates (so a re-run can target just the experiment that failed).
RUN_EXP1="${RUN_EXP1:-true}"
RUN_EXP2="${RUN_EXP2:-true}"
RUN_EXP3="${RUN_EXP3:-true}"
GO_MOD_FILES=na
GO_MOD_BYTES=0

EBS_DIR=/tmp/bench            # container overlay on the gp3 root volume -> "EBS"
TMPFS_DIR=/root/tmpfs         # emptyDir{medium: Memory} from pipeline.yaml -> "RAM"
                              # (mounted inside /root so `cache save` can archive it;
                              #  the agent refuses to archive paths outside chroot /root)
mkdir -p "$EBS_DIR" "$TMPFS_DIR"
RESULTS_CSV=cache-bench-results.csv
RESULTS_MD=cache-bench-results.md
echo "experiment,rep,phase,tool,target,concurrency,part_size_mb,bytes,wall_s,download_stage_s,get_only_s,extract_s,clean_s,transfer_speed_mbps,wall_mbps,files,note" > "$RESULTS_CSV"

echo "--- :mag: storage, memory & region assertions"
echo "TMPDIR=${TMPDIR:-/tmp} | REPEATS=$REPEATS"
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
# can't OOM the node (esp. with a 2 GB tmpfs object also resident).
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

echo "--- :earth_asia: region + bucket"
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
target_dir () {
  case "$1" in
    bench_big)          echo /root/.cache/bench-big;;
    bench_big_tmpfs)    echo /root/tmpfs/bench-big;;
    go_mod)             echo /root/.cache/go-mod;;
    go_mod_bench)       echo /root/.cache/go-mod-bench;;
    go_mod_bench_tmpfs) echo /root/tmpfs/go-mod-bench;;
  esac
}
target_medium () { case "$1" in *_tmpfs) echo tmpfs;; *) echo ebs;; esac; }

mbps () { awk -v b="$1" -v s="$2" 'BEGIN{ if (s+0>0) printf "%.2f", b/1000000/s }'; } # bytes seconds

# Convert a humanized size like "406 MB" / "1.1 GB" / "57 kB" to bytes (best effort).
human_to_bytes () {
  awk -v s="$1" 'BEGIN{
    n=s; unit=s; sub(/[^0-9.].*/,"",n); sub(/^[0-9. ]+/,"",unit);
    mult=1;
    if (unit ~ /^kB|^KB|^kiB/) mult=1000; else if (unit ~ /^MB|^MiB/) mult=1000000;
    else if (unit ~ /^GB|^GiB/) mult=1000000000; else if (unit ~ /^B/) mult=1;
    if (n+0>0) printf "%d", n*mult;
  }'
}

# csv: append a full row. Fields in header order; pass "" for any not applicable.
csv () { # exp rep phase tool target conc part bytes wall dl_stage get_only extract clean tspeed wall_mbps files note
  printf '%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s\n' \
    "$1" "$2" "$3" "$4" "$5" "$6" "$7" "$8" "$9" "${10}" "${11}" "${12}" "${13}" "${14}" "${15}" "${16}" "${17}" >> "$RESULTS_CSV"
  echo "[$1 r$2/$3] $4->$5 (c=${6:-?} p=${7:-?}): wall=${9:-–}s dl_stage=${10:-–}s extract=${12:-–}s tspeed=${14:-–} wall_mbps=${15:-–} files=${16:-–} ${17}"
}

# Strip ANSI then return wall-clock epoch (col 1) of the first line containing $2.
stage_ts () { sed 's/\x1b\[[0-9;]*m//g' "$1" | grep -m1 -F "$2" | awk '{print $1}'; }
delta () { awk -v a="$1" -v b="$2" 'BEGIN{ if (a!="" && b!="") printf "%.3f", b-a }'; }

timed () { # dest_path -- cmd...   -> prints elapsed seconds (cmd output -> fd2)
  local dest="$1"; shift; [ "$1" = "--" ] && shift
  rm -f "$dest"
  local start end; start=$EPOCHREALTIME; "$@" 1>&2; end=$EPOCHREALTIME
  awk "BEGIN{printf \"%.3f\", $end-$start}"
}

# Raw-tool synthetic object (role can't ListBucket to find the real archive).
# Only needed for the synthetic-blob experiments (1 & 2).
OBJ_KEY="benchmark/cache-bench-def-${BUILDKITE_JOB_ID:-local}"
OBJ="s3://$BUCKET/$OBJ_KEY"
cleanup () { aws s3 rm "$OBJ" >/dev/null 2>&1 || true; }
trap cleanup EXIT
if [ "$RUN_EXP1" = true ] || [ "$RUN_EXP2" = true ]; then
  echo "--- :package: generate + upload 2GB synthetic raw object -> $OBJ"
  gen_blob > "$EBS_DIR/payload"
  ls -l "$EBS_DIR/payload"
  if ! aws s3 cp --only-show-errors "$EBS_DIR/payload" "$OBJ"; then
    echo "^^^ aws s3 cp failed; retrying once with s5cmd"; s5cmd cp "$EBS_DIR/payload" "$OBJ"
  fi
  # Seed + save the synthetic agent cache entries (EBS-target and tmpfs-target).
  for name in bench_big bench_big_tmpfs; do
    d=$(target_dir "$name"); mkdir -p "$d"
    echo "Seeding + saving $name -> $d"
    cp "$EBS_DIR/payload" "$d/blob"
    buildkite-agent cache save --name "$name" || echo "(save no-op / already exists)"
    rm -rf "$d"
  done
  rm -f "$EBS_DIR/payload"
fi

# Core agent-restore measurement with stage decomposition.
# Sets BUILDKITE_AGENT_CACHE_STORE_URL per config, clears target, runs restore,
# timestamps every log line, then decomposes the stages.
agent_restore () { # exp rep cache_name tmpdir conc part bytes
  local exp="$1" rep="$2" name="$3" tmp="$4" conc="$5" part="$6" bytes="$7"
  local tgt; tgt=$(target_medium "$name")
  if [ -n "$conc" ]; then
    local need=$(( conc * part ))
    if [ "$need" -gt "$BUDGET_MB" ]; then
      csv "$exp" "$rep" "$REPEATS" agent "$tgt" "$conc" "$part" "$bytes" "" "" "" "" "" "" "" "" "SKIPPED buffers ${need}MB>budget ${BUDGET_MB}MB"
      return 0
    fi
    export BUILDKITE_AGENT_CACHE_STORE_URL="s3://$BUCKET?concurrency=$conc&part_size_mb=$part"
  else
    export BUILDKITE_AGENT_CACHE_STORE_URL="s3://$BUCKET"
  fi
  rm -rf "$(target_dir "$name")"
  echo ">>> MARKER $exp rep=$rep $name target=$tgt c=${conc:-default} p=${part:-default}"
  local log start end rc wall
  log=$(mktemp); start=$EPOCHREALTIME
  set +e
  # Prepend a wall-clock epoch to every streamed line so we can time the agent's
  # own stage-progress log lines (downloading/cleaning/extracting/complete).
  TMPDIR="$tmp" buildkite-agent cache restore --name "$name" 2>&1 \
    | while IFS= read -r line; do printf '%s\t%s\n' "$EPOCHREALTIME" "$line"; done \
    | tee "$log"
  rc=${PIPESTATUS[0]}
  set -e
  end=$EPOCHREALTIME
  wall=$(awk "BEGIN{printf \"%.3f\", $end-$start}")
  if [ "$rc" -ne 0 ]; then
    csv "$exp" "$rep" "$REPEATS" agent "$tgt" "$conc" "$part" "$bytes" "$wall" "" "" "" "" "" "" "" "FAILED rc=$rc (likely OOM)"
    export BUILDKITE_AGENT_CACHE_STORE_URL="s3://$BUCKET"; return 0
  fi
  # Decompose via stage timestamps.
  local t_dl t_clean t_extract t_done dl_stage clean_s extract_s
  t_dl=$(stage_ts "$log" "Downloading cache archive")
  t_clean=$(stage_ts "$log" "Cleaning paths")
  t_extract=$(stage_ts "$log" "Extracting files from cache")
  t_done=$(stage_ts "$log" "Cache restored successfully")
  dl_stage=$(delta "$t_dl" "$t_clean")
  clean_s=$(delta "$t_clean" "$t_extract")
  extract_s=$(delta "$t_extract" "$t_done")
  # GET-only metric + archive size.
  local sp arch_h arch_b get_only wall_mbps files
  sp=$(sed 's/\x1b\[[0-9;]*m//g' "$log" | grep -oE 'transfer_speed=[0-9.]+' | head -1 | cut -d= -f2 || true)
  arch_h=$(sed 's/\x1b\[[0-9;]*m//g' "$log" | grep -oE 'archive_size=[0-9.]+ ?[KkMGi]*B' | head -1 | cut -d= -f2 || true)
  arch_b=$(human_to_bytes "${arch_h:-}")
  [ -z "$arch_b" ] && arch_b="$bytes"
  get_only=$(awk -v b="$arch_b" -v s="$sp" 'BEGIN{ if (s+0>0) printf "%.3f", b/1000000/s }')
  wall_mbps=$(mbps "$arch_b" "$wall")
  files=$(sed 's/\x1b\[[0-9;]*m//g' "$log" | grep -oE 'written_entries=[0-9]+' | head -1 | cut -d= -f2 || true)
  csv "$exp" "$rep" "$REPEATS" agent "$tgt" "$conc" "$part" "$arch_b" "$wall" "${dl_stage:-}" "${get_only:-}" "${extract_s:-}" "${clean_s:-}" "${sp:-}" "$wall_mbps" "${files:-}" "stages: dl(incl self-copy)/clean/extract"
  rm -f "$log"
  export BUILDKITE_AGENT_CACHE_STORE_URL="s3://$BUCKET"
}

# raw-tool leg to a given medium (corroborate the ceiling, no agent SDK).
raw_leg () { # exp rep tool target dir conc part cmd...
  local exp="$1" rep="$2" tool="$3" tgt="$4" dir="$5" conc="$6" part="$7"; shift 7
  local s; s=$(timed "$dir/o" -- "$@")
  csv "$exp" "$rep" download "$tool" "$tgt" "$conc" "$part" "$OBJ_BYTES" "$s" "$s" "" "" "" "" "$(mbps "$OBJ_BYTES" "$s")" "" "raw tool, wall-clock"
  rm -f "$dir/o"
}

# ---------------------------------------------------------------------------
if [ "$RUN_EXP1" = true ]; then
echo "+++ :one: Experiment 1 — CEILING: agent vs s5cmd vs aws, tmpfs AND EBS (synthetic 2GB)"
for rep in $(seq 1 "$REPEATS"); do
  # agent default to each target
  agent_restore exp1-ceiling "$rep" bench_big_tmpfs "$TMPFS_DIR" "" "" "$OBJ_BYTES"
  agent_restore exp1-ceiling "$rep" bench_big       "$EBS_DIR"   "" "" "$OBJ_BYTES"
  # same-run raw references to the SAME targets
  raw_leg exp1-ceiling "$rep" s5cmd-default tmpfs "$TMPFS_DIR" ""   ""   s5cmd cp "$OBJ" "$TMPFS_DIR/o"
  raw_leg exp1-ceiling "$rep" s5cmd-tuned   tmpfs "$TMPFS_DIR" "16" "32" s5cmd cp -c 16 -p 32 "$OBJ" "$TMPFS_DIR/o"
  raw_leg exp1-ceiling "$rep" awscli        tmpfs "$TMPFS_DIR" ""   ""   aws s3 cp --only-show-errors "$OBJ" "$TMPFS_DIR/o"
  raw_leg exp1-ceiling "$rep" s5cmd-default ebs   "$EBS_DIR"   ""   ""   s5cmd cp "$OBJ" "$EBS_DIR/o"
  raw_leg exp1-ceiling "$rep" s5cmd-tuned   ebs   "$EBS_DIR"   "16" "32" s5cmd cp -c 16 -p 32 "$OBJ" "$EBS_DIR/o"
  raw_leg exp1-ceiling "$rep" awscli        ebs   "$EBS_DIR"   ""   ""   aws s3 cp --only-show-errors "$OBJ" "$EBS_DIR/o"
done
fi

# ---------------------------------------------------------------------------
if [ "$RUN_EXP2" = true ]; then
echo "+++ :two: Experiment 2 — SETTINGS SWEEP (synthetic 2GB, tmpfs): GET-only vs wall"
for rep in $(seq 1 "$REPEATS"); do
  agent_restore exp2-sweep "$rep" bench_big_tmpfs "$TMPFS_DIR" 5 5 "$OBJ_BYTES"     # explicit default c5/p5
  for c in 8 16 32; do for p in 16 32 64; do
    agent_restore exp2-sweep "$rep" bench_big_tmpfs "$TMPFS_DIR" "$c" "$p" "$OBJ_BYTES"
  done; done
done
fi

# Free synthetic tmpfs/EBS cache dirs before the (larger, many-file) go_mod work.
rm -rf /root/tmpfs/bench-big /root/.cache/bench-big

# ---------------------------------------------------------------------------
if [ "$RUN_EXP3" = true ]; then
echo "+++ :three: Experiment 3 — REAL go_mod cache: download_s vs extract_s (EBS vs tmpfs)"
echo "--- populate the go_mod module cache (/root/.cache/go-mod) via go mod download + build"
export BUILDKITE_AGENT_CACHE_STORE_URL="s3://$BUCKET"
# The production go_mod cache entry is a miss in this benchmark registry, so we
# recreate the genuine many-file go module cache deterministically: 'go mod
# download all' fetches + extracts module sources, and 'go build ./...' forces
# extraction of every dependency's source tree (the tens-of-thousands-of-files
# shape the ticket is about). Build/vet errors are irrelevant — we only want the
# module cache populated.
rm -rf /root/.cache/go-mod; mkdir -p /root/.cache/go-mod
export GOMODCACHE=/root/.cache/go-mod GOFLAGS=-mod=mod
go mod download all 2>&1 | tail -3 || true
go build ./... 2>&1 | tail -3 || true
mkdir -p /root/.cache/go-mod
GO_MOD_FILES=$( (find /root/.cache/go-mod -type f 2>/dev/null || true) | wc -l | tr -d ' ')
GO_MOD_BYTES=$(du -sk /root/.cache/go-mod 2>/dev/null | awk '{printf "%d", $1*1024}' || echo 0)
echo "go_mod content: ${GO_MOD_FILES} files, ${GO_MOD_BYTES} bytes"

# Seed comparable EBS + tmpfs bench entries from the real content (distinct keys).
echo "--- seed go_mod_bench (EBS) + go_mod_bench_tmpfs (tmpfs) from real content"
for name in go_mod_bench go_mod_bench_tmpfs; do
  d=$(target_dir "$name"); rm -rf "$d"; mkdir -p "$(dirname "$d")"
  cp -a /root/.cache/go-mod "$d"
  buildkite-agent cache save --name "$name" || echo "(save no-op / already exists)"
  rm -rf "$d"
done

echo "--- decomposed restores (EBS vs tmpfs), $REPEATS repeats"
for rep in $(seq 1 "$REPEATS"); do
  agent_restore exp3-real-ebs   "$rep" go_mod_bench       "$EBS_DIR"   "" "" "$GO_MOD_BYTES"
  agent_restore exp3-real-tmpfs "$rep" go_mod_bench_tmpfs "$TMPFS_DIR" "" "" "$GO_MOD_BYTES"
done
fi

echo "--- :bar_chart: render results"
{
  echo "# A-1412 DEFINITIVE cache transfer benchmark — ceiling, sweep, real go_mod decomposition"
  echo
  echo "- Synthetic object: **${PAYLOAD_MB} MB (~1.86 GiB) incompressible**; real go_mod: **${GO_MOD_FILES} files / ${GO_MOD_BYTES} bytes**."
  echo "- **No memory cap** this run (tmpfs is RAM); 16 GB node. EBS legs are write-back-cached, NOT cold-disk (see caveat in doc)."
  echo "- Region: $AWS_REGION | Node: $(hostname) | Instance: m7a.xlarge | Root vol: gp3 250GiB / 125 MB/s / 3000 IOPS"
  echo "- Repeats: $REPEATS | Sweep buffer budget: ${BUDGET_MB} MB (configs above this SKIPPED)."
  echo "- Decomposition uses the agent's own progress log: download_stage_s incl. the per-restore CopyObject self-copy; get_only_s = bytes/transfer_speed."
  echo
  echo '| experiment | rep | phase | tool | target | c | p | wall_s | dl_stage_s | get_only_s | extract_s | clean_s | tspeed_MBps | wall_MBps | files | note |'
  echo '|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|'
  tail -n +2 "$RESULTS_CSV" | awk -F, '{printf "| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",$1,$2,$3,$4,$5,$6,$7,$9,$10,$11,$12,$13,$14,$15,$16,$17}'
} > "$RESULTS_MD"
cat "$RESULTS_MD"
buildkite-agent annotate --style info --context cache-bench < "$RESULTS_MD"
