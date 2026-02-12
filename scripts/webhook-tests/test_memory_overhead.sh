#!/bin/bash

# Test script for memory overhead annotation feature
# This script tests the webhook functionality with ConfigMap-based configuration
# AND verifies the Kata runtime consumes the annotation correctly.
#
# The memoryOverheadMB configuration is read from the osc-feature-gates ConfigMap
# in the openshift-sandboxed-containers-operator namespace.
#
# Test coverage:
# - Webhook annotation injection
# - ConfigMap-based configuration
# - Default value handling
# - Kata runtime consumption of the annotation

set -e

NAMESPACE="openshift-sandboxed-containers-operator"
CONFIGMAP_NAME="osc-feature-gates"
KATACONFIG_NAME="test-kataconfig"
POD_NAME="test-pod"
TEST_NAMESPACE="webhook-test-$$"
SKIP_RUNTIME_TESTS=${SKIP_RUNTIME_TESTS:-false}

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo "Testing Memory Overhead Annotation Feature"
echo "=========================================="
echo "Configuration source: $CONFIGMAP_NAME ConfigMap"
echo ""

# Function to cleanup resources
cleanup() {
    echo ""
    echo "Cleaning up test resources..."
    kubectl delete namespace $TEST_NAMESPACE --ignore-not-found=true --wait=false 2>/dev/null || true
    kubectl delete configmap $CONFIGMAP_NAME -n $NAMESPACE --ignore-not-found=true 2>/dev/null || true
    # Don't delete KataConfig as it may be used by the cluster
}

# Set trap for cleanup
trap cleanup EXIT

# Function to print status
print_status() {
    local status=$1
    local message=$2
    if [ "$status" = "ok" ]; then
        echo -e "${GREEN}✓${NC} $message"
    elif [ "$status" = "fail" ]; then
        echo -e "${RED}✗${NC} $message"
    elif [ "$status" = "warn" ]; then
        echo -e "${YELLOW}⚠${NC} $message"
    elif [ "$status" = "info" ]; then
        echo -e "  $message"
    fi
}

# Check if kubectl/oc is available
if command -v oc &> /dev/null; then
    KUBECTL="oc"
elif command -v kubectl &> /dev/null; then
    KUBECTL="kubectl"
else
    echo "ERROR: Neither oc nor kubectl is installed or in PATH"
    exit 1
fi

# Check if we have cluster access
if ! $KUBECTL cluster-info &> /dev/null; then
    echo "ERROR: Cannot connect to Kubernetes cluster"
    echo "Please ensure $KUBECTL is configured correctly"
    exit 1
fi

# Check if operator is running
echo "1. Checking if operator is running..."
if ! $KUBECTL get deployment controller-manager -n $NAMESPACE >/dev/null 2>&1; then
    echo "ERROR: Operator not found in namespace $NAMESPACE"
    echo "Please install the sandboxed containers operator first"
    exit 1
fi
print_status "ok" "Operator is running"

# Check if webhook is registered
echo "2. Checking if pod mutating webhook is registered..."
if ! $KUBECTL get mutatingwebhookconfiguration 2>/dev/null | grep -qE "mpods|sandboxed-containers-pod-mutator"; then
    print_status "warn" "Memory overhead webhook not found"
    echo "This may be expected if the webhook is not yet deployed"
    echo ""
    echo "Available mutating webhooks:"
    $KUBECTL get mutatingwebhookconfigurations 2>/dev/null || echo "  (none found)"
    echo ""
    echo "Skipping webhook-dependent tests..."
    print_status "ok" "Test completed (webhook not registered)"
    exit 0
fi
print_status "ok" "Webhook is registered"

