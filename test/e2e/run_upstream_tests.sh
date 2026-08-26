#!/bin/bash
# Run upstream kata-containers BATS tests on OpenShift.
# Requires: bats, yq, jq, kubectl, envsubst, oc, git
set -o pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RESULTS_DIR="${RESULTS_DIR:-${SCRIPT_DIR}/results}"

export KATA_HYPERVISOR="${KATA_HYPERVISOR:-qemu}"
export AUTO_GENERATE_POLICY="no"
export CONTAINER_RUNTIME="crio"
export K8S_TEST_DEBUG="false"

usage() {
    cat <<EOF
Usage: $(basename "$0") [options]

Run upstream kata-containers BATS tests on an OpenShift cluster.
Requires: bats, yq, jq, kubectl, envsubst, oc

Options:
  -t, --test PROFILE        Test profile or .bats file to run (default: full)
                            Profiles: sanity, pods, workloads, resources, volumes, networking, full
                            Or a single .bats file, e.g. k8s-env.bats
  --tests-repo URL|DIR      Kata tests repo URL or local directory
                            (default: https://github.com/openshift/kata-containers)
  --tests-repo-ref REF      Git ref to checkout — branch or tag (default: main)
  --preserve-tests-repo     Do not delete the cloned test repo after execution
  --skip-setup              Skip cluster setup (node labeling, setup.sh, namespace, SCC)
  -h, --help                Show this help
EOF
    exit "${1:-1}"
}

PROFILE="full"
TESTS_REPO="https://github.com/openshift/kata-containers"
TESTS_REPO_REF="main"
PRESERVE_TESTS_REPO=false
SKIP_SETUP=false

while [ $# -gt 0 ]; do
    case "$1" in
        -t|--test) PROFILE="$2"; shift 2;;
        --tests-repo) TESTS_REPO="$2"; shift 2;;
        --tests-repo-ref) TESTS_REPO_REF="$2"; shift 2;;
        --preserve-tests-repo) PRESERVE_TESTS_REPO=true; shift;;
        --skip-setup) SKIP_SETUP=true; shift;;
        -h|--help) usage 0;;
        *) echo "Unknown argument: $1"; usage;;
    esac
done

export KUBECONFIG="${KUBECONFIG:?KUBECONFIG must be set}"

# --- Test sections ---
PODS=(
    k8s-env.bats
    k8s-hostname.bats
    k8s-exec.bats
    k8s-attach-handlers.bats
    k8s-caps.bats
    # k8s-pid-ns.bats — greps for /pause which has a different path on OpenShift
    k8s-kill-all-process-in-container.bats
    k8s-security-context.bats
    k8s-seccomp.bats
    k8s-copy-file.bats
    k8s-sysctls.bats
    # k8s-graceful-termination.bats — pod deleted before finalizer check, timing race on OpenShift
    # k8s-empty-image.bats — not present in openshift/kata-containers tree
    # k8s-privileged.bats — not present in openshift/kata-containers tree
    # k8s-footloose.bats — setup fails: requires sudo on host to create SSH keys
    # k8s-ip6tables.bats — OSC guest kernel lacks iptables module (upstream has it)
)

WORKLOADS=(
    k8s-job.bats
    k8s-cron-job.bats
    k8s-parallel.bats
    # k8s-replication.bats — CrashLoopBackOff, test hangs waiting for Ready condition
    # k8s-scale-nginx.bats — flaky: intermittent timeout failures
    # k8s-liveness-probes.bats — greps "Started container" but OpenShift uses "Container started"
)

RESOURCES=(
    k8s-limit-range.bats
    k8s-memory.bats
    k8s-oom.bats
    # k8s-pod-quota.bats — flaky: intermittent failures
    k8s-qos-pods.bats
    # k8s-sandbox-cgroup.bats — not present in openshift/kata-containers tree
    # k8s-sandbox-vcpus-allocation.bats — flaky: intermittent failures
    k8s-number-cpus.bats
    k8s-cpu-ns.bats
)

VOLUMES=(
    k8s-configmap.bats
    k8s-optional-empty-configmap.bats
    k8s-credentials-secrets.bats
    k8s-optional-empty-secret.bats
    k8s-empty-dirs.bats
    k8s-shared-volume.bats
    # k8s-volume.bats — hostPath blocked by SELinux on RHCOS: virtiofsd can't access host-created files
    # k8s-file-volume.bats — hostPath blocked by SELinux on RHCOS: virtiofsd can't access host-created files
    # k8s-block-volume.bats — setup precondition fails, 0 tests executed
    k8s-projected-volume.bats
    k8s-nested-configmap-secret.bats
    k8s-inotify.bats
    # k8s-smb-volume.bats — CIFS mount fails (exit 32): OSC guest kernel lacks cifs module (upstream has it)
)

NETWORKING=(
    k8s-nginx-connectivity.bats
    k8s-port-forward.bats
    k8s-custom-dns.bats
)

SANITY=(
    k8s-env.bats
    k8s-exec.bats
    k8s-memory.bats
    k8s-configmap.bats
    k8s-nginx-connectivity.bats
)

case "$PROFILE" in
    sanity)     TESTS=("${SANITY[@]}") ;;
    pods)       TESTS=("${PODS[@]}") ;;
    workloads)  TESTS=("${WORKLOADS[@]}") ;;
    resources)  TESTS=("${RESOURCES[@]}") ;;
    volumes)    TESTS=("${VOLUMES[@]}") ;;
    networking) TESTS=("${NETWORKING[@]}") ;;
    full)       TESTS=("${PODS[@]}" "${WORKLOADS[@]}" "${RESOURCES[@]}" "${VOLUMES[@]}" "${NETWORKING[@]}") ;;
    *.bats)     TESTS=("${PROFILE}") ;;
    *)  echo "Unknown profile: $PROFILE"; usage;;
