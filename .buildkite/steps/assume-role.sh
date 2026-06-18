#!/bin/sh
set -eu

# TODO: design config to apply a plugin only to the checkout container, command
# containers, or a specific container(s)...
# TODO: then replace this script with aws-assume-role-with-web-identity plugin
# (which would also remove the aws-cli/jq dependency installed below).

# This script needs the AWS CLI and jq. The Go job images don't ship them, so
# install them here (the only place they're needed). No-op if already present.
# Detect the package manager so this works on both alpine (apk) and debian (apt) images.
if ! command -v aws >/dev/null 2>&1 || ! command -v jq >/dev/null 2>&1; then
    if command -v apk >/dev/null 2>&1; then
        apk add --quiet --update-cache --no-progress aws-cli jq >/dev/null
    elif command -v apt-get >/dev/null 2>&1; then
        apt-get -qq update >/dev/null
        apt-get -qq install -y --no-install-recommends awscli jq >/dev/null
    else
        echo "^^^ +++"
        echo "No supported package manager (apk/apt-get) found to install aws-cli/jq" >&2
        exit 1
    fi
fi

echo "~~~ :buildkite::key::aws: Requesting an OIDC token for AWS from Buildkite"

role_arn='arn:aws:iam::172840064832:role/pipeline-buildkite-kubernetes-stack-kubernetes-agent-stack'
BUILDKITE_OIDC_TOKEN="$(buildkite-agent oidc request-token --audience sts.amazonaws.com  --aws-session-tag organization_id,organization_slug,pipeline_slug)"

echo "~~~ :aws: Assuming role using OIDC token"
ASSUME_ROLE_RESPONSE="$(aws sts assume-role-with-web-identity \
    --role-arn "${role_arn}" \
    --role-session-name "buildkite-job-${BUILDKITE_JOB_ID}" \
    --web-identity-token "${BUILDKITE_OIDC_TOKEN}"
)"
ASSUME_ROLE_CMD_STATUS=$?

if [ "${ASSUME_ROLE_CMD_STATUS}" -ne 0 ]; then
    echo "^^^ +++"
    echo "Failed to assume AWS role:"
    echo "${ASSUME_ROLE_RESPONSE}"
    exit 1
fi

AWS_ACCESS_KEY_ID="$(echo "${ASSUME_ROLE_RESPONSE}" | jq -r ".Credentials.AccessKeyId")"
AWS_SECRET_ACCESS_KEY="$(echo "${ASSUME_ROLE_RESPONSE}" | jq -r ".Credentials.SecretAccessKey")"
AWS_SESSION_TOKEN="$(echo "${ASSUME_ROLE_RESPONSE}" | jq -r ".Credentials.SessionToken")"
export AWS_ACCESS_KEY_ID
export AWS_SECRET_ACCESS_KEY
export AWS_SESSION_TOKEN
