#!/usr/bin/env bash
#
# Downloads the Kubernetes JSON schema definitions for a given version.
#
# Usage:
#   ./update-k8s-schema.sh [VERSION]
#
# Example:
#   ./update-k8s-schema.sh 1.36.0
#
set -euo pipefail

VERSION="${1:-1.35.0}"
URL="https://raw.githubusercontent.com/yannh/kubernetes-json-schema/master/v${VERSION}/_definitions.json"
OUTPUT="$(dirname "$0")/kubernetes-v${VERSION}.json"

echo "Downloading Kubernetes schema v${VERSION}..."
curl -fsSL "${URL}" -o "${OUTPUT}"

echo "Written to ${OUTPUT}"
echo "File size: $(du -h "${OUTPUT}" | cut -f1)"
echo ""
echo "Don't forget to update \$ref paths in values.schema.json if the version changed."
