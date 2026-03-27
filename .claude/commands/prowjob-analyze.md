---
name: prowjob-analyze
description: Analyze OpenShift Prow job results to determine status, extract metadata, and identify failures
allowed-tools:
  - Bash(python3 scripts/prowjob-analyzer/dig.py:*)
  - Bash(python3 scripts/prowjob-analyzer/dig_failed_tests_report.py:*)
---

Analyze a Prow job to determine its status and provide detailed failure analysis.

**Workflow:**
1. Run the main analyzer to get overall job status and metadata
2. Based on which step failed, decide if further analysis is needed

Execute the following steps:

**Step 1: Run main analyzer**
```bash
python3 scripts/prowjob-analyzer/dig.py --no-wait "$@"
```

**Step 2: Analyze the output**
- Review the analysis report from Step 1
- Check the "Failure Analysis" section to see which step(s) failed
- Check if there are "Failed Tests" listed

**Step 3: Determine next action based on failed step**

**Case A: Tests failed** (Failed Step is `openshift-extended-test` AND Failed Tests are listed)
- This means the job ran through infrastructure setup and failed during test execution
- **IMPORTANT**: Check the failure count:
  - If **>90% of tests failed** (e.g., 10+ tests failed):
    - Each test in the "Failed Tests" section shows metadata including "Execution Order: N"
    - Find the test with "Execution Order: 1" - this is the FIRST test that executed
    - Use ONLY that test name for detailed analysis:
    ```bash
    python3 scripts/prowjob-analyzer/dig_failed_tests_report.py <PROW_JOB_URL> "<TEST_NAME_WITH_EXECUTION_ORDER_1>"
    ```
    This is because when all/most tests fail, it indicates a setup failure. The first test contains the root cause, and subsequent tests show cascading errors.

  - If **only a few tests failed** (e.g., <10 tests): Run analysis on all failing tests
    ```bash
    python3 scripts/prowjob-analyzer/dig_failed_tests_report.py <PROW_JOB_URL> <TEST_NAME_1> <TEST_NAME_2> ...
    ```
    Use the exact test names from the "Failed Tests" section.

**Case B: Infrastructure/setup step failed** (Failed Step is NOT `openshift-extended-test`)
- Examples: `ipi-install-install`, `sandboxed-containers-operator-peerpods-param-cm`, etc.
- This means tests never ran - job failed before reaching the test step
- Do NOT run dig_failed_tests_report.py
- Provide summary explaining that the job failed at infrastructure/setup stage
- Point user to the specific step's artifacts for investigation

**Case C: Multiple steps failed**
- If `openshift-extended-test` is among the failed steps AND has failing tests, run test analysis
- Otherwise, treat as Case B

---

## About

Comprehensive Prow job analysis with two-level investigation:

**Level 1: Overall Analysis** (dig.py)
- Extracts job metadata (provider, OCP version, Kata RPM, catalog, etc.)
- Determines overall job status (pass/fail/timeout)
- Identifies which step(s) failed
- Lists failing tests if tests ran and failed
- Provides links to all artifacts

**Level 2: Detailed Test Debugging** (dig_failed_tests_report.py - only when tests failed)
- Only runs if `openshift-extended-test` step failed with failing tests
- Extracts full test logs and failure summaries from build-log.txt
- Reports test metadata: elapsed time, category, author, priority
- Provides complete log context for each failing test
- Supports filtering specific tests by name or ID

**Important:** If the job failed at an earlier step (like `ipi-install-install`),
tests never ran and test analysis is not applicable. The analyzer will correctly
identify which infrastructure/setup step failed.

---

## OSC Test Design and Execution

### Test Framework and Organization

**Framework**: Tests use the Ginkgo v2 BDD (Behavior-Driven Development) framework with Gomega assertions, integrated with Kubernetes e2e test infrastructure.

**Test Suite Location**: All OSC tests are grouped under the `[sig-kata]` descriptor in the test suite. Test code resides in `openshift-tests-private/test/extended/kata/` with test templates in `test/extended/testdata/kata/`.

**Test Naming Convention**: Each test follows a structured naming pattern:
```
Author:<developer>-<Priority>-<CaseID>-<Description> [Tags]
```

