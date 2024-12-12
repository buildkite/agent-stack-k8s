#!/bin/sh
set -eu

echo "~~~ :buildkite::key::aws: Requesting an OIDC token for AWS from Buildkite"

role_arn='arn:aws:iam::172840064832:role/pipeline-buildkite-kubernetes-stack-kubernetes-agent-stack'
BUILDKITE_OIDC_TOKEN="$(buildkite-agent oidc request-token --audience sts.amazonaws.com)"

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
