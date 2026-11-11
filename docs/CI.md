# Continuous Integration (CI)

## OpenShift CI (Prow)

OpenShift CI runs jobs on cloud infrastructure (Azure, AWS) using the Prow CI system.

**Access:** https://prow.ci.openshift.org/

**Source:** https://github.com/openshift/release/tree/main/ci-operator/config/openshift/sandboxed-containers-operator

**Step registry:** https://github.com/openshift/release/tree/main/ci-operator/step-registry/sandboxed-containers-operator

### Periodic jobs

Jobs that run on a regular schedule (daily Mon-Fri or weekly on Sunday).

**Variant terminology:**
- **local**: Kata VMs run locally on the cluster node (standard kata runtime)
- **Peer Pods**: Kata VMs run remotely in cloud provider infrastructure (kata-remote runtime)

| Job results | Workload type | Variant | OCP | Cloud Provider | Comments |
|--|--|--|--|--|--|
| [candidate420-azure-ipi-kata](https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate420-azure-ipi-kata) | kata | local | 4.20 | azure | |
| [candidate420-azure-ipi-kata-disconnected](https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate420-azure-ipi-kata-disconnected) | kata | local | 4.20 | azure | Disconnected network (proxy+mirror) |
| [candidate420-azure-ipi-peerpods](https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate420-azure-ipi-peerpods) | peer pods | Peer Pods | 4.20 | azure | |
| [candidate420-azure-ipi-coco](https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate420-azure-ipi-coco) | CoCo | Peer Pods | 4.20 | azure | |
| [candidate420-aws-ipi-peerpods](https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate420-aws-ipi-peerpods) | peer pods | Peer Pods | 4.20 | aws | |
| [candidate420-aro-peerpods](https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate420-aro-peerpods) | Peer Pods | Peer Pods | 4.20 | azure | ARO |
| [candidate420-aro-coco](https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate420-aro-coco) | CoCo | Peer Pods | 4.20 | azure | ARO |
| [candidate421-azure-ipi-kata](https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate421-azure-ipi-kata) | kata | local | 4.21 | azure | |
| [candidate421-azure-ipi-kata-disconnected](https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate421-azure-ipi-kata-disconnected) | kata | local | 4.21 | azure | Disconnected network (proxy+mirror) |
| [candidate421-azure-ipi-peerpods](https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate421-azure-ipi-peerpods) | peer pods | Peer Pods | 4.21 | azure | |
| [candidate421-azure-ipi-coco](https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate421-azure-ipi-coco) | CoCo | Peer Pods | 4.21 | azure | |
| [candidate421-aws-ipi-peerpods](https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate421-aws-ipi-peerpods) | peer pods | Peer Pods | 4.21 | aws | |
| [candidate421-aro-peerpods](https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate421-aro-peerpods) | Peer Pods | Peer Pods | 4.21 | azure | ARO |
| [candidate421-aro-coco](https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate421-aro-coco) | CoCo | Peer Pods | 4.21 | azure | ARO |
| [candidate422-azure-ipi-kata](https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate422-azure-ipi-kata) | kata | local | 4.22 | azure | |
| [candidate422-azure-ipi-kata-disconnected](https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate422-azure-ipi-kata-disconnected) | kata | local | 4.22 | azure | Disconnected network (proxy+mirror) |
| [candidate422-azure-ipi-peerpods](https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate422-azure-ipi-peerpods) | peer pods | Peer Pods | 4.22 | azure | |
| [candidate422-azure-ipi-peerpods-sts](https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate422-azure-ipi-peerpods-sts) | peer pods | Peer Pods | 4.22 | azure | Configured with workload identity. Runs weekly on Sunday |
| [candidate422-azure-ipi-coco](https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate422-azure-ipi-coco) | CoCo | Peer Pods | 4.22 | azure | |
| [candidate422-aws-ipi-peerpods](https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate422-aws-ipi-peerpods) | peer pods | Peer Pods | 4.22 | aws | |
| [candidate422-aws-ipi-peerpods-sts](https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate422-aws-ipi-peerpods-sts) | peer pods | Peer Pods | 4.22 | aws | Configured with Security Token Service (STS). Runs weekly on Sunday |
| [candidate500-azure-ipi-kata](https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate500-azure-ipi-kata) | kata | local | 5.0 | azure | |
| [candidate500-azure-ipi-kata-disconnected](https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate500-azure-ipi-kata-disconnected) | kata | local | 5.0 | azure | Disconnected network (proxy+mirror) |
| [candidate500-azure-ipi-peerpods](https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate500-azure-ipi-peerpods) | peer pods | Peer Pods | 5.0 | azure | |
| [candidate500-azure-ipi-peerpods-sts](https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate500-azure-ipi-peerpods-sts) | peer pods | Peer Pods | 5.0 | azure | Configured with workload identity. Runs weekly on Sunday |
| [candidate500-azure-ipi-coco](https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate500-azure-ipi-coco) | CoCo | Peer Pods | 5.0 | azure | |
| [candidate500-aws-ipi-peerpods](https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate500-aws-ipi-peerpods) | peer pods | Peer Pods | 5.0 | aws | |
| [candidate500-aws-ipi-peerpods-sts](https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/periodic-ci-openshift-sandboxed-containers-operator-devel-downstream-candidate500-aws-ipi-peerpods-sts) | peer pods | Peer Pods | 5.0 | aws | Configured with Security Token Service (STS). Runs weekly on Sunday |