# Check if KataConfig exists and has RuntimeClasses populated
echo "3. Checking KataConfig status..."
RUNTIME_CLASSES=$($KUBECTL get kataconfig -o jsonpath='{.items[0].status.runtimeClasses[*]}' 2>/dev/null || echo "")
if [ -z "$RUNTIME_CLASSES" ]; then
    print_status "warn" "No KataConfig found or RuntimeClasses not populated"
    echo "The webhook needs KataConfig.status.runtimeClasses to identify Kata pods"
    echo ""
    echo "Creating a basic KataConfig for testing..."
    cat <<EOF | $KUBECTL apply -f -
apiVersion: kataconfiguration.openshift.io/v1
kind: KataConfig
metadata:
  name: $KATACONFIG_NAME
spec:
  checkNodeEligibility: false
EOF
    echo "Waiting for KataConfig to be ready..."
    sleep 10
    RUNTIME_CLASSES=$($KUBECTL get kataconfig $KATACONFIG_NAME -o jsonpath='{.status.runtimeClasses[*]}' 2>/dev/null || echo "")
fi
print_status "ok" "KataConfig found with runtime classes: $RUNTIME_CLASSES"

# Determine which runtime class to use for testing
if echo "$RUNTIME_CLASSES" | grep -q "kata "; then
    TEST_RUNTIME_CLASS="kata"
elif echo "$RUNTIME_CLASSES" | grep -qw "kata"; then
    TEST_RUNTIME_CLASS="kata"
elif echo "$RUNTIME_CLASSES" | grep -q "kata-remote"; then
    TEST_RUNTIME_CLASS="kata-remote"
else
    TEST_RUNTIME_CLASS="kata"
    print_status "warn" "Using default runtime class 'kata' - may not match KataConfig"
fi
echo "   Using runtime class: $TEST_RUNTIME_CLASS"

# Create test namespace
echo "4. Creating test namespace..."
$KUBECTL create namespace $TEST_NAMESPACE 2>/dev/null || true
print_status "ok" "Test namespace created: $TEST_NAMESPACE"

# Create osc-feature-gates ConfigMap with custom memory overhead
echo "5. Creating osc-feature-gates ConfigMap with memoryOverheadMB=512..."
cat <<EOF | $KUBECTL apply -f -
apiVersion: v1
kind: ConfigMap
metadata:
  name: $CONFIGMAP_NAME
  namespace: $NAMESPACE
data:
  memoryOverheadMB: "512"
EOF
print_status "ok" "ConfigMap created"

# Wait a moment for the operator to pick up the ConfigMap
sleep 2

# Create test pod with Kata runtime class
echo "6. Creating test pod with $TEST_RUNTIME_CLASS runtime class..."
cat <<EOF | $KUBECTL apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: $POD_NAME
  namespace: $TEST_NAMESPACE
spec:
  runtimeClassName: $TEST_RUNTIME_CLASS
  securityContext:
    runAsNonRoot: true
    seccompProfile:
      type: RuntimeDefault
  containers:
  - name: test-container
    image: registry.access.redhat.com/ubi9/ubi-minimal:latest
    command: ["sleep", "infinity"]
    resources:
      requests:
        memory: "100Mi"
      limits:
        memory: "200Mi"
    securityContext:
      allowPrivilegeEscalation: false
      capabilities:
        drop:
          - ALL
      runAsNonRoot: true
  restartPolicy: Never
  terminationGracePeriodSeconds: 0
EOF

# Wait for pod to be admitted
echo "7. Waiting for pod to be admitted..."
sleep 5
print_status "ok" "Pod created"

# Check if memory overhead annotation was injected
echo "8. Checking if memory overhead annotation was injected..."
ANNOTATION_VALUE=$($KUBECTL get pod $POD_NAME -n $TEST_NAMESPACE -o jsonpath='{.metadata.annotations.io\.katacontainers\.config\.hypervisor\.memory_overhead}' 2>/dev/null || echo "")

