#!/bin/bash
# Run the OpenShift sandboxed containers sanity tests.
# Requires: bats, oc, envsubst
set -o pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RESULTS_DIR="${RESULTS_DIR:-${SCRIPT_DIR}/results}"
RESULTS_DIR="${RESULTS_DIR}/$(date +"%Y-%m-%d_%H%M%S")"

usage() {
    cat <<EOF
Usage: $(basename "$0") [options]

Check that a workload runs on an OpenShift cluster with OpenShift sandboxed
containers installed: create a pod, check it runs, delete it and check it is
gone. On failure, the logs that explain it are printed and written to the
results directory.

The cluster is not inspected: pass the workload and the RuntimeClass the
cluster was installed with.

Requires: bats, oc, envsubst

Options:
  -w, --workload TYPE       Workload to check: kata (default: kata)
  -r, --runtime_class NAME  RuntimeClass to run the pod with
                            (default: kata for the kata workload)
      --timeout SECONDS     How long to wait for the pod to be ready
                            (default: 180 for the kata workload)
  -h, --help                Show this help

Environment:
  SANITY_IMAGE      Workload image (default:
                    registry.access.redhat.com/ubi9/ubi-minimal:latest)
  SANITY_NAMESPACE  Namespace to run the workload in (default: osc-sanity)
  SANITY_KEEP_NS    Set to keep the namespace after the run, for debugging
  RESULTS_DIR       Where to write results (default: ${SCRIPT_DIR}/results)
EOF
    exit "${1:-1}"
}

WORKLOAD="kata"
RUNTIME_CLASS=""
TIMEOUT=""

while [ $# -gt 0 ]; do
    case "$1" in
        -w|--workload) WORKLOAD="$2"; shift 2;;
        -r|--runtime_class) RUNTIME_CLASS="$2"; shift 2;;
        --timeout) TIMEOUT="$2"; shift 2;;
        -h|--help) usage 0;;
        *) echo "Unknown argument: $1"; usage;;
    esac
done

export KUBECONFIG="${KUBECONFIG:?KUBECONFIG must be set}"

for cmd in bats oc envsubst; do
    if ! command -v "$cmd" &>/dev/null; then
        echo "ERROR: $cmd is required but not found in PATH" >&2
        exit 1
    fi
done

case "$WORKLOAD" in
    kata)
        RUNTIME_CLASS="${RUNTIME_CLASS:-kata}"
        TIMEOUT="${TIMEOUT:-180}"
        ;;
    *)
        echo "Unknown workload: $WORKLOAD"
        usage
        ;;
esac

if ! oc get runtimeclass "$RUNTIME_CLASS" &>/dev/null; then
    echo "ERROR: RuntimeClass $RUNTIME_CLASS does not exist in the cluster" >&2
    exit 1
fi

export SANITY_WORKLOAD="$WORKLOAD"
export SANITY_RUNTIME_CLASS="$RUNTIME_CLASS"
export SANITY_TIMEOUT="$TIMEOUT"
export SANITY_IMAGE="${SANITY_IMAGE:-registry.access.redhat.com/ubi9/ubi-minimal:latest}"
export SANITY_NAMESPACE="${SANITY_NAMESPACE:-osc-sanity}"

mkdir -p "$RESULTS_DIR" || exit 1
export SANITY_LOG_FILE="${RESULTS_DIR}/${WORKLOAD}-failure.log"

NAMESPACE_CREATED=false
cleanup() {
    if [[ "$NAMESPACE_CREATED" != "true" || -n "${SANITY_KEEP_NS:-}" ]]; then
        return
    fi

    # The workload pod is already gone, so the namespace should follow within
    # seconds. Wait for it: one that lingers means something in the cluster is
    # stuck, which is worth reporting rather than leaving behind for the next
    # run to trip over.
    if ! oc delete namespace "$SANITY_NAMESPACE" \
        --ignore-not-found --wait --timeout=120s; then
        echo "ERROR: namespace $SANITY_NAMESPACE was not deleted within 120s" >&2
        oc get namespace "$SANITY_NAMESPACE" -o yaml >&2 || true
        exit 1
    fi
}
trap cleanup EXIT

cat <<EOF

==============================
Workload:     $SANITY_WORKLOAD
RuntimeClass: $SANITY_RUNTIME_CLASS
Image:        $SANITY_IMAGE
Namespace:    $SANITY_NAMESPACE
Timeout:      ${SANITY_TIMEOUT}s
Results:      $RESULTS_DIR
==============================

EOF

# A run interrupted before its cleanup finished can leave the namespace
# terminating, and pods cannot be created in one. Wait for it to go away
# rather than failing later on the pod.
if [[ "$(oc get namespace "$SANITY_NAMESPACE" \
    -o jsonpath='{.status.phase}' 2>/dev/null)" == "Terminating" ]]; then
    echo "Waiting for the $SANITY_NAMESPACE namespace of a previous run to be deleted..."
    oc wait --for=delete "namespace/$SANITY_NAMESPACE" --timeout=180s || exit 1
fi

if ! oc get namespace "$SANITY_NAMESPACE" &>/dev/null; then
    oc create namespace "$SANITY_NAMESPACE" || exit 1
    NAMESPACE_CREATED=true
fi

bats --formatter pretty --report-formatter junit --output "$RESULTS_DIR" \
    "${SCRIPT_DIR}/sanity/${WORKLOAD}.bats"
rc=$?

echo "=============================="
if [[ $rc -eq 0 ]]; then
    echo "PASS: $SANITY_WORKLOAD sanity check"
else
    echo "FAIL: $SANITY_WORKLOAD sanity check"
    echo "Failure logs: $SANITY_LOG_FILE"
fi
echo "JUnit XML: $RESULTS_DIR"
echo "=============================="

exit $rc
