#!/usr/bin/env bash
#
# Helpers shared by the sanity tests.
#
# Every variable has a default so that a .bats file can also be run directly
# with bats, without going through run_sanity_tests.sh.

SANITY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

SANITY_WORKLOAD="${SANITY_WORKLOAD:-kata}"
SANITY_RUNTIME_CLASS="${SANITY_RUNTIME_CLASS:-kata}"
SANITY_NAMESPACE="${SANITY_NAMESPACE:-osc-sanity}"
SANITY_IMAGE="${SANITY_IMAGE:-registry.access.redhat.com/ubi9/ubi-minimal:latest}"
SANITY_TIMEOUT="${SANITY_TIMEOUT:-180}"
SANITY_LOG_FILE="${SANITY_LOG_FILE:-/tmp/sanity-${SANITY_WORKLOAD}-failure.log}"
SANITY_TRUSTEE_NS="${SANITY_TRUSTEE_NS:-trustee-operator-system}"
SANITY_KBS_RESOURCE="${SANITY_KBS_RESOURCE:-default/kbsres1/key1}"
SANITY_INITDATA="${SANITY_INITDATA:-}"

export SANITY_WORKLOAD SANITY_RUNTIME_CLASS SANITY_NAMESPACE SANITY_IMAGE \
    SANITY_TIMEOUT SANITY_LOG_FILE SANITY_TRUSTEE_NS SANITY_KBS_RESOURCE \
    SANITY_INITDATA

CAA_NAMESPACE="openshift-sandboxed-containers-operator"
TRUSTEE_DEPLOYMENT="trustee-deployment"

# Write a message to stdout and to the failure log.
log() {
    echo "$*" | tee -a "$SANITY_LOG_FILE"
}

# Run a command and write a titled block with its output to stdout and to the
# failure log. Never fails: a log we cannot collect must not hide the failure
# we are collecting it for.
dump() {
    local title="$1"
    shift

    {
        echo
        echo "=== ${title} ==="
        "$@" 2>&1 || true
    } | tee -a "$SANITY_LOG_FILE"
}

# How long ago the test started, with slack. The collectors bound logs by this
# duration rather than by a timestamp: the kubelet and the node journal
# resolve it against their own clock, while a timestamp taken here is read
# against theirs, and silently matches nothing when the two disagree.
sanity_elapsed() {
    echo $(( $(date +%s) - SANITY_START_EPOCH + 30 ))
}

# Set the pod name, render the pod manifest and record when the test started.
sanity_setup() {
    POD_NAME="sanity-${SANITY_WORKLOAD}"
    POD_YAML="${BATS_TEST_TMPDIR:-/tmp}/${POD_NAME}.yaml"
    SANITY_START_EPOCH="$(date +%s)"
    export POD_NAME POD_YAML SANITY_START_EPOCH

    local template="${SANITY_DIR}/testdata/pod.yaml.in"
    if [[ "$SANITY_WORKLOAD" == "coco" ]]; then
        template="${SANITY_DIR}/testdata/pod-coco.yaml.in"
    fi

    # Only set the initdata annotation when there is initdata to set: an
    # empty one would be worse than none.
    SANITY_ANNOTATIONS=""
    if [[ -n "$SANITY_INITDATA" ]]; then
        # The quotes below are literal: they are the YAML ones.
        # shellcheck disable=SC2089
        SANITY_ANNOTATIONS="  annotations:
    io.katacontainers.config.hypervisor.cc_init_data: \"${SANITY_INITDATA}\""
    fi
    # shellcheck disable=SC2090
    export SANITY_ANNOTATIONS

    # Plain envsubst, with no SHELL-FORMAT argument restricting which names
    # it touches: that is a GNU extension the CI-pinned envsubst (a8m/envsubst)
    # does not have, and does not fail on either — it silently treats the
    # argument as an input file path and falls back to stdin, substituting
    # every $VAR it finds. So the templates must contain no shell variable
    # that isn't meant to be substituted (see the comment in pod-coco.yaml.in).
    envsubst < "$template" > "$POD_YAML"
}

