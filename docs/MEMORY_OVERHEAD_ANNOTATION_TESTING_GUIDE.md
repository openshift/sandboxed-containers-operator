# Testing Guide for Memory Overhead Annotation Feature

This guide provides comprehensive testing instructions for the memory overhead annotation feature implementation.

## Overview

The memory overhead annotation feature allows the Kata runtime to correctly handle memory hotplugging by passing the known overhead (from the RuntimeClass) via a pod annotation: `io.katacontainers.config.hypervisor.memory_overhead`.

## Test Scripts

Two test scripts are provided in `scripts/webhook-tests/`:

| Script | Purpose | Requirements |
|--------|---------|--------------|
| `run_pod_overhead_mutator.sh` | Unit tests, benchmarks, coverage, linting | Go toolchain only |
| `test_memory_overhead.sh` | End-to-end integration tests | Running OpenShift cluster with operator |

### Quick Start

```bash
# Run all unit tests (no cluster required)
./scripts/webhook-tests/run_pod_overhead_mutator.sh

# Run end-to-end tests (requires cluster with operator deployed)
./scripts/webhook-tests/test_memory_overhead.sh
```

## Unit Tests

The unit test script (`run_pod_overhead_mutator.sh`) runs the following test suites:

### What It Tests

1. **Pod Mutator Unit Tests** - Core webhook logic
2. **KataConfig Webhook Tests** - Validation logic
3. **Integration Tests** - All memory overhead related tests
4. **Benchmark Tests** - Performance measurements
5. **Coverage Analysis** - Code coverage reports
6. **Linting Checks** - Code quality (if golangci-lint available)
7. **Race Detection** - Concurrency safety

### Pod Mutator Tests (`controllers/pod_mutator_test.go`)

**Test Functions:**
- `TestPodMutator_getMemoryOverheadFromKataConfig` - Tests configuration reading and runtime class detection
- `TestPodMutator_injectMemoryOverheadAnnotation` - Tests annotation injection
- `TestPodMutator_Default` - Tests the webhook defaulter method

**Test Scenarios:**
- ✅ Pod with `kata` runtime class gets annotation
- ✅ Pod with `kata-remote` runtime class gets annotation
- ✅ Pod without runtime class doesn't get annotation
- ✅ Pod with non-Kata runtime class doesn't get annotation
- ✅ Pod with Kata runtime but no matching KataConfig returns 0
- ✅ Pod with Kata runtime and KataConfig without MemoryOverheadMB uses default (350)
- ✅ Annotation values match KataConfig settings
- ✅ Multiple KataConfigs - first matching one wins
- ✅ KataConfig with empty RuntimeClasses handled correctly
- ✅ Error handling for invalid configurations

### KataConfig Webhook Tests (`api/v1/kataconfig_webhook_test.go`)

**Test Functions:**
- `TestKataConfig_ValidateCreate` - Tests creation validation
- `TestKataConfig_ValidateUpdate` - Tests update validation
- `TestKataConfig_ValidateDelete` - Tests deletion validation
- `TestKataConfig_MemoryOverheadMB_EdgeCases` - Tests boundary conditions

**Test Scenarios:**
- ✅ Valid MemoryOverheadMB values (1-2048) pass validation
- ✅ Invalid MemoryOverheadMB values (< 1, > 2048) fail validation
- ✅ Nil MemoryOverheadMB is valid (uses default)
- ✅ Update operations validate correctly
- ✅ Delete operations are always valid
- ✅ Edge cases and boundary conditions handled

### Benchmark Tests

- `BenchmarkPodMutator_Default` - Measures webhook defaulter performance
- `BenchmarkKataConfig_ValidateCreate` - Measures validation performance
- `BenchmarkKataConfig_ValidateUpdate` - Measures update validation performance

## End-to-End Tests

The E2E test script (`test_memory_overhead.sh`) validates the feature in a real cluster environment.

### Prerequisites

- Running OpenShift/Kubernetes cluster
- Operator deployed in `openshift-sandboxed-containers-operator` namespace
- `kubectl` configured with cluster access

### What It Tests

1. **Operator Status** - Verifies operator is running
2. **Webhook Registration** - Checks if mutating webhook is registered
3. **KataConfig Creation** - Creates KataConfig with custom memory overhead
4. **Annotation Injection** - Verifies pods get the correct annotation
5. **Dynamic Updates** - Tests that KataConfig changes affect new pods
6. **Non-Kata Pods** - Verifies non-Kata pods don't get annotated

