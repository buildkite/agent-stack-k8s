#!/usr/bin/env bash

set -eufo pipefail

curl --proto '=https' --tlsv1.3 -sSf https://just.systems/install.sh | bash -s -- --to /bin

just lint -v