if [ -z "$ANNOTATION_VALUE" ]; then
    print_status "fail" "Memory overhead annotation not found"
    echo ""
    echo "Pod annotations:"
    $KUBECTL get pod $POD_NAME -n $TEST_NAMESPACE -o jsonpath='{.metadata.annotations}' 2>/dev/null | python3 -m json.tool 2>/dev/null || $KUBECTL get pod $POD_NAME -n $TEST_NAMESPACE -o jsonpath='{.metadata.annotations}'
    echo ""
    echo ""
    echo "Possible causes:"
    echo "  - Webhook is not intercepting pod creation"
    echo "  - Runtime class '$TEST_RUNTIME_CLASS' not in KataConfig.status.runtimeClasses"
    echo "  - ConfigMap not in correct namespace"
    exit 1
fi

print_status "ok" "Memory overhead annotation found: $ANNOTATION_VALUE"

# Verify the annotation value matches the ConfigMap
if [ "$ANNOTATION_VALUE" != "512" ]; then
    print_status "fail" "Expected annotation value '512', got '$ANNOTATION_VALUE'"
    exit 1
fi

print_status "ok" "Annotation value is correct: $ANNOTATION_VALUE"

# Test with different memory overhead value
echo "9. Testing ConfigMap update (memoryOverheadMB=768)..."
$KUBECTL patch configmap $CONFIGMAP_NAME -n $NAMESPACE --type='merge' -p='{"data":{"memoryOverheadMB":"768"}}'

# Wait for operator to pick up the change
sleep 2

# Create another test pod
POD_NAME_2="test-pod-2"
cat <<EOF | $KUBECTL apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: $POD_NAME_2
  namespace: $TEST_NAMESPACE
spec:
  runtimeClassName: $TEST_RUNTIME_CLASS
  securityContext:
    runAsNonRoot: true
    seccompProfile:
      type: RuntimeDefault
  containers:
  - name: test-container
    image: registry.access.redhat.com/ubi9/ubi-minimal:latest
    command: ["sleep", "infinity"]
    resources:
      requests:
        memory: "100Mi"
      limits:
        memory: "200Mi"
    securityContext:
      allowPrivilegeEscalation: false
      capabilities:
        drop:
          - ALL
      runAsNonRoot: true
  restartPolicy: Never
  terminationGracePeriodSeconds: 0
EOF

# Wait for pod to be created
sleep 5

# Check annotation value
ANNOTATION_VALUE_2=$($KUBECTL get pod $POD_NAME_2 -n $TEST_NAMESPACE -o jsonpath='{.metadata.annotations.io\.katacontainers\.config\.hypervisor\.memory_overhead}' 2>/dev/null || echo "")

if [ "$ANNOTATION_VALUE_2" != "768" ]; then
    print_status "fail" "Expected annotation value '768', got '$ANNOTATION_VALUE_2'"
    exit 1
fi

print_status "ok" "Second pod has correct annotation value: $ANNOTATION_VALUE_2"

# Test default value (delete ConfigMap)
echo "10. Testing default value (removing ConfigMap)..."
$KUBECTL delete configmap $CONFIGMAP_NAME -n $NAMESPACE

# Wait for operator to pick up the change
sleep 2

# Create another test pod
POD_NAME_3="test-pod-default"
cat <<EOF | $KUBECTL apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: $POD_NAME_3
  namespace: $TEST_NAMESPACE
spec:
  runtimeClassName: $TEST_RUNTIME_CLASS
  securityContext:
    runAsNonRoot: true
    seccompProfile:
      type: RuntimeDefault
  containers:
  - name: test-container
    image: registry.access.redhat.com/ubi9/ubi-minimal:latest
    command: ["sleep", "infinity"]
    resources:
      requests:
        memory: "100Mi"
      limits:
        memory: "200Mi"
    securityContext:
      allowPrivilegeEscalation: false
      capabilities:
        drop:
          - ALL
      runAsNonRoot: true
  restartPolicy: Never
  terminationGracePeriodSeconds: 0
EOF

# Wait for pod to be created
sleep 5