### Test Flow

```
1. Check operator is running
2. Check webhook is registered
3. Create KataConfig (memoryOverheadMB: 512)
4. Create pod with kata runtime class
5. Verify annotation = "512"
6. Update KataConfig (memoryOverheadMB: 256)
7. Create another pod
8. Verify annotation = "256"
9. Create pod without kata runtime class
10. Verify no annotation present
11. Cleanup
```

## Running Tests Manually

### Individual Unit Test Categories

```bash
# Run only getMemoryOverheadFromKataConfig tests
go test -v ./controllers -run TestPodMutator_getMemoryOverheadFromKataConfig

# Run only Default method tests
go test -v ./controllers -run TestPodMutator_Default

# Run only annotation injection tests
go test -v ./controllers -run TestPodMutator_injectMemoryOverheadAnnotation

# Run only KataConfig validation tests
go test -v ./api/v1 -run TestKataConfig

# Run benchmark tests
go test -v ./controllers -bench=BenchmarkPodMutator

# Run with coverage
go test -v ./controllers -run TestPodMutator -coverprofile=pod_mutator_coverage.out
go tool cover -html=pod_mutator_coverage.out
```

### Test with Different Options

```bash
# Run tests with race detection
go test -v ./controllers -run TestPodMutator -race

# Run tests with timeout
go test -v ./controllers -run TestPodMutator -timeout 30s

# Run a specific sub-test
go test -v ./controllers -run "TestPodMutator_Default/Pod_with_Kata_runtime_class"
```

## Test Data

### Sample Pod Configurations

```yaml
# Kata pod (should get annotation)
apiVersion: v1
kind: Pod
metadata:
  name: test-pod
spec:
  runtimeClassName: kata
  containers:
  - name: test-container
    image: nginx:latest

# Kata-remote pod (should get annotation)
apiVersion: v1
kind: Pod
metadata:
  name: test-pod-remote
spec:
  runtimeClassName: kata-remote
  containers:
  - name: test-container
    image: nginx:latest

# Non-Kata pod (should NOT get annotation)
apiVersion: v1
kind: Pod
metadata:
  name: test-pod-runc
spec:
  runtimeClassName: runc
  containers:
  - name: test-container
    image: nginx:latest
```

### Sample KataConfig Configurations

```yaml
# KataConfig with custom memory overhead
apiVersion: kataconfiguration.openshift.io/v1
kind: KataConfig
metadata:
  name: example-kataconfig
spec:
  memoryOverheadMB: 512
status:
  runtimeClasses:
  - kata
  - kata-remote

# KataConfig without memory overhead (uses default 350)
apiVersion: kataconfiguration.openshift.io/v1
kind: KataConfig
metadata:
  name: example-kataconfig-default
spec:
  checkNodeEligibility: true
  logLevel: "info"
status:
  runtimeClasses:
  - kata
```

**Note:** The webhook matches pods to KataConfigs by checking if the pod's `runtimeClassName` is present in `KataConfig.Status.RuntimeClasses`. This status field is populated by the controller when runtime classes are created.

## Expected Results

### Unit Test Output (run_pod_overhead_mutator.sh)

```
Running Pod Overhead Mutator Tests
==================================
1. Running pod mutator tests...
-------------------------------
=== RUN   TestPodMutator_getMemoryOverheadFromKataConfig
...
--- PASS: TestPodMutator_getMemoryOverheadFromKataConfig (0.16s)
=== RUN   TestPodMutator_injectMemoryOverheadAnnotation
...
--- PASS: TestPodMutator_injectMemoryOverheadAnnotation (0.00s)
=== RUN   TestPodMutator_Default
...
--- PASS: TestPodMutator_Default (0.02s)
PASS
✓ Pod mutator tests passed

2. Running KataConfig webhook tests...
--------------------------------------
...
✓ KataConfig webhook tests passed

...

🎉 All tests completed successfully!

Test Summary:
- Pod mutator unit tests: ✓
- KataConfig webhook tests: ✓
- Memory overhead integration tests: ✓
- Benchmark tests: ✓
- Coverage analysis: ✓
- Linting checks: ✓
- Race detection: ✓

The feature appears to work as expected.
```

### E2E Test Output (test_memory_overhead.sh)

