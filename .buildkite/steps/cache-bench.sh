#!/usr/bin/env bash
set -eufo pipefail

# A-1412 — S3 cache transfer bandwidth benchmark.
# Runs three experiments strictly serially on a single pinned node:
#   1. agent restore + raw cp to tmpfs vs EBS (isolate the bottleneck)
#   2. concurrency x part_size sweep via store URL (isolate SDK config)
#   3. s5cmd reference (default + tuned), then synthesise results + annotate.

EBS_DIR=/tmp/bench           # container overlay on the gp3 root volume -> "EBS" leg
TMPFS_DIR=/mnt/tmpfs         # emptyDir medium=Memory -> "tmpfs" leg
mkdir -p "$EBS_DIR" "$TMPFS_DIR"
RESULTS_CSV=cache-bench-results.csv
RESULTS_MD=cache-bench-results.md
echo "experiment,tool,target,concurrency,part_size_mb,bytes,seconds,mb_per_s" > "$RESULTS_CSV"

echo "--- :mag: storage assertions"
echo "TMPDIR=${TMPDIR:-/tmp}"
df -hT /tmp 2>/dev/null || df -h /tmp
df -hT "$TMPFS_DIR" 2>/dev/null || df -h "$TMPFS_DIR"   # expect tmpfs
grep -E ' / | /mnt/tmpfs ' /proc/mounts || true
echo "instance: $(grep -c ^processor /proc/cpuinfo) vCPU visible"

echo "--- :wrench: tools (aws-cli & jq already installed by assume-role.sh)"
command -v curl >/dev/null 2>&1 || apk add --quiet --no-progress curl
if ! command -v s5cmd >/dev/null 2>&1; then
  S5_VER=2.3.0
  curl -sL "https://github.com/peak/s5cmd/releases/download/v${S5_VER}/s5cmd_${S5_VER}_Linux-64bit.tar.gz" \
    | tar xz -C /usr/local/bin s5cmd
fi
s5cmd version; aws --version

echo "--- :earth_asia: region + target object"
BUCKET=buildkite-agent-stack-k8s-cache
# The OIDC role isn't granted s3:GetBucketLocation, so resolve region from EC2
# IMDS (best-effort) and fall back to us-east-1 (node DNS is *.ec2.internal).
AWS_REGION="${AWS_REGION:-}"
if [ -z "$AWS_REGION" ]; then
  IMDS_TOKEN="$(curl -sf -X PUT 'http://169.254.169.254/latest/api/token' \
    -H 'X-aws-ec2-metadata-token-ttl-seconds: 60' 2>/dev/null || true)"
  AWS_REGION="$(curl -sf -H "X-aws-ec2-metadata-token: ${IMDS_TOKEN}" \
    'http://169.254.169.254/latest/meta-data/placement/region' 2>/dev/null || true)"
fi
[ -z "$AWS_REGION" ] && AWS_REGION=us-east-1
export AWS_REGION AWS_DEFAULT_REGION="$AWS_REGION"

# The OIDC role grants GetObject/PutObject on the bucket's keys, but NOT
# ListBucket/GetBucketLocation. We therefore cannot enumerate the bucket to find
# the real cache archive's key for the raw-tool (s5cmd / aws cp) legs.
#
# Resolution: the SDK legs use the REAL agent restore (`go_mod`, ~406 MB,
# content-addressed key handled internally by the agent). The raw-tool legs use
# a synthetic, same-size payload we upload once. Same bucket, same region, same
# object size -> a fair transfer-bandwidth comparison (content is irrelevant; S3
# transfers raw bytes with no on-the-wire compression).
PAYLOAD_MB=406
OBJ_BYTES=$((PAYLOAD_MB * 1000 * 1000))
OBJ_KEY="benchmark/cache-bench-payload-${BUILDKITE_JOB_ID:-local}"
OBJ="s3://$BUCKET/$OBJ_KEY"
echo "Region=$AWS_REGION. Building ${PAYLOAD_MB}MB synthetic payload and uploading to $OBJ"
head -c "$OBJ_BYTES" /dev/urandom > "$EBS_DIR/payload"
aws s3 cp "$EBS_DIR/payload" "$OBJ"
rm -f "$EBS_DIR/payload"
echo "Synthetic raw-tool object: $OBJ ($OBJ_BYTES bytes)"

cleanup () { aws s3 rm "$OBJ" >/dev/null 2>&1 || true; }
trap cleanup EXIT

record () { # experiment tool target conc part bytes seconds  (mb_per_s computed)
  local mbps; mbps=$(awk "BEGIN{printf \"%.2f\", $6/1000000/$7}")
  echo "$1,$2,$3,$4,$5,$6,$7,$mbps" >> "$RESULTS_CSV"
  echo "[$1] $2 -> $3 (c=$4 p=$5): ${mbps} MB/s (${7}s)"
}

timed () { # dest_path -- cmd...   -> prints elapsed seconds
  # busybox `date` has no %N, so use bash 5's $EPOCHREALTIME (sec.microsec).
  local dest="$1"; shift; [ "$1" = "--" ] && shift
  rm -f "$dest"
  # Send the timed command's own output to the job log (fd2) so the command
  # substitution only captures the elapsed seconds printed by awk below.
  local start end; start=$EPOCHREALTIME; "$@" 1>&2; end=$EPOCHREALTIME
  awk "BEGIN{printf \"%.3f\", $end-$start}"
}

# ---------------------------------------------------------------------------
echo "+++ :one: Experiment 1 — tmpfs vs EBS"