Examples:
- `Author:vvoronko-High-C00349-deploy peerpod with non-existing image annotation [Serial]`

Where:
- **Author**: Test developer's username
- **Priority**: High/Medium/Low
- **CaseID**: Unique test case identifier (e.g., C00077, C00349)
- **Description**: Human-readable test purpose
- **Tags**: Execution characteristics (Serial, Slow, Disruptive)

### Workload Types and Runtime Classes

OSC supports three distinct workload types, controlled by configuration:

1. **kata**: Traditional Kata Containers (VM-isolated pods on cluster nodes)
   - Runtime class: `kata`
   - Tests tagged with standard kata behavior

2. **peer-pods**: Cloud-provider remote VMs (PeerPods architecture)
   - Runtime class: `kata-remote`
   - Tests verify cloud-specific features (AWS, Azure, GCP)
   - Includes instance type, vCPU/memory annotations, custom images

3. **coco**: Confidential Containers with attestation
   - Runtime class: `kata-remote`
   - Tests verify Trustee integration and attestation
   - Requires KBS (Key Broker Service) configuration

### Test Execution Flow

**Setup Phase** (BeforeEach):
1. Check for test configuration in `osc-config` ConfigMap (namespace: default)
2. Subscribe to OSC operator from catalog source
3. Create KataConfig custom resource
4. Wait for operator installation and runtime installation on nodes
5. **Node reboots occur** during KataConfig creation/deletion (can take >20 minutes per node)
6. If configured to install Kata RPM: Install specific Kata runtime version on nodes
7. **Workload-specific setup**:
   - If peer-pods enabled: Create `peer-pods-cm` ConfigMap with cloud provider-specific configuration
   - If coco workload: Update `peer-pods-cm` with confidential instance type and set INITDATA property containing Trustee access information and other attestation configuration

**Critical Setup Behavior**:
- Every test runs BeforeEach which includes the setup phase
- If the first test's setup succeeds, it marks setup as complete and subsequent tests skip the setup
- If the first test's setup fails, ALL subsequent tests will attempt the full setup phase again
- Since setup includes node reboots (>20 minutes), repeated setup attempts across all tests can cause:
  - Extremely long job execution times
  - Job timeouts (tests trying setup instead of running actual test logic)
- This explains why infrastructure failures can cause the entire job to timeout

**CRITICAL: Root Cause Analysis Strategy**:
When ALL or MOST tests fail (>90%), this indicates a common root cause in the environment setup:

1. **Focus on the FIRST test's logs ONLY**:
   - The first test that runs will encounter the initial/real error during setup
   - Subsequent tests will show cascading/secondary errors because setup already failed
   - The FIRST test's logs contain the true root cause

2. **RPM Installation Failures - Common Pattern**:
   - **Real error** (appears in FIRST test): `error: Failed dependencies` - This is the ROOT CAUSE
   - **Cascading error** (appears in MANY subsequent tests): `error: Deployment is already in unlocked state: hotfix`
   - The cascading error appears frequently because every subsequent test tries to install the RPM and fails since the system is in a bad state
   - DO NOT be misled by error frequency - the FIRST occurrence is what matters

3. **Analysis Approach**:
   - If 90%+ of tests failed, immediately look at the FIRST test's logs
   - Identify what failed during setup (RPM install, KataConfig creation, operator installation, etc.)
   - Ignore errors that appear in most/all tests - these are cascading failures
   - The error that appears EARLIEST (in the first test) is the root cause

4. **Example Scenario**:
   ```
   Test 1: Fails with "error: Failed dependencies: packageX is needed by kata"
   Test 2: Fails with "error: Deployment is already in unlocked state: hotfix"
   Test 3: Fails with "error: Deployment is already in unlocked state: hotfix"
   ... (all remaining tests show the same "unlocked state" error)

   ROOT CAUSE: Failed dependencies in Test 1
   CASCADING ERRORS: "Deployment is already in unlocked state" in all subsequent tests
   ```

