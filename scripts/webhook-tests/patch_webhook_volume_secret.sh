#!/bin/bash
# Align the controller-manager Deployment volume used for admission webhook TLS
# with the Secret issued by cert-manager (Certificate "serving-cert").
#
# Why: On some clusters (e.g. OLM-installed operator), the Deployment may mount
# controller-manager-service-cert at the webhook path while cert-manager injects
# clientConfig.caBundle from the CA that signs webhook-server-cert. The API
# server then fails TLS verification (x509: certificate signed by unknown authority).
#
# This script finds the volume by name (default: webhook-cert), resolves its index
# in the volumes list, and patches secretName to webhook-server-cert — no
# hardcoded volume index.
#
# Usage (from repo root, with KUBECONFIG set):
#   bash scripts/webhook-tests/patch_webhook_volume_secret.sh
#
# Environment overrides: NAMESPACE, DEPLOYMENT, VOLUME_NAME, SECRET_NAME

set -euo pipefail

NAMESPACE="${NAMESPACE:-openshift-sandboxed-containers-operator}"
DEPLOYMENT="${DEPLOYMENT:-controller-manager}"
VOLUME_NAME="${VOLUME_NAME:-webhook-cert}"
SECRET_NAME="${SECRET_NAME:-webhook-server-cert}"

kubectl_or_oc() {
	if command -v oc &>/dev/null; then
		oc "$@"
	else
		kubectl "$@"
	fi
}

INDEX=$(kubectl_or_oc get deployment "$DEPLOYMENT" -n "$NAMESPACE" -o json |
	jq -r --arg n "$VOLUME_NAME" '.spec.template.spec.volumes | to_entries | map(select(.value.name == $n)) | .[0].key // empty')

if [[ -z "$INDEX" || "$INDEX" == "null" ]]; then
	echo "No volume named '$VOLUME_NAME' in Deployment/$DEPLOYMENT — nothing to patch." >&2
	exit 0
fi

CURRENT=$(kubectl_or_oc get deployment "$DEPLOYMENT" -n "$NAMESPACE" -o json |
	jq -r --arg n "$VOLUME_NAME" '.spec.template.spec.volumes[] | select(.name == $n) | .secret.secretName // empty')

if [[ "$CURRENT" == "$SECRET_NAME" ]]; then
	echo "Volume '$VOLUME_NAME' already uses Secret '$SECRET_NAME'."
	exit 0
fi

echo "Patching Deployment/$DEPLOYMENT volume '$VOLUME_NAME': '${CURRENT:-<unset>}' -> '$SECRET_NAME' (list index $INDEX)"
kubectl_or_oc patch deployment "$DEPLOYMENT" -n "$NAMESPACE" --type=json \
	-p="[{\"op\":\"replace\",\"path\":\"/spec/template/spec/volumes/$INDEX/secret/secretName\",\"value\":\"$SECRET_NAME\"}]"

echo "Roll out: kubectl/oc rollout status deployment/$DEPLOYMENT -n $NAMESPACE"