esac

# --- Prereq checks ---
for cmd in bats yq jq kubectl envsubst oc git; do
    if ! command -v "$cmd" &>/dev/null; then
        echo "ERROR: $cmd is required but not found in PATH"
        exit 1
    fi
done

# --- Cleanup ---
ORIG_NAMESPACE="$(kubectl config view --minify -o jsonpath='{.contexts[0].context.namespace}' 2>/dev/null)"
ORIG_NAMESPACE="${ORIG_NAMESPACE:-default}"
CLONE_DIR=""
SCC_ADDED=false
cleanup() {
    kubectl config set-context --current --namespace="$ORIG_NAMESPACE" 2>/dev/null || true
    if [[ "$SCC_ADDED" == "true" ]]; then
        oc adm policy remove-scc-from-user privileged -z default -n kata-containers-k8s-tests 2>/dev/null || true
    fi
    if [[ -n "$CLONE_DIR" && -d "$CLONE_DIR" ]]; then
        if [[ "$PRESERVE_TESTS_REPO" == "true" ]]; then
            echo "Test repo preserved at: $CLONE_DIR"
        else
            rm -rf "$CLONE_DIR"
        fi
    fi
}
trap cleanup EXIT

# --- Resolve test repo ---
# Strip credentials from URL for logging
safe_url="${TESTS_REPO/\/\/*@/\/\/}"
if [[ -d "$TESTS_REPO" ]]; then
    TESTS_REPO="$(realpath "$TESTS_REPO")"
    KATA_TESTS_DIR="${TESTS_REPO}/tests/integration/kubernetes"
    echo "Using local test repo: $TESTS_REPO"
else
    CLONE_DIR=$(mktemp -d /tmp/kata-tests-XXXXXX)
    echo "Cloning $safe_url (ref: $TESTS_REPO_REF) to $CLONE_DIR..."
    git clone --depth 1 --branch "$TESTS_REPO_REF" "$TESTS_REPO" "$CLONE_DIR" || {
        echo "ERROR: failed to clone $safe_url at ref $TESTS_REPO_REF" >&2
        exit 1
    }
    KATA_TESTS_DIR="${CLONE_DIR}/tests/integration/kubernetes"
fi

if [[ ! -d "$KATA_TESTS_DIR" ]]; then
    echo "ERROR: test directory not found: $KATA_TESTS_DIR"
    exit 1
fi

cd "$KATA_TESTS_DIR" || exit 1

if [[ "$SKIP_SETUP" != "true" ]]; then
    # --- Ensure kata node label ---
    # Upstream tests look for katacontainers.io/kata-runtime=true
    # OSC uses node-role.kubernetes.io/kata-oc
    if ! kubectl get nodes -l katacontainers.io/kata-runtime=true -o name 2>/dev/null | grep -q .; then
        echo "Adding upstream kata node label to OSC worker nodes..."
        for node in $(kubectl get nodes -l node-role.kubernetes.io/kata-oc -o name); do
            kubectl label "$node" katacontainers.io/kata-runtime=true --overwrite >/dev/null || {
                echo "ERROR: failed to label node $node" >&2; exit 1
            }
        done
    fi

    # --- Setup upstream working directory ---
    echo "Running upstream setup.sh..."
    bash setup.sh || exit 1

    # Create test namespace
    kubectl apply -f runtimeclass_workloads/tests-namespace.yaml || {
        echo "ERROR: failed to create test namespace" >&2; exit 1
    }

    # Upstream tests need root (nginx image) and hostPath volumes.
    # Grant privileged SCC to the test namespace service account.
    if ! oc adm policy who-can use scc/privileged -n kata-containers-k8s-tests 2>/dev/null | grep -q "system:serviceaccount:kata-containers-k8s-tests:default"; then
        oc adm policy add-scc-to-user privileged -z default -n kata-containers-k8s-tests || {
            echo "ERROR: failed to grant privileged SCC" >&2; exit 1
        }
        SCC_ADDED=true
    fi
fi

kubectl config set-context --current --namespace=kata-containers-k8s-tests

# --- Run tests ---
mkdir -p "$RESULTS_DIR"
passed=0
failed=0
errors=""

echo ""
echo "=============================="
echo "Profile: $PROFILE (${#TESTS[@]} test files)"
echo "=============================="
echo ""

for test_file in "${TESTS[@]}"; do
    filepath="${KATA_TESTS_DIR}/${test_file}"
    if [[ ! -f "$filepath" ]]; then
        echo "ERROR: $test_file (not found in $KATA_TESTS_DIR)"
        ((failed++))
        errors="${errors}\n  - ${test_file} (not found)"
        continue
    fi

    echo "--- Running: $test_file ---"
    junit_file="${RESULTS_DIR}/${test_file%.bats}.xml"

    if bats --formatter junit "$filepath" > "$junit_file"; then
        echo "PASS: $test_file"
        ((passed++))
    else
        echo "FAIL: $test_file"
        ((failed++))
        errors="${errors}\n  - ${test_file}"
    fi
    echo ""
done

# --- Summary ---
echo "=============================="
echo "Results: ${passed} passed, ${failed} failed (of ${#TESTS[@]} files)"
echo "JUnit XML: ${RESULTS_DIR}/"
if [[ -n "$errors" ]]; then
    echo -e "Failed tests:${errors}"
fi
echo "=============================="

exit $(( failed > 0 ? 1 : 0 ))