**Test Execution**:
- Tests run sequentially due to `[Serial]` tag (shared cluster state)
- Tests automatically skip when workload type or platform doesn't match test requirements
- Each test creates isolated namespace via `oc.SetupProject()`
- Workloads deployed from YAML templates with variable substitution
- Tests verify using `oc` CLI commands and Kubernetes client-go
- Cleanup in deferred functions (defer deleteKataResource)

**Configuration Overrides**:
Tests use hardcoded defaults that can be overridden via `osc-config` ConfigMap:
- Operator channel (stable)
- Catalog source (redhat-operators or custom)
- Runtime class name (kata, kata-remote)
- Enable peer pods (true/false)
- Workload type to test (kata, peer-pods, coco)
****- Install Kata RPM (true/false)
- Enable GPU to include Nvidia GPU drivers in podvm image and run GPU tests
- Set initdata for the OSC operator
- Must-gather image
- Import podvm image from URL instead of building it from scratch
- Point OSC operator to an existing external Trustee instance

### Test Categories

**Operator Lifecycle**:
- Installation and subscription verification (C00113, C00193)
- Version validation (C00077)
- Multiple KataConfig prevention (C00156)
- Upgrade scenarios (manual/automated)
- Deletion with running workloads (C00317)

**Workload Deployment**:
- Basic pod/deployment creation and deletion (C00102)
- RuntimeClass verification
- Security context validation
- Resource requests and limits (C00350, C00355)

**PeerPods-Specific**:
- Instance type annotations (C00099)
- vCPU and memory annotations (C00131)
- Custom tags (C00320)
- Image annotations - existing (C00347), empty (C00348), non-existing (C00349)
- PodVM image verification (C00095)

**Scaling and Operations**:
- Deployment scale-up (C00122)
- Deployment scale-down (C00123)
- Service exposure (C00100)
- Cluster limits (C00133)

**Monitoring and Metrics**:
- Namespace monitoring labels (C00112)
- Pod metrics validation (C00142)
- `oc adm top pod` functionality (C00143)
- Monitor operator deletion (C00160)

**Special Features**:
- FIPS validation (C00094)
- GPU workloads on PeerPods (C00210)
- Confidential containers with cosigned images (C00316)
- SELinux-enabled pods (422081)
- EmptyDir with sizeLimit (C00355)

### Test Execution Tags

**[Serial]**: All tests run serially (no parallel execution)
- Reason: Shared cluster state (KataConfig, operator installation)

**[Slow]**: Tests that exceed normal timeout
- KataConfig creation/deletion (node reboots)
- Typically require `--timeout 120m` or longer

**[Disruptive]**: Tests that affect cluster state
- KataConfig deletion (reboots all worker nodes)
- Operator uninstall
- Upgrade operations

### Common Failure Patterns

**Infrastructure Failures** (tests don't run):
- Cluster provisioning issues
- Operator installation failures
- KataConfig creation timeout (node reboot issues)
- Catalog source not available
- Pod scheduling failures (node eligibility, taints)
- **Setup phase failures causing all tests to retry setup (leads to timeouts)**

**Common Feature Failures**:
- Image pull errors
- Timeout waiting for pod ready
- Assertion failures (version mismatch, incorrect behavior)
- Resource constraints (memory, CPU limits)

**PeerPods-Specific Failures**:
- Cloud provider API failures
- PodVM image creation failures
- Instance type not available in region
- Network connectivity to cloud metadata service
- Annotation parsing errors

**CoCo-Specific Failures**:
- Trustee connectivity issues
- Attestation failures
- INITDATA configuration errors
- KBS (Key Broker Service) unavailable

---

**Usage:**
```
/prowjob-analyze <PROW_JOB_URL>
```

**Example:**
```
/prowjob-analyze https://prow.ci.openshift.org/view/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate-aws-ipi-peerpods/1987995564184178688
```

**Output:**
- Comprehensive analysis report in markdown format
- Identifies which step(s) failed
- For test failures: Detailed debugging information for each failing test
- For infrastructure failures: Guidance on which step failed and where to investigate
- Links to all relevant artifacts

**Options:**
- `--json`: Output machine-readable JSON format (for both scripts)
- `--verbose`: Show detailed progress information
