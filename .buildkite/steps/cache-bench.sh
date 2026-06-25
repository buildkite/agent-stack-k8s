#!/usr/bin/env bash
set -eufo pipefail

# A-1412 — S3 cache transfer benchmark (BIG-OBJECT / MEMORY-CAPPED variant).
#
# Differences from the original 406 MB benchmark:
#   * Object is ~2 GB of *incompressible* data, so the result is far less
#     skewed by the OS page cache (a 406 MB object fits trivially in RAM).
#   * The pod runs under a hard memory limit (see pipeline.yaml) so the cache
#     cannot hold the whole object -> measured speed reflects real I/O.
#   * EBS (/tmp) target only. A memory-backed tmpfs would consume the capped
#     memory and defeat the purpose, so the tmpfs legs are dropped.
#
# Experiments (strictly serial, single pinned node):
#   1. agent restore + raw cp (s5cmd / aws) to EBS
#   2. concurrency x part_size sweep via the store URL
#   3. s5cmd reference (default + tuned), then synthesise + annotate.

EBS_DIR=/tmp/bench           # container overlay on the gp3 root volume -> "EBS"
mkdir -p "$EBS_DIR"
RESULTS_CSV=cache-bench-results.csv
RESULTS_MD=cache-bench-results.md
echo "experiment,tool,target,concurrency,part_size_mb,bytes,seconds,mb_per_s" > "$RESULTS_CSV"

echo "--- :mag: storage & memory assertions"
echo "TMPDIR=${TMPDIR:-/tmp}"
df -hT /tmp 2>/dev/null || df -h /tmp
grep -E ' / ' /proc/mounts || true
echo "instance: $(grep -c ^processor /proc/cpuinfo) vCPU visible"
echo -n "cgroup memory limit: "
cat /sys/fs/cgroup/memory.max 2>/dev/null \
  || cat /sys/fs/cgroup/memory/memory.limit_in_bytes 2>/dev/null \
  || echo "unknown"
free -m 2>/dev/null || true

echo "--- :wrench: tools (aws-cli & jq already installed by assume-role.sh)"
command -v curl    >/dev/null 2>&1 || apk add --quiet --no-progress curl
command -v openssl >/dev/null 2>&1 || apk add --quiet --no-progress openssl
if ! command -v s5cmd >/dev/null 2>&1; then
  S5_VER=2.3.0
  curl -sL "https://github.com/peak/s5cmd/releases/download/v${S5_VER}/s5cmd_${S5_VER}_Linux-64bit.tar.gz" \
    | tar xz -C /usr/local/bin s5cmd
fi
s5cmd version; aws --version

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

# Fast, incompressible bytes via an AES-CTR keystream over /dev/zero. This keeps
# the agent's zstd archive from shrinking (so the transfer really is ~2 GB) and
# is far faster than reading /dev/urandom. Runs in a subshell with pipefail off
# so openssl's expected SIGPIPE (when head closes) isn't treated as an error.
gen_blob () { ( set +o pipefail; openssl enc -aes-256-ctr -pass pass:a1412bench -nosalt -in /dev/zero 2>/dev/null | head -c "$OBJ_BYTES" ); }

# Raw-tool target: a 2 GB synthetic object (role can't ListBucket to find the
# real archive). Same bucket/region/size -> fair transfer comparison.
OBJ_KEY="benchmark/cache-bench-2gb-${BUILDKITE_JOB_ID:-local}"
OBJ="s3://$BUCKET/$OBJ_KEY"
echo "Generating 2GB synthetic payload"
gen_blob > "$EBS_DIR/payload"
ls -l "$EBS_DIR/payload"
free -m 2>/dev/null || true
echo "Uploading 2GB synthetic object -> $OBJ"
# --only-show-errors: suppress the \r progress meter that otherwise hides the
# real error message in the captured log.
if ! aws s3 cp --only-show-errors "$EBS_DIR/payload" "$OBJ"; then
  echo "^^^ aws s3 cp failed; retrying once with s5cmd"
  s5cmd cp "$EBS_DIR/payload" "$OBJ"
fi
rm -f "$EBS_DIR/payload"
cleanup () { aws s3 rm "$OBJ" >/dev/null 2>&1 || true; }
trap cleanup EXIT

# Agent target: a 2 GB incompressible cache entry (bench_big in .buildkite/cache.yml).
# Save once; if the key already exists from a prior build the save is a no-op and
# restores pull that object. A single 2 GB file (not 58k small files), so this
# isolates *transfer* rather than extraction.
BIG_DIR=/root/.cache/bench-big
echo "Building + saving 2GB agent cache entry (bench_big)"
mkdir -p "$BIG_DIR"
gen_blob > "$BIG_DIR/blob"
buildkite-agent cache save --name bench_big
echo "region=$AWS_REGION, object_bytes=$OBJ_BYTES"

record () { # experiment tool target conc part bytes seconds  (mb_per_s computed)
  local mbps; mbps=$(awk "BEGIN{printf \"%.2f\", $6/1000000/$7}")
  echo "$1,$2,$3,$4,$5,$6,$7,$mbps" >> "$RESULTS_CSV"
  echo "[$1] $2 -> $3 (c=$4 p=$5): ${mbps} MB/s (${7}s)"
}