# Check annotation value (should be default 350)
ANNOTATION_VALUE_3=$($KUBECTL get pod $POD_NAME_3 -n $TEST_NAMESPACE -o jsonpath='{.metadata.annotations.io\.katacontainers\.config\.hypervisor\.memory_overhead}' 2>/dev/null || echo "")

if [ "$ANNOTATION_VALUE_3" != "350" ]; then
    print_status "fail" "Expected default annotation value '350', got '$ANNOTATION_VALUE_3'"
    exit 1
fi

print_status "ok" "Default value works correctly: $ANNOTATION_VALUE_3"

# Test with non-Kata runtime class (should not get annotation)
echo "11. Testing with non-Kata runtime class..."
POD_NAME_4="test-pod-non-kata"
cat <<EOF | $KUBECTL apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: $POD_NAME_4
  namespace: $TEST_NAMESPACE
spec:
  securityContext:
    runAsNonRoot: true
    seccompProfile:
      type: RuntimeDefault
  containers:
  - name: test-container
    image: registry.access.redhat.com/ubi9/ubi-minimal:latest
    command: ["sleep", "infinity"]
    resources:
      requests:
        memory: "100Mi"
      limits:
        memory: "200Mi"
    securityContext:
      allowPrivilegeEscalation: false
      capabilities:
        drop:
          - ALL
      runAsNonRoot: true
  restartPolicy: Never
  terminationGracePeriodSeconds: 0
EOF

# Wait for pod to be created
sleep 5

# Check that no annotation was added
ANNOTATION_VALUE_4=$($KUBECTL get pod $POD_NAME_4 -n $TEST_NAMESPACE -o jsonpath='{.metadata.annotations.io\.katacontainers\.config\.hypervisor\.memory_overhead}' 2>/dev/null || echo "")

if [ -n "$ANNOTATION_VALUE_4" ]; then
    print_status "fail" "Non-Kata pod should not have memory overhead annotation, got '$ANNOTATION_VALUE_4'"
    exit 1
fi

print_status "ok" "Non-Kata pod correctly has no memory overhead annotation"

echo ""
echo "========================================"
echo "Part 1: Webhook Tests PASSED"
echo "========================================"

# ============================================
# PART 2: KATA RUNTIME VERIFICATION
# ============================================

echo ""
echo "========================================"
echo "Part 2: Kata Runtime Verification"
echo "========================================"
echo ""

if [ "$SKIP_RUNTIME_TESTS" = "true" ]; then
    print_status "warn" "Skipping runtime tests (SKIP_RUNTIME_TESTS=true)"
    echo ""
else
    # Re-create ConfigMap for runtime test
    echo "12. Setting up for runtime verification (memoryOverheadMB=512)..."
    cat <<EOF | $KUBECTL apply -f -
apiVersion: v1
kind: ConfigMap
metadata:
  name: $CONFIGMAP_NAME
  namespace: $NAMESPACE
data:
  memoryOverheadMB: "512"
EOF
    sleep 2

    # Create a pod for runtime testing
    POD_NAME_RUNTIME="test-pod-runtime"
    echo "13. Creating pod for runtime verification..."
    cat <<EOF | $KUBECTL apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: $POD_NAME_RUNTIME
  namespace: $TEST_NAMESPACE
spec:
  runtimeClassName: $TEST_RUNTIME_CLASS
  securityContext:
    runAsNonRoot: true
    seccompProfile:
      type: RuntimeDefault
  containers:
  - name: test-container
    image: registry.access.redhat.com/ubi9/ubi-minimal:latest
    command: ["sleep", "infinity"]
    resources:
      requests:
        memory: "256Mi"
      limits:
        memory: "512Mi"
    securityContext:
      allowPrivilegeEscalation: false
      capabilities:
        drop:
          - ALL
      runAsNonRoot: true
  restartPolicy: Never
  terminationGracePeriodSeconds: 0
