#!/usr/bin/env sh
set -euo pipefail

if [ -f /app/src/LICENSE ]; then
  echo "LICENSE found!"
  echo "$(cat /app/src/LICENSE)"
  exit 0
else
  echo "No LICENSE found!"
  exit 1
fi