timed () { # dest_path -- cmd...   -> prints elapsed seconds
  # busybox `date` has no %N, so use bash 5's $EPOCHREALTIME. Send the command's
  # own output to fd2 so the substitution only captures the elapsed seconds.
  local dest="$1"; shift; [ "$1" = "--" ] && shift
  rm -f "$dest"
  local start end; start=$EPOCHREALTIME; "$@" 1>&2; end=$EPOCHREALTIME
  awk "BEGIN{printf \"%.3f\", $end-$start}"
}

# Resilient agent restore: a config whose buffers exceed the memory cap may be
# OOM-killed; record it as FAILED and keep going instead of aborting the run.
agent_restore () { # experiment conc part   (conc/part empty => default URL)
  local exp="$1" conc="$2" part="$3"
  if [ -n "$conc" ]; then
    export BUILDKITE_AGENT_CACHE_STORE_URL="s3://$BUCKET?concurrency=$conc&part_size_mb=$part"
  else
    export BUILDKITE_AGENT_CACHE_STORE_URL="s3://$BUCKET"
  fi
  rm -rf "$BIG_DIR"
  echo ">>> BENCHMARK-MARKER $exp concurrency=${conc:-default} part_size_mb=${part:-default}"
  local log start end sp rc
  log=$(mktemp); start=$EPOCHREALTIME
  set +e
  TMPDIR="$EBS_DIR" buildkite-agent cache restore --name bench_big 2>&1 | tee "$log"
  rc=${PIPESTATUS[0]}
  set -e
  end=$EPOCHREALTIME
  if [ "$rc" -ne 0 ]; then
    echo "$exp,agent,ebs,$conc,$part,${OBJ_BYTES},,FAILED" >> "$RESULTS_CSV"
    echo "[$exp] c=$conc p=$part: FAILED (rc=$rc — likely OOM under memory cap)"
    export BUILDKITE_AGENT_CACHE_STORE_URL="s3://$BUCKET"
    return 0
  fi
  sp=$(grep -oE 'transfer_speed=[0-9.]+' "$log" | cut -d= -f2 | sed -n 1p || true)
  [ -z "$sp" ] && sp=$(awk "BEGIN{printf \"%.2f\", ${OBJ_BYTES}/1000000/(${end}-${start})}")
  echo "$exp,agent,ebs,$conc,$part,${OBJ_BYTES},$(awk "BEGIN{printf \"%.3f\",${end}-${start}}"),${sp}" >> "$RESULTS_CSV"
  echo "[$exp] c=${conc:-default} p=${part:-default}: ${sp} MB/s (download-only metric if present, else wallclock)"
  export BUILDKITE_AGENT_CACHE_STORE_URL="s3://$BUCKET"
}

# ---------------------------------------------------------------------------
echo "+++ :one: Experiment 1 — agent vs raw tools (EBS, 2GB)"
agent_restore exp1-agent "" ""
s=$(timed "$EBS_DIR/o" -- s5cmd cp "$OBJ" "$EBS_DIR/o"); record exp1 s5cmd  ebs "" "" "$OBJ_BYTES" "$s"; rm -f "$EBS_DIR/o"
s=$(timed "$EBS_DIR/o" -- aws s3 cp "$OBJ" "$EBS_DIR/o"); record exp1 awscli ebs "" "" "$OBJ_BYTES" "$s"; rm -f "$EBS_DIR/o"

# ---------------------------------------------------------------------------
echo "+++ :two: Experiment 2 — concurrency/part_size sweep (2GB, EBS)"
agent_restore exp2-sweep 5 5                                   # baseline (current defaults)
for c in 8 16 32; do for p in 16 32 64; do agent_restore exp2-sweep "$c" "$p"; done; done

# ---------------------------------------------------------------------------
echo "+++ :three: Experiment 3 — s5cmd reference (2GB, EBS)"
s=$(timed "$EBS_DIR/o" -- s5cmd cp "$OBJ" "$EBS_DIR/o");             record exp3 s5cmd-default ebs ""   ""  "$OBJ_BYTES" "$s"; rm -f "$EBS_DIR/o"
s=$(timed "$EBS_DIR/o" -- s5cmd cp -c 16 -p 32 "$OBJ" "$EBS_DIR/o"); record exp3 s5cmd-tuned   ebs "16" "32" "$OBJ_BYTES" "$s"; rm -f "$EBS_DIR/o"

echo "--- :bar_chart: render results"
{
  echo "# A-1412 cache transfer benchmark — 2GB / memory-capped"
  echo
  echo "- Object: **${PAYLOAD_MB} MB (~1.86 GiB) incompressible**, EBS target only."
  echo "- SDK legs (\`agent\`): \`bench_big\` cache entry (single 2 GB file)."
  echo "- Raw-tool legs (\`s5cmd\`/\`awscli\`): synthetic \`$OBJ\` ($OBJ_BYTES bytes)."
  echo "- Region: $AWS_REGION | Node: $(hostname)"
  echo "- Instance: m7a.xlarge | Root vol: gp3 250GiB / 125 MB/s / 3000 IOPS | **pod memory capped at 4Gi**"
  echo
  echo '| experiment | tool | target | conc | part_mb | MB/s |'
  echo '|---|---|---|---|---|---|'
  tail -n +2 "$RESULTS_CSV" | awk -F, '{printf "| %s | %s | %s | %s | %s | %s |\n",$1,$2,$3,$4,$5,$8}'
} > "$RESULTS_MD"
cat "$RESULTS_MD"
buildkite-agent annotate --style info --context cache-bench < "$RESULTS_MD"