```
Testing Memory Overhead Annotation Feature
==========================================
1. Checking if operator is running...
✓ Operator is running
2. Checking if webhook is registered...
✓ Webhook is registered
3. Creating test KataConfig with custom memory overhead...
✓ KataConfig created
4. Creating test pod with Kata runtime class...
5. Waiting for pod to be created...
✓ Pod created
6. Checking if memory overhead annotation was injected...
✓ Memory overhead annotation found: 512
✓ Annotation value is correct: 512
7. Testing with different memory overhead value...
✓ Second pod has correct annotation value: 256
8. Testing with non-Kata runtime class...
✓ Non-Kata pod correctly has no memory overhead annotation

🎉 All tests passed! Memory overhead annotation feature is working correctly.

Summary:
- Webhook is properly registered
- Memory overhead annotation is injected for Kata pods
- Annotation value matches KataConfig setting
- Non-Kata pods do not get the annotation
- Dynamic configuration changes work correctly
```

## Key Implementation Details

### How the Webhook Works

1. **Pod Admission**: When a pod is created/updated, the mutating webhook intercepts it
2. **Runtime Class Check**: The webhook calls `getMemoryOverheadFromKataConfig()` which:
   - Checks if `pod.Spec.RuntimeClassName` is set
   - Lists all KataConfigs in the cluster
   - Checks if the runtime class name matches any entry in `KataConfig.Status.RuntimeClasses`
   - Returns the `MemoryOverheadMB` value (or default 350) if matched, or 0 if not
3. **Annotation Injection**: If overhead > 0, adds annotation `io.katacontainers.config.hypervisor.memory_overhead`
4. **Skip for Non-Kata**: If overhead == 0, the webhook returns without modifying the pod

### Default Values

- `KataMemoryOverheadDefault = 350` (MiB)
- `KataMemoryOverheadAnnotation = "io.katacontainers.config.hypervisor.memory_overhead"`

## Troubleshooting

### Common Test Issues

1. **Import Errors**
   ```bash
   # Ensure all dependencies are available
   go mod tidy
   go mod download
   ```

2. **Test Timeouts**
   ```bash
   # Increase timeout for slow tests
   go test -v ./controllers -run TestPodMutator -timeout 60s
   ```

3. **Race Conditions**
   ```bash
   # Run with race detection
   go test -v ./controllers -run TestPodMutator -race
   ```

4. **E2E Test Failures**
   ```bash
   # Check operator status
   kubectl get deployment controller-manager -n openshift-sandboxed-containers-operator
   
   # Check webhook registration
   kubectl get mutatingwebhookconfigurations
   
   # Check KataConfig status
   kubectl get kataconfig -o yaml
   ```

### Test Environment Setup

```bash
# Ensure Go version compatibility
go version  # Should be 1.21+

# Set environment variables if needed
export GO111MODULE=on

# Install test dependencies
go mod tidy
go mod download

# For E2E tests: ensure kubectl access
kubectl cluster-info
```

## Test Maintenance

### Adding New Tests

1. **Unit Tests**: Add test cases to existing test functions in `pod_mutator_test.go`
2. **E2E Tests**: Add scenarios to `scripts/webhook-tests/test_memory_overhead.sh`
3. **Benchmark Tests**: Add to the benchmark section in `pod_mutator_test.go`
4. **Documentation**: Update this guide with new test scenarios

### Test Best Practices

1. **Test Naming**: Use descriptive names that explain the scenario
2. **Test Data**: Use realistic test data that matches production scenarios
3. **Status.RuntimeClasses**: Always set this in test KataConfigs to simulate real controller behavior
4. **Error Testing**: Always test both success and failure cases
5. **Edge Cases**: Test boundary conditions (nil values, empty lists, etc.)
6. **Performance**: Include benchmark tests for performance-critical code

## Conclusion

The test suite ensures that the memory overhead annotation feature works correctly:

| Test Type | Coverage |
|-----------|----------|
| Unit Tests | Core webhook logic, helper methods |
| Validation Tests | KataConfig field validation |
| E2E Tests | Full integration with Kubernetes |
| Benchmark Tests | Performance characteristics |
| Race Detection | Concurrency safety |

Run both scripts to ensure complete test coverage:
```bash
# Unit tests (development)
./scripts/webhook-tests/run_pod_overhead_mutator.sh

# E2E tests (before release)
./scripts/webhook-tests/test_memory_overhead.sh
```