**Configuration:**
- **Pre-GA testing** (candidate420, candidate421, candidate422, candidate500):
  - OSC catalog: `quay.io/redhat-user-workloads/ose-osc-tenant/osc-test-fbc:latest`
  - Catalog source name: `brew-catalog`
  - Trustee catalog (CoCo jobs): `quay.io/redhat-user-workloads/ose-osc-tenant/trustee-test-fbc:latest`
  - Kata RPM versions (most jobs use `INSTALL_KATA_RPM=true`):
    - candidate420: `4.2.0-2.rhaos4.20.el9`
    - candidate421: `4.2.0-2.rhaos4.20.el9`
    - candidate422: `4.2.0-2.rhaos4.22.el9`
    - candidate500: `4.2.0-2.rhaos5.0.el10`
    - **Note:** Disconnected and STS jobs use `INSTALL_KATA_RPM=false` (version from operator)
- **Test sources:** Test suites from this repository's `test/e2e` directory (default: `devel` branch)
- **Test images:** 
  - CoCo signed image: `ghcr.io/confidential-containers/test-container-image-rs:cosign-signed`
  - Must-gather: `registry.redhat.io/openshift-sandboxed-containers/osc-must-gather-rhel9:latest`

**Test suites enabled per job:**
- **kata jobs**: `TESTS_OSC_ENABLE=true`, `TESTS_KATA_UPSTREAM_ENABLE=true`
- **peer-pods jobs**: `TESTS_OSC_ENABLE=true`, `TESTS_CAA_ENABLE=true`
- **CoCo jobs**: `TESTS_CAA_ENABLE=true`, `TESTS_CAA_PROFILE=coco`, requires Trustee installation
- **disconnected jobs**: Use `isolation-restricted-proxy-mirror` workflow with mirrored images

**Workflow types:**
- `sandboxed-containers-operator-e2e-azure`: Standard Azure IPI
- `sandboxed-containers-operator-e2e-azure-sts`: Azure with workload identity (STS)
- `sandboxed-containers-operator-e2e-aws`: Standard AWS IPI
- `sandboxed-containers-operator-e2e-aws-sts`: AWS with Security Token Service
- `sandboxed-containers-operator-e2e-azure-isolation-restricted-proxy-mirror`: Disconnected with proxy and mirror registry

### Job architecture

Jobs execute in three phases:

1. **`pre:`** — Provision OCP cluster, install OSC operator, install Kata RPM (when `INSTALL_KATA_RPM=true`), install Trustee (CoCo jobs), record metadata
2. **`test:`** — Run sanity check (one pod must start/run/delete; CoCo also verifies Trustee attestation), create gate marker file
3. **`post:`** — Execute test suites (OSC, kata-upstream, CAA), collect must-gather, deprovision cluster

