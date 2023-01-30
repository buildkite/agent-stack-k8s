#!/bin/bash
set -eu

echo "--- Uploading :pipeline:"
buildkite-agent pipeline upload
