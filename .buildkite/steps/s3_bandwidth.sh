#!/usr/bin/env sh

set -euf

echo --- :hammer: Installing tools
apk add --update-cache --no-progress aws-cli curl jq

. .buildkite/steps/assume-role.sh

echo --- :hammer: installing s5cmd
curl -L https://github.com/peak/s5cmd/releases/download/v2.3.0/s5cmd_2.3.0_Linux-64bit.tar.gz | tar xz

echo --- :hammer: Testing bandwidth
dd if=/dev/zero of=/tmp/testfile bs=1M count=1000
./s5cmd cp -sp -c 20 "/tmp/testfile" "s3://buildkite-agent-stack-k8s-cache/test-file"
