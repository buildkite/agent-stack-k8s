#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

echo "--- I'm burnin' up and out and growing bored..."
printf "Stalling one hour"
for i in `seq 1 60`; do
  sleep 60
  printf "."
done

echo "Done! :tada:"
exit 0