Test suites run as `best_effort: true` steps — a failing suite doesn't block siblings, but the job still reports `FAILURE` if any suite fails.

**Detailed flow:** https://gitlab.cee.redhat.com/osc/osc-LLM-wiki/-/blob/main/wiki/concepts/ci-testing-flow.md

### Analyzing job results

**Artifacts location:** `https://storage.googleapis.com/test-platform-results-public/logs/<job-name>/<build-id>/`

Each step writes to `artifacts/<job-name>/<step-name>/`:
- `build-log.txt` — Step execution logs
- `junit_*.xml` — Test results (when applicable)
- `finished.json` — Step result (`SUCCESS`, `FAILURE`, `SKIPPED`)

**Recommended:** Use the `prowjob-analyze` Claude skill to parse job results, extract metadata, and identify failures:
```
/prowjob-analyze <job-url>
```

The skill handles Prow-specific quirks (self-skipped suites, test-code build failures, gate semantics) and produces structured reports with per-suite status, failure categories, and artifact links.

**Tooling:** [`scripts/prowjob-analyzer/`](../scripts/prowjob-analyzer/) — Python CLI version of the analysis logic

**Analysis guide:** https://gitlab.cee.redhat.com/osc/osc-LLM-wiki/-/blob/main/wiki/concepts/osc-job-result-analysis.md

### Additional resources

- **Job history:** https://prow.ci.openshift.org/?repo=openshift%2Fsandboxed-containers-operator&type=periodic
- **Slack notifications:** `#kata-ocp-ci-reports`

## Bare-metal jobs (Jenkins)

The bare-metal CI runs on dedicated physical hardware with TDX and SNP TEE support, managed through Jenkins.

**Access:** https://jenkins-csb-rhelvirt-kvmqe.dno.corp.redhat.com/ (requires SAML login + `rh-kata-dev` rover group)

**Source:** https://gitlab.cee.redhat.com/ldoktor/kata-bm

### Weekly scheduled jobs (OSC 1.14.0)

These jobs run weekly on dedicated bare-metal machines, testing both TDX and SNP TEE types with the full `k8s-all` integration test suite from kata-containers.

