#!/usr/bin/env bash

set -o errexit
set -o pipefail
set -o nounset

# RBAC Permissions Check Script
# This script checks if the current user/service account has the required permissions

# Usage function
usage() {
    echo "Usage: $0 [-n namespace] [-h]"
    echo "  -n namespace  Check permissions in specific namespace (default: current context namespace)"
    echo "  -h           Show this help message"
    exit 1
}

# Parse command line arguments
NAMESPACE=""
while getopts "n:h" opt; do
    case $opt in
        n)
            NAMESPACE="$OPTARG"
            ;;
        h)
            usage
            ;;
        \?)
            echo "Invalid option: -$OPTARG" >&2
            usage
            ;;
    esac
done

# Build namespace flag for kubectl commands
NAMESPACE_FLAG=""
if [ -n "$NAMESPACE" ]; then
    NAMESPACE_FLAG="--namespace=$NAMESPACE"
    echo "Checking RBAC permissions in namespace: $NAMESPACE"
else
    echo "Checking RBAC permissions in current context namespace"
fi
echo "================================"

# Track overall success
OVERALL_SUCCESS=true

# Function to check permission and report result
check_permission() {
    local resource=$1
    local verb=$2
    local apigroup=$3
    local display_name=$4

    # For non-core API groups, use the format: resource.group
    if [ -n "$apigroup" ] && [ "$apigroup" != '""' ]; then
        local full_resource="${resource}.${apigroup}"
        kubectl auth can-i "$verb" "$full_resource" $NAMESPACE_FLAG
    else
        kubectl auth can-i "$verb" "$resource" $NAMESPACE_FLAG
    fi

    if [ $? -eq 0 ]; then
        echo "✓ $display_name: $verb $resource"
    else
        echo "✗ $display_name: $verb $resource"
        OVERALL_SUCCESS=false
    fi
}

echo "Core API Group (v1) Resources:"
echo "------------------------------"

# Secrets permissions
check_permission "secrets" "get" "" "Secrets"

# Pods permissions
check_permission "pods" "get" "" "Pods"
check_permission "pods" "list" "" "Pods"
check_permission "pods" "watch" "" "Pods"
check_permission "pods" "delete" "" "Pods"

# Events permissions
check_permission "events" "list" "" "Events"

echo ""
echo "Batch API Group Resources:"
echo "-------------------------"

# Jobs permissions
check_permission "jobs" "get" "batch" "Jobs"
check_permission "jobs" "list" "batch" "Jobs"
check_permission "jobs" "watch" "batch" "Jobs"
check_permission "jobs" "create" "batch" "Jobs"
check_permission "jobs" "delete" "batch" "Jobs"
check_permission "jobs" "update" "batch" "Jobs"

echo ""
echo "================================"
if [ "$OVERALL_SUCCESS" = true ]; then
    echo "✓ All required permissions are granted"
    exit 0
else
    echo "✗ Some permissions are missing"
    exit 1
fi
