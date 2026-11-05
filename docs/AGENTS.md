# Agent Instructions for Documentation Maintenance

This document provides guidance for AI agents (like Claude) on how to update and maintain documentation in the `docs/` directory.

## General Guidelines

- Documentation files in this directory should be kept up-to-date with the actual implementation
- Always verify information against source repositories before updating
- Use GitHub/GitLab URLs to reference source files (not local filesystem paths)

---

## CI.md Maintenance

`CI.md` documents the continuous integration systems used to test the OpenShift Sandboxed Containers operator:

1. **OpenShift CI (Prow)**: Cloud-based CI on Azure/AWS infrastructure
2. **Bare-metal CI (Jenkins)**: Physical hardware CI with TDX/SNP TEE support

### Source of Truth Locations

#### OpenShift CI (Prow)

**Job definitions:**
- Repository: https://github.com/openshift/release
- Path: `ci-operator/config/openshift/sandboxed-containers-operator/`
- Files: `openshift-sandboxed-containers-operator-devel__downstream-candidate*.yaml`
- Direct link: https://github.com/openshift/release/tree/main/ci-operator/config/openshift/sandboxed-containers-operator

**Step registry:**
- Path: `ci-operator/step-registry/sandboxed-containers-operator/`
- Direct link: https://github.com/openshift/release/tree/main/ci-operator/step-registry/sandboxed-containers-operator

**How to verify (if you have the repo cloned):**
```bash
# From the openshift/release repository
cd ci-operator/config/openshift/sandboxed-containers-operator/

# Extract job details from a candidate
python3 << 'EOF'
import yaml
with open("openshift-sandboxed-containers-operator-devel__downstream-candidate422.yaml") as f:
    data = yaml.safe_load(f)
    for test in data['tests']:
        print(f"{test['as']:40} | cron: {test.get('cron', 'N/A')}")
EOF
```

**Key fields to check:**
- `tests[].as` - Job name
- `tests[].cron` - Schedule (disabled jobs use "0 0 31 2 1" = Feb 31st)
- `tests[].steps.env.KATA_RPM_VERSION` - Kata RPM version
- `tests[].steps.env.INSTALL_KATA_RPM` - Whether to install scratch RPM
- `tests[].steps.env.CATALOG_SOURCE_IMAGE` - OSC catalog image
- `tests[].steps.env.TRUSTEE_CATALOG_SOURCE_IMAGE` - Trustee catalog image
- `tests[].steps.env.WORKLOAD_TO_TEST` - Workload type (kata, peer-pods, coco)
- `tests[].steps.workflow` - Workflow name
- `tests[].steps.cluster_profile` - Cloud provider profile

#### Bare-metal CI (Jenkins)

**Job definitions:**
- Repository: https://gitlab.cee.redhat.com/ldoktor/kata-bm
- File: `jjb/kata-bm-provision.yaml`
- Direct link: https://gitlab.cee.redhat.com/ldoktor/kata-bm/blob/main/jjb/kata-bm-provision.yaml

**Test runner:**
- File: `pipelines/kata-bm-provision/kata-bm-test-runner.sh`
- Direct link: https://gitlab.cee.redhat.com/ldoktor/kata-bm/blob/main/pipelines/kata-bm-provision/kata-bm-test-runner.sh
- Contains test suite definitions (TESTS_K8S_ALL, TESTS_K8S_CONFIDENTIAL, etc.)

**Key fields to check:**
- `kata_rpm_version` - Kata RPM version pattern
- `kata_src_repo` - Test source repository
- `kata_src_branch` - Test source branch
- `kata_tests` - Test suite name (k8s-all, k8s-confidential, etc.)
- `scenario[].schedule` - Cron schedule
- `scenario[].ocp_version` - OCP version
- `scenario[].profile` - Network profile
- `tee[].machine_selection` - Machine name

### Common Update Scenarios

#### 1. New OCP Version (e.g., candidate423)

