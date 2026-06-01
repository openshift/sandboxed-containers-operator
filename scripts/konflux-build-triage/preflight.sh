#!/bin/bash
#
# Pre-flight checks for Konflux triage: verify tools, authenticate, configure tkn-results.
# Exits non-zero on failure. All output goes to stderr except the final JSON status on stdout.
#

set -euo pipefail

KUBECONFIG_PATH="/tmp/konflux-kubeconfig"
KONFLUX_SERVER="https://api.stone-prd-rh01.pg1f.p1.openshiftapps.com:6443"
TKN_RESULTS_HOST="https://tekton-results-tekton-results.apps.stone-prd-rh01.pg1f.p1.openshiftapps.com"

if [ -z "${KONFLUX_TOKEN:-}" ]; then
    echo '{"ok":false,"error":"KONFLUX_TOKEN is not set"}'
    exit 1
fi

if ! command -v oc &>/dev/null; then
    echo '{"ok":false,"error":"oc CLI not found"}'
    exit 1
fi

if ! command -v gh &>/dev/null; then
    echo '{"ok":false,"error":"gh CLI not found"}'
    exit 1
fi

if ! gh auth status &>/dev/stderr; then
    echo '{"ok":false,"error":"gh is not authenticated — run gh auth login"}'
    exit 1
fi

if ! oc login --token="$KONFLUX_TOKEN" --server="$KONFLUX_SERVER" --kubeconfig="$KUBECONFIG_PATH" &>/dev/stderr; then
    echo '{"ok":false,"error":"oc login failed — token may be expired"}'
    exit 1
fi

USER=$(oc whoami --kubeconfig="$KUBECONFIG_PATH" 2>/dev/stderr)

TKN_RESULTS_OK=false
if command -v tkn-results &>/dev/null; then
    if tkn-results config set \
        --host="$TKN_RESULTS_HOST" \
        --token="$KONFLUX_TOKEN" \
        --insecure-skip-tls-verify \
        --kubeconfig="$KUBECONFIG_PATH" &>/dev/stderr; then
        TKN_RESULTS_OK=true
    fi
fi

echo "{\"ok\":true,\"user\":\"$USER\",\"kubeconfig\":\"$KUBECONFIG_PATH\",\"tkn_results\":$TKN_RESULTS_OK}"