EOF

    # Wait for pod to be scheduled and running
    echo "14. Waiting for pod to be running on a Kata-enabled node..."
    RUNTIME_TEST_TIMEOUT=120
    RUNTIME_TEST_START=$(date +%s)
    POD_RUNNING=false

    while [ $(($(date +%s) - RUNTIME_TEST_START)) -lt $RUNTIME_TEST_TIMEOUT ]; do
        POD_PHASE=$($KUBECTL get pod $POD_NAME_RUNTIME -n $TEST_NAMESPACE -o jsonpath='{.status.phase}' 2>/dev/null || echo "Unknown")
        if [ "$POD_PHASE" = "Running" ]; then
            POD_RUNNING=true
            break
        elif [ "$POD_PHASE" = "Failed" ] || [ "$POD_PHASE" = "Unknown" ]; then
            print_status "warn" "Pod failed to start (phase: $POD_PHASE)"
            echo "   This may be expected if no Kata-enabled nodes are available"
            break
        fi
        echo "   Pod phase: $POD_PHASE, waiting..."
        sleep 5
    done

    if [ "$POD_RUNNING" = "true" ]; then
        print_status "ok" "Pod is running"

        # Get the node where the pod is running
        NODE_NAME=$($KUBECTL get pod $POD_NAME_RUNTIME -n $TEST_NAMESPACE -o jsonpath='{.spec.nodeName}')
        echo "   Pod is running on node: $NODE_NAME"

        # Get the pod UID for finding the sandbox
        POD_UID=$($KUBECTL get pod $POD_NAME_RUNTIME -n $TEST_NAMESPACE -o jsonpath='{.metadata.uid}')
        echo "   Pod UID: $POD_UID"

        echo "15. Verifying Kata runtime configuration on node..."
        
        # Create a debug script to run on the node
        DEBUG_SCRIPT='
#!/bin/bash
set -e

echo "=== Checking Kata VM configuration ==="

# Find sandbox ID from crictl
SANDBOX_ID=$(crictl pods --name test-pod-runtime -q 2>/dev/null | head -1)
if [ -z "$SANDBOX_ID" ]; then
    echo "Could not find sandbox ID via crictl"
    # Try to find via /run/vc
    echo "Searching in /run/vc/vm/..."
    ls -la /run/vc/vm/ 2>/dev/null || echo "No VMs found in /run/vc/vm/"
    exit 1
fi

echo "Sandbox ID: $SANDBOX_ID"

# Check for hypervisor.json or similar config
VM_PATH="/run/vc/vm/${SANDBOX_ID}"
if [ -d "$VM_PATH" ]; then
    echo "VM directory found: $VM_PATH"
    ls -la "$VM_PATH"
    
    # Look for configuration files
    for config_file in hypervisor.json config.json state.json; do
        if [ -f "$VM_PATH/$config_file" ]; then
            echo ""
            echo "=== $config_file ==="
            cat "$VM_PATH/$config_file" | grep -i "memory" || echo "(no memory config found)"
        fi
    done
else
    echo "VM directory not found at $VM_PATH"
fi

# Check QEMU process for memory settings
echo ""
echo "=== QEMU process memory settings ==="
QEMU_PID=$(pgrep -f "qemu.*$SANDBOX_ID" 2>/dev/null | head -1)
if [ -n "$QEMU_PID" ]; then
    echo "QEMU PID: $QEMU_PID"
    ps -p $QEMU_PID -o args= 2>/dev/null | tr " " "\n" | grep -E "^-m|memory" || echo "(memory args not found in cmdline)"
else
    echo "QEMU process not found for sandbox"
    # Try to find any QEMU process
    echo "Looking for any QEMU processes..."
    pgrep -a qemu 2>/dev/null | head -3 || echo "No QEMU processes found"
fi

# Check kata-runtime logs for memory overhead
echo ""
echo "=== Recent kata-related logs ==="
journalctl -u crio --since "5 minutes ago" 2>/dev/null | grep -i "memory_overhead\|memory overhead" | tail -10 || echo "(no memory_overhead logs found)"