# Node the workload pod was scheduled to, empty if it was not scheduled.
pod_node() {
    oc get pod "$POD_NAME" -n "$SANITY_NAMESPACE" \
        -o jsonpath='{.spec.nodeName}' 2>/dev/null
}

collect_pod_logs() {
    dump "pod ${POD_NAME}" \
        oc get pod "$POD_NAME" -n "$SANITY_NAMESPACE" -o wide
    dump "pod ${POD_NAME} description" \
        oc describe pod "$POD_NAME" -n "$SANITY_NAMESPACE"
    dump "pod ${POD_NAME} logs" \
        oc logs "$POD_NAME" -n "$SANITY_NAMESPACE" --all-containers
}

# Kata and CRI-O journal entries of a node, from the time the test started.
# One debug pod runs both journalctl calls because -t and -u match different
# journal fields, which journalctl ANDs.
collect_node_journal() {
    local node="$1"

    local since
    since="$(sanity_elapsed) seconds ago"

    dump "node ${node} kata and CRI-O journal, last $(sanity_elapsed)s" \
        oc debug "node/${node}" --quiet -- chroot /host /bin/bash -c \
        "journalctl -t kata --since '${since}' -o cat
         echo '--- CRI-O ---'
         journalctl -u crio --since '${since}' -o cat"
}

# Name of the cloud-api-adaptor pod running on a node, empty if there is none.
caa_pod_on_node() {
    oc get pods -n "$CAA_NAMESPACE" -l name=osc-caa-ds \
        --field-selector "spec.nodeName=$1" \
        -o jsonpath='{.items[0].metadata.name}' 2>/dev/null
}

# Logs of the cloud-api-adaptor pod that created the peer pod VM. The node
# journal is of little help for peer pods, the adaptor logs are where the VM
# lifecycle shows up.
collect_caa_logs() {
    local caa="$1"

    dump "cloud-api-adaptor ${caa} logs, last $(sanity_elapsed)s" \
        oc logs -n "$CAA_NAMESPACE" "$caa" \
        --since="$(sanity_elapsed)s"
}

# Logs of Trustee, which only exist when it is installed in this cluster.
collect_trustee_logs() {
    if ! oc get "deployment/${TRUSTEE_DEPLOYMENT}" \
        -n "$SANITY_TRUSTEE_NS" &>/dev/null; then
        log "No ${TRUSTEE_DEPLOYMENT} in namespace ${SANITY_TRUSTEE_NS}:" \
            "Trustee is installed in another cluster, its logs are unavailable"
        return
    fi

    dump "Trustee logs, last $(sanity_elapsed)s" \
        oc logs -n "$SANITY_TRUSTEE_NS" "deployment/${TRUSTEE_DEPLOYMENT}" \
        --all-containers --since="$(sanity_elapsed)s"
}

# Logs that explain a failure of the workload under test.
collect_failure_logs() {
    local node caa

    collect_pod_logs

    if [[ "$SANITY_WORKLOAD" == "coco" ]]; then
        collect_trustee_logs
    fi

    node="$(pod_node)"
    if [[ -z "$node" ]]; then
        log "Pod ${POD_NAME} was not scheduled to a node, no node logs to collect"
        return
    fi

    caa="$(caa_pod_on_node "$node")"

    case "$SANITY_WORKLOAD" in
        kata)
            collect_node_journal "$node"
            ;;
        peer-pods)
            if [[ -z "$caa" ]]; then
                log "No cloud-api-adaptor pod runs on node ${node}"
                return
            fi
            collect_caa_logs "$caa"
            ;;
        coco)
            # A cloud-api-adaptor pod on the node means this is CoCo on peer
            # pods, where the adaptor logs replace the node journal.
            if [[ -n "$caa" ]]; then
                collect_caa_logs "$caa"
            else
                collect_node_journal "$node"
            fi
            ;;
    esac
}