| Job View | OCP | Profile | Schedule | TEE Types | Machines | Comments |
|----------|-----|---------|----------|-----------|----------|----------|
| [kata-bm-1.14.0-4.22-default](https://jenkins-csb-rhelvirt-kvmqe.dno.corp.redhat.com/view/kata-bm-osc-1.14.0/) | 4.22 | default | Mon 00:00 | TDX, SNP | virtlab2404, virtlab26116gen | Full k8s-all test suite |
| [kata-bm-1.14.0-4.22-disconnected](https://jenkins-csb-rhelvirt-kvmqe.dno.corp.redhat.com/view/kata-bm-osc-1.14.0/) | 4.22 | disconnected | Mon 18:00 | TDX, SNP | virtlab2404, virtlab26116gen | Mirror registry + blocked internet |
| [kata-bm-1.14.0-4.22-restricted](https://jenkins-csb-rhelvirt-kvmqe.dno.corp.redhat.com/view/kata-bm-osc-1.14.0/) | 4.22 | restricted | Tue 12:00 | TDX, SNP | virtlab2404, virtlab26116gen | Squid proxy with allowlist |
| [kata-bm-1.14.0-4.21-default](https://jenkins-csb-rhelvirt-kvmqe.dno.corp.redhat.com/view/kata-bm-osc-1.14.0/) | 4.21 | default | Wed 06:00 | TDX, SNP | virtlab2404, virtlab26116gen | Full k8s-all test suite |
| [kata-bm-1.14.0-4.21-disconnected](https://jenkins-csb-rhelvirt-kvmqe.dno.corp.redhat.com/view/kata-bm-osc-1.14.0/) | 4.21 | disconnected | Thu 00:00 | TDX, SNP | virtlab2404, virtlab26116gen | Mirror registry + blocked internet |
| [kata-bm-1.14.0-4.21-restricted](https://jenkins-csb-rhelvirt-kvmqe.dno.corp.redhat.com/view/kata-bm-osc-1.14.0/) | 4.21 | restricted | Thu 18:00 | TDX, SNP | virtlab2404, virtlab26116gen | Squid proxy with allowlist |
| [kata-bm-1.14.0-4.20-default](https://jenkins-csb-rhelvirt-kvmqe.dno.corp.redhat.com/view/kata-bm-osc-1.14.0/) | 4.20 | default | Fri 12:00 | TDX, SNP | virtlab2404, virtlab26116gen | Full k8s-all test suite |
| [kata-bm-1.14.0-4.20-disconnected](https://jenkins-csb-rhelvirt-kvmqe.dno.corp.redhat.com/view/kata-bm-osc-1.14.0/) | 4.20 | disconnected | Sat 06:00 | TDX, SNP | virtlab2404, virtlab26116gen | Mirror registry + blocked internet |
| [kata-bm-1.14.0-4.20-restricted](https://jenkins-csb-rhelvirt-kvmqe.dno.corp.redhat.com/view/kata-bm-osc-1.14.0/) | 4.20 | restricted | Sun 00:00 | TDX, SNP | virtlab2404, virtlab26116gen | Squid proxy with allowlist |

**Configuration:**
- Kata RPM: `4.2.0-2.rhaos{version}.el9`
- Test source: https://gitlab.cee.redhat.com/osc/midstream/kata-containers (branch: `bm-4-2-0`)
- Catalog: `quay.io/redhat-user-workloads/ose-osc-tenant/osc-test-fbc:latest`
- Trustee: `quay.io/redhat-user-workloads/ose-osc-tenant/trustee-test-fbc:latest`
- TEE types: TDX (virtlab2404 - emerald), SNP (virtlab26116gen - genoa)
- Tests: Full `k8s-all` suite from OSC midstream kata-containers

**Network profile descriptions:**
- **default**: Normal internet access
- **disconnected**: Bastion mirror registry + blocked internet access on worker
- **restricted**: Squid proxy on bastion with domain allowlist + cluster-wide proxy configuration

### Manually-triggered jobs

**OSC 1.13.0:** Matrix of 12 job combinations (4 OCP versions × 3 profiles)
- View: https://jenkins-csb-rhelvirt-kvmqe.dno.corp.redhat.com/view/kata-bm-osc-1.13.0/
- OCP versions: 4.19, 4.20, 4.21, 4.22
- Profiles: default, disconnected, restricted
- Test source: https://github.com/wainersm/kata-containers.git (branch: `ocp-k8s-suite`)
- Tests: `k8s-confidential` suite

**Latest GA:** Ad-hoc testing with latest OSC GA release
- View: https://jenkins-csb-rhelvirt-kvmqe.dno.corp.redhat.com/view/kata-bm-osc-latest/

**Dev/Debug:** Reserve a machine for manual testing
- Job: https://jenkins-csb-rhelvirt-kvmqe.dno.corp.redhat.com/job/kata-bm-reserve/

**GPU testing:** Available for manual execution only (not in automated weekly schedule)
- Machine: virtlab2410 (TDX + NVIDIA H100)
- Test suites: `k8s-gpu` (non-CoCo), `k8s-cc-gpu` (CoCo + GPU)
- Enabled via: `ENABLE_GPU=true` parameter and selecting virtlab2410

### Job architecture

Jobs execute in sequential phases (controlled via `SKIP` parameter):

1. **Provision** — Install OCP cluster (SNO) on bare-metal hardware via jetlag (~45 min)
2. **Install** — Install TEE support, OSC operator, Trustee, KataConfig
3. **Profile** — Configure network profile (default/disconnected/restricted)
4. **Test** — Run CoCo smoke tests and kata-containers integration tests (`k8s-all` suite)
5. **Reservation** — Hold machine for debugging (on failure) or release immediately (on success)

Provisioning locks the bastion — other jobs queue until completion. Full run takes ~1.5h.

**Detailed documentation:** https://gitlab.cee.redhat.com/ldoktor/kata-bm/-/blob/main/README.md

### Analyzing job results

**Artifacts location:** Jenkins job artifacts (downloadable from job page)

**Console output patterns:**
- **Success:** `Release machine or Abort` with network profile active
- **Warning:** `:warning:` markers — machine usable but review warnings
- **Failure:** `:error:` markers + `FAILED: Machine ... is reserved for user (debugging)`

Search console for `:error:` and `:warning:` to identify specific issues.

**Bastion access:** After provisioning, SSH to bastion shows login banner with versions and cluster info. Scripts available in `~/.local/bin/`:
- `osc_versions.sh` — Show installed component versions
- `osc_simple_test.sh` — Quick CoCo/GPU smoke test
- `kata-bm-test-runner.sh` — Run kata-containers integration tests

### Manual job triggering

Access Jenkins (requires `rh-kata-dev` rover group) to run jobs manually:

**Reserve machine for debugging:**
```
Job: kata-bm-reserve
Parameters: Set SSH_PUBLIC_KEY (your ~/.ssh/id_rsa.pub content)
```

**Run tests without reprovisioning:**
```
SKIP=provision tee osc attestation trustee kataconfig verification reservation reservation_on_error reservation_on_test_fail
```

**SSH access only:**
```
SKIP=all
```

**Common parameters:**
- `SSH_PUBLIC_KEY` — Your public SSH key (required for bastion access)
- `TEE_TYPE` — `tdx`, `snp`, or `none`
- `OCP_VERSION` — Target OCP version
- `SKIP` — Space-separated phase tokens to skip

**Machine availability:** https://jenkins-csb-rhelvirt-kvmqe.dno.corp.redhat.com/lockable-resources/

### Additional resources

- **Job views by OSC version:** https://jenkins-csb-rhelvirt-kvmqe.dno.corp.redhat.com/view/kata-bm-osc-1.14.0/
- **Machine reservation:** https://jenkins-csb-rhelvirt-kvmqe.dno.corp.redhat.com/job/kata-bm-reserve/
- **Pipeline source:** https://gitlab.cee.redhat.com/ldoktor/kata-bm

## Test suites

### OpenShift CI (Prow) test suites

The Prow jobs run test suites from [`test/e2e`](../test/e2e/README.md) as part
of the job itself, driven by steps under
`ci-operator/step-registry/sandboxed-containers-operator/` in the
`openshift/release` repository:

- The **sanity suite** (`test/e2e/sanity`) runs first, in the `test` phase,
  enabled by default. A failure there stops the test phase, so the suites
  below never run against a broken install.
- The **OSC** (Ginkgo, `test/e2e`), **upstream kata-containers**
  (`run_upstream_tests.sh`), and **CAA** (`run_caa_tests.sh`) suites run
  afterward, in the `post` phase, each as an independent `best_effort` step:
  one suite failing never blocks another or the cluster teardown. All three
  are skip-by-default and enabled per job via a `TESTS_<SUITE>_ENABLE`
  variable.

Each step fetches `test/e2e` from this repository (`devel` by default,
overridable per job) and runs the same scripts documented in
[`test/e2e/README.md`](../test/e2e/README.md).

### Bare-metal CI (Jenkins) test suite

The Jenkins bare-metal jobs run kata-containers Kubernetes integration tests
from Red Hat forks/midstream:

- **OSC 1.14.0 weekly jobs**: Full `k8s-all` suite from
  https://gitlab.cee.redhat.com/osc/midstream/kata-containers (branch: `bm-4-2-0`)
- **OSC 1.13.0 manual jobs**: `k8s-confidential` subset from
  https://github.com/wainersm/kata-containers.git (branch: `ocp-k8s-suite`)

These are based on the upstream kata-containers test suite:
https://github.com/kata-containers/kata-containers/tree/main/tests/integration/kubernetes

**Test coverage:**
- `k8s-all` = `k8s-basic` (~30 tests) + `k8s-confidential` (6 tests) = ~36 total tests
- `k8s-confidential` suite includes:
  - k8s-confidential.bats - Basic CoCo enclave verification
  - k8s-confidential-attestation.bats - Remote attestation with KBS
  - k8s-sealed-secret.bats - Sealed secrets unsealing
  - k8s-guest-pull-image-signature.bats - Image signature verification in guest
  - k8s-confidential-encrypted-storage.bats - Encrypted storage volumes
  - k8s-confidential-custom-ca-registry.bats - Custom CA for registry access