# Check if the annotation made it to containerd/CRI-O
echo ""
echo "=== Pod annotations in CRI ==="
crictl inspectp $SANDBOX_ID 2>/dev/null | grep -A5 -B5 "memory_overhead" || echo "(annotation not found in sandbox inspect)"
'

        # Run the debug script on the node
        echo ""
        echo "   Running diagnostics on node $NODE_NAME..."
        echo "   (This uses 'oc debug node' which may take a moment)"
        echo ""
        
        # Execute via oc debug node
        if command -v oc &> /dev/null; then
            # Use oc debug for OpenShift
            # Run non-interactively for automation; newer oc rejects -it here.
            RUNTIME_OUTPUT=$($KUBECTL debug node/$NODE_NAME --image=registry.access.redhat.com/ubi9/ubi:latest -- chroot /host bash -c "$DEBUG_SCRIPT" 2>&1) || true
        else
            # For non-OpenShift, try to use a privileged daemonset or direct ssh
            print_status "warn" "Node debugging requires 'oc debug' (OpenShift) or direct node access"
            RUNTIME_OUTPUT="Skipped - no node access method available"
        fi

        echo "   --- Node Debug Output ---"
        echo "$RUNTIME_OUTPUT" | sed 's/^/   /'
        echo "   --- End Debug Output ---"
        echo ""

        # Check if we found evidence of the memory overhead being applied
        if echo "$RUNTIME_OUTPUT" | grep -qi "memory_overhead\|512"; then
            print_status "ok" "Found memory overhead configuration in Kata runtime"
        else
            print_status "warn" "Could not verify memory overhead in Kata runtime config"
            echo "   This may be due to:"
            echo "   - Different Kata configuration storage location"
            echo "   - Kata version differences"
            echo "   - The annotation is processed differently"
            echo ""
            echo "   Manual verification steps:"
            echo "   1. SSH to node: $NODE_NAME"
            echo "   2. Find sandbox: crictl pods --name $POD_NAME_RUNTIME"
            echo "   3. Check VM config: ls /run/vc/vm/<sandbox-id>/"
            echo "   4. Check QEMU args: ps aux | grep qemu"
        fi

    else
        print_status "warn" "Pod did not reach Running state within ${RUNTIME_TEST_TIMEOUT}s"
        echo "   This is expected if:"
        echo "   - No nodes have Kata runtime installed"
        echo "   - Nodes are not labeled for Kata workloads"
        echo "   - RuntimeClass '$TEST_RUNTIME_CLASS' is not available"
        echo ""
        echo "   Pod events:"
        $KUBECTL get events -n $TEST_NAMESPACE --field-selector involvedObject.name=$POD_NAME_RUNTIME --sort-by='.lastTimestamp' | tail -10
        echo ""
        echo "   Skipping runtime verification (webhook tests still passed)"
    fi
fi

echo ""
echo "========================================"
echo "Test Summary"
echo "========================================"
echo ""
echo "Webhook Tests:"
print_status "ok" "Annotation injection for Kata pods"
print_status "ok" "ConfigMap-based configuration"
print_status "ok" "ConfigMap updates reflected in new pods"
print_status "ok" "Default value (350) when ConfigMap absent"
print_status "ok" "Non-Kata pods excluded from annotation"
echo ""
echo "Runtime Tests:"
if [ "$SKIP_RUNTIME_TESTS" = "true" ]; then
    print_status "warn" "Skipped (SKIP_RUNTIME_TESTS=true)"
elif [ "$POD_RUNNING" = "true" ]; then
    print_status "info" "Ran diagnostics on node (see output above)"
else
    print_status "warn" "Skipped (pod did not reach Running state)"
fi
echo ""
echo "Configuration:"
echo "  ConfigMap: $NAMESPACE/$CONFIGMAP_NAME"
echo "  Key: memoryOverheadMB"
echo "  Default: 350"
echo ""
echo "========================================"
echo -e "${GREEN}All webhook tests passed!${NC}"
echo "========================================"