**Steps:**
1. Check the [config directory](https://github.com/openshift/release/tree/main/ci-operator/config/openshift/sandboxed-containers-operator) for new candidate file

2. Extract job list and configuration from the YAML file

3. Add new table rows to CI.md following the existing pattern

4. Update the Kata RPM version in the configuration section

#### 2. Kata RPM Version Update

**When:** Kata RPM build number changes (e.g., 4.2.0-1 → 4.2.0-2)

**Steps:**
1. Check Prow configs for `KATA_RPM_VERSION:` in the [config files](https://github.com/openshift/release/tree/main/ci-operator/config/openshift/sandboxed-containers-operator)

2. Check Jenkins config for `kata_rpm_version:` in [kata-bm-provision.yaml](https://gitlab.cee.redhat.com/ldoktor/kata-bm/blob/main/jjb/kata-bm-provision.yaml)

3. Update both sections in CI.md:
   - **OpenShift CI**: Update the list under "Kata RPM versions"
   - **Bare-metal CI**: Update "Kata RPM:" under Configuration

#### 3. New Bare-metal Weekly Jobs (OSC Version)

**When:** New OSC version added (e.g., OSC 1.15.0)

**Steps:**
1. Check [kata-bm-provision.yaml](https://gitlab.cee.redhat.com/ldoktor/kata-bm/blob/main/jjb/kata-bm-provision.yaml) for new project definitions (search for `name: kata-bm-.*-pipelines`)

2. Extract configuration:
   - OSC version
   - OCP versions tested
   - Network profiles
   - Schedule
   - Machines used
   - Test suite

3. Add new subsection under "Weekly scheduled jobs"

#### 4. Catalog Image Update

**When:** OSC or Trustee catalog images change

**Steps:**
1. Check Prow configs for `CATALOG_SOURCE_IMAGE` and `TRUSTEE_CATALOG_SOURCE_IMAGE`

2. Check Jenkins config for `catalog_source_image` and `trustee_catalog_source_image`

3. Update both CI systems' configuration sections

#### 5. Test Suite Changes

**When:** Test suites are added/removed/renamed

**Steps:**
1. Check [Prow step registry](https://github.com/openshift/release/tree/main/ci-operator/step-registry/sandboxed-containers-operator) for test suite changes

2. Check [kata-bm-test-runner.sh](https://gitlab.cee.redhat.com/ldoktor/kata-bm/blob/main/pipelines/kata-bm-provision/kata-bm-test-runner.sh) for suite definitions (search for `TESTS_K8S_`)

3. Update the "Test suites" section

### Validation Checklist

Before committing updates to CI.md, verify:

- [ ] All job URLs are valid (check one from each candidate)
- [ ] Kata RPM versions match source configs exactly
- [ ] Catalog images match source configs exactly
- [ ] Schedule descriptions are accurate (daily Mon-Fri, weekly Sunday, etc.)
- [ ] Variant column uses correct terminology ("local" vs "Peer Pods")
- [ ] Cloud provider column is correct (azure, aws, not N/A for cloud jobs)
- [ ] Table formatting is correct (proper separator count)
- [ ] Test suite lists match actual test files
- [ ] Machine names match Jenkins config
- [ ] Network profile descriptions are accurate

### Common Mistakes to Avoid

1. **Don't confuse variant terminology:**
   - "local" = Kata VMs run on cluster node (kata runtime)
   - "Peer Pods" = Kata VMs run in cloud (kata-remote runtime)
   - NOT "bare-metal" for cloud-based kata jobs

2. **Don't assume cloud provider:**
   - Check the `cluster_profile` field in Prow configs
   - "azure-qe" = azure, "aws-*" = aws
   - Kata jobs run on Azure even though they don't use peer pods

3. **Don't copy-paste version numbers:**
   - Verify each candidate's Kata RPM version individually
   - candidate420 and candidate421 may use the same version
   - candidate422 often uses a different version

4. **Don't ignore disabled jobs:**
   - Jobs with cron "0 0 31 2 1" are disabled (Feb 31st never happens)
   - Don't include them in the daily jobs table
   - Document them separately if needed

5. **Don't skip INSTALL_KATA_RPM verification:**
   - Disconnected jobs typically use INSTALL_KATA_RPM=false
   - STS jobs typically use INSTALL_KATA_RPM=false
   - Document this in the configuration notes

### Questions to Ask Before Updating

If the user requests an update, verify:

1. **What changed?** (new version, new jobs, configuration update)
2. **Which CI system?** (Prow, Jenkins, or both)
3. **Where is the source?** (verify URLs to configs)
4. **Should old entries be removed?** (or just add new ones)
5. **Are there related changes?** (e.g., new OCP version might need new Kata RPM)

---

## Additional Resources

- OpenShift Release repo: https://github.com/openshift/release
- Bare-metal CI repo: https://gitlab.cee.redhat.com/ldoktor/kata-bm
- Project maintainers: See OWNERS files in this repository