run_agent_restore () { # tmpdir label  (reads transfer_speed metric)
  rm -rf /root/.cache/go-mod                  # force a real re-download
  local log start end elapsed sp
  echo ">>> BENCHMARK-MARKER exp1-agent target=$2"
  log=$(mktemp)
  start=$EPOCHREALTIME
  TMPDIR="$1" buildkite-agent cache restore --name go_mod 2>&1 | tee "$log"
  end=$EPOCHREALTIME
  # The nested agent streams its logs straight to the job, so $log is usually
  # empty; the precise transfer_speed is harvested from the job log separately.
  # Fall back to a wall-clock figure (download+extract) so the row isn't blank.
  sp=$(grep -oE 'transfer_speed=[0-9.]+' "$log" | cut -d= -f2 | sed -n 1p || true)
  [ -z "$sp" ] && sp=$(awk "BEGIN{printf \"%.2f\", ${OBJ_BYTES}/1000000/(${end}-${start})}")
  echo "exp1-agent,agent,$2,,,${OBJ_BYTES},$(awk "BEGIN{printf \"%.3f\",${end}-${start}}"),${sp}" >> "$RESULTS_CSV"
  echo "[exp1-agent] agent -> $2: ${sp} MB/s (wallclock incl. extract)"
}
run_agent_restore "$EBS_DIR"   ebs
run_agent_restore "$TMPFS_DIR" tmpfs

for pair in "ebs:$EBS_DIR" "tmpfs:$TMPFS_DIR"; do
  lbl=${pair%%:*}; dir=${pair#*:}
  s=$(timed "$dir/o" -- s5cmd cp "$OBJ" "$dir/o"); record exp1 s5cmd  "$lbl" "" "" "$OBJ_BYTES" "$s"; rm -f "$dir/o"
  s=$(timed "$dir/o" -- aws s3 cp "$OBJ" "$dir/o"); record exp1 awscli "$lbl" "" "" "$OBJ_BYTES" "$s"; rm -f "$dir/o"
done

# ---------------------------------------------------------------------------
echo "+++ :two: Experiment 2 — concurrency/part_size sweep"

sweep_restore () { # conc part
  export BUILDKITE_AGENT_CACHE_STORE_URL="s3://$BUCKET?concurrency=$1&part_size_mb=$2"
  rm -rf /root/.cache/go-mod
  local log start end sp
  echo ">>> BENCHMARK-MARKER exp2-sweep concurrency=$1 part_size_mb=$2"
  log=$(mktemp)
  start=$EPOCHREALTIME
  TMPDIR="$EBS_DIR" buildkite-agent cache restore --name go_mod 2>&1 | tee "$log"
  end=$EPOCHREALTIME
  sp=$(grep -oE 'transfer_speed=[0-9.]+' "$log" | cut -d= -f2 | sed -n 1p || true)
  [ -z "$sp" ] && sp=$(awk "BEGIN{printf \"%.2f\", ${OBJ_BYTES}/1000000/(${end}-${start})}")
  echo "exp2-sweep,agent,ebs,$1,$2,${OBJ_BYTES},$(awk "BEGIN{printf \"%.3f\",${end}-${start}}"),${sp}" >> "$RESULTS_CSV"
  echo "[exp2-sweep] c=$1 p=$2: ${sp} MB/s (wallclock incl. extract)"
}
sweep_restore 5 5                                   # baseline (current defaults)
for c in 8 16 32; do for p in 16 32 64; do sweep_restore "$c" "$p"; done; done

# restore default URL for any later steps
export BUILDKITE_AGENT_CACHE_STORE_URL="s3://$BUCKET"

# ---------------------------------------------------------------------------
echo "+++ :three: Experiment 3 — s5cmd reference"

for pair in "ebs:$EBS_DIR" "tmpfs:$TMPFS_DIR"; do
  lbl=${pair%%:*}; dir=${pair#*:}
  s=$(timed "$dir/o" -- s5cmd cp "$OBJ" "$dir/o");            record exp3 s5cmd-default "$lbl" ""   ""  "$OBJ_BYTES" "$s"; rm -f "$dir/o"
  s=$(timed "$dir/o" -- s5cmd cp -c 16 -p 32 "$OBJ" "$dir/o"); record exp3 s5cmd-tuned   "$lbl" "16" "32" "$OBJ_BYTES" "$s"; rm -f "$dir/o"
done

echo "--- :bar_chart: render results"
{
  echo "# A-1412 cache transfer benchmark results"
  echo
  echo "- SDK legs (\`agent\`): real \`go_mod\` cache archive (~${PAYLOAD_MB} MB, content-addressed)."
  echo "- Raw-tool legs (\`s5cmd\`/\`awscli\`): synthetic \`$OBJ\` ($OBJ_BYTES bytes) — role lacks ListBucket, so a same-size payload is used."
  echo "- Region: $AWS_REGION | Node: $(hostname)"
  echo "- Instance: m7a.xlarge | Root vol: gp3 250GiB / 125 MB/s / 3000 IOPS"
  echo
  echo '| experiment | tool | target | conc | part_mb | MB/s |'
  echo '|---|---|---|---|---|---|'
  tail -n +2 "$RESULTS_CSV" | awk -F, '{printf "| %s | %s | %s | %s | %s | %s |\n",$1,$2,$3,$4,$5,$8}'
} > "$RESULTS_MD"
cat "$RESULTS_MD"
buildkite-agent annotate --style info --context cache-bench < "$RESULTS_MD"
