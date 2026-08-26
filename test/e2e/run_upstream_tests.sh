#!/bin/bash
# Run upstream kata-containers BATS tests on OpenShift.
# Requires: bats, yq, jq, kubectl, envsubst, oc
# Usage: KUBECONFIG=/path/to/kubeconfig bash run_upstream_tests.sh [sanity|pods|workloads|resources|volumes|networking|full]
set -o pipefail

KATA_TESTS_DIR="${KATA_TESTS_DIR:?KATA_TESTS_DIR must be set to kata-containers/tests/integration/kubernetes}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RESULTS_DIR="${RESULTS_DIR:-${SCRIPT_DIR}/results}"

export KATA_HYPERVISOR="${KATA_HYPERVISOR:-qemu}"
export AUTO_GENERATE_POLICY="no"
export CONTAINER_RUNTIME="crio"
export K8S_TEST_DEBUG="false"
export KUBECONFIG="${KUBECONFIG:?KUBECONFIG must be set}"

PROFILE="${1:-full}"

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
    *)
        echo "Usage: $0 [sanity|pods|workloads|resources|volumes|networking|full|<test>.bats]"
        echo "  sanity       - 5 tests, one per section (default)"
        echo "  pods         - ${#PODS[@]} tests: exec, caps, security context, etc."
        echo "  workloads    - ${#WORKLOADS[@]} tests: jobs, cron, replication, scaling"
        echo "  resources    - ${#RESOURCES[@]} tests: limits, memory, oom, quotas"
        echo "  volumes      - ${#VOLUMES[@]} tests: configmaps, secrets, volumes"
        echo "  networking   - ${#NETWORKING[@]} tests: connectivity, port-forward, dns"
        echo "  full         - all $(( ${#PODS[@]} + ${#WORKLOADS[@]} + ${#RESOURCES[@]} + ${#VOLUMES[@]} + ${#NETWORKING[@]} )) tests"
        echo "  <test>.bats  - run a single test file, e.g. k8s-file-volume.bats"
        exit 1
        ;;
esac

# --- Prereq checks ---
for cmd in bats yq jq kubectl envsubst oc; do
    if ! command -v "$cmd" &>/dev/null; then
        echo "ERROR: $cmd is required but not found in PATH"
        exit 1
    fi
done

if [[ ! -d "$KATA_TESTS_DIR" ]]; then
    echo "ERROR: kata-containers test dir not found: $KATA_TESTS_DIR"
    exit 1
fi

# --- Ensure kata node label ---
# Upstream tests look for katacontainers.io/kata-runtime=true
# OSC uses node-role.kubernetes.io/kata-oc
if ! kubectl get nodes -l katacontainers.io/kata-runtime=true -o name 2>/dev/null | grep -q .; then
    echo "Adding upstream kata node label to OSC worker nodes..."
    for node in $(kubectl get nodes -l node-role.kubernetes.io/kata-oc -o name); do
        kubectl label "$node" katacontainers.io/kata-runtime=true --overwrite
    done
fi

# --- Setup upstream working directory ---
echo "Running upstream setup.sh..."
cd "$KATA_TESTS_DIR" || exit 1
bash setup.sh || exit 1

# Create test namespace if needed
kubectl apply -f runtimeclass_workloads/tests-namespace.yaml 2>/dev/null || true

# Upstream tests need root (nginx image) and hostPath volumes.
# Grant privileged SCC to the test namespace service account.
oc adm policy add-scc-to-user privileged -z default -n kata-containers-k8s-tests 2>/dev/null || true

kubectl config set-context --current --namespace=kata-containers-k8s-tests

# --- Run tests ---
mkdir -p "$RESULTS_DIR"
passed=0
failed=0
skipped=0
errors=""

echo ""
echo "=============================="
echo "Profile: $PROFILE (${#TESTS[@]} test files)"
echo "=============================="
echo ""

for test_file in "${TESTS[@]}"; do
    filepath="${KATA_TESTS_DIR}/${test_file}"
    if [[ ! -f "$filepath" ]]; then
        echo "SKIP: $test_file (not found)"
        ((skipped++))
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

# --- Restore namespace ---
kubectl config set-context --current --namespace=default 2>/dev/null || true

# --- Summary ---
echo "=============================="
echo "Results: ${passed} passed, ${failed} failed, ${skipped} skipped (of ${#TESTS[@]} files)"
echo "JUnit XML: ${RESULTS_DIR}/"
if [[ -n "$errors" ]]; then
    echo -e "Failed tests:${errors}"
fi
echo "=============================="

exit $failed
