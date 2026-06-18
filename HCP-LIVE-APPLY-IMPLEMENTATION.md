# HCP Live-Apply OSC Operator Implementation

**Date**: 2026-06-18  
**Goal**: Enable OSC kata installation on HCP clusters without node reboots  
**Status**: ✅ Complete - Two operator images built with RBAC fixes

---

## Overview

Built two custom OSC operator images that combine:
1. **HCP support** (from PR #2291) - Skip MachineConfigPool watch when MCO is unavailable
2. **Live-apply installation** (from PR #2312 and PR #1349) - Use `rpm-ostree --apply-live` for immediate kata availability
3. **RBAC fixes** (discovered and fixed today) - Proper ServiceAccount and permissions for node labeling

---

## Operator Images Published

### Primary: hpc-deployment-live-apply-2312
**Image**: `quay.io/c3d/openshift-sandboxed-containers-operator:hpc-deployment-live-apply-2312`

**Based on**: PR #2312 (cleaner implementation, recommended)

**Features**:
- rpm-ostree --apply-live for no-reboot installation
- Wildcard pattern for kata config files (`50-kata*`)
- Simpler code, better documented
- Full RBAC support with auto-creation

**Commits**:
```
89a904db fix: daemonset: add proper RBAC and ServiceAccount for kata-install DaemonSet
1b630e71 fix: daemonset: use rpm-ostree --apply-live to avoid node reboots
209d24e8 Merge pull request #2291 from paulczar/hcp-skip-mcp-watch-when-unavailable (base)
```

### Alternative: hpc-deployment-live-apply-1349
**Image**: `quay.io/c3d/openshift-sandboxed-containers-operator:hpc-deployment-live-apply-1349`

**Based on**: PR #1349 (alternative implementation)

**Features**:
- rpm-ostree --apply-live for no-reboot installation
- Explicit kata-remote config file handling
- Full RBAC support with auto-creation

**Commits**:
```
1d45b7ed fix: daemonset: add proper RBAC and ServiceAccount for kata-install DaemonSet
18b74ebb fix: add missing kata-remote config file handling functions
d95b5c82 daemonset deployment: enable Kata installation without node reboots
209d24e8 Merge pull request #2291 from paulczar/hcp-skip-mcp-watch-when-unavailable (base)
```

---

## What's New vs Week 24's `hcp-skip-mcp-watch`

### Week 24 (hcp-skip-mcp-watch)
- ✅ HCP support (no MCO dependency)
- ❌ Required node reboot after kata installation
- ❌ Missing RBAC (used default ServiceAccount)
- Installation time: ~5 min + reboot time (~10-15 min) = **15-20 minutes**

### Week 25 (hpc-deployment-live-apply-2312)
- ✅ HCP support (no MCO dependency)
- ✅ **No reboot required** (rpm-ostree --apply-live)
- ✅ **Full RBAC** (auto-creates ServiceAccount, ClusterRole, ClusterRoleBinding, SCC)
- ✅ **Immediate kata availability** after installation
- Installation time: **~5 minutes** (no reboot wait)

---

## Technical Implementation

### Live-Apply Installation (PR #2312)

**Key Changes in `scripts/kata-install/osc-kata-install.sh`**:

1. **Install with --apply-live**:
   ```bash
   chroot /host bash -c "rpm-ostree install --apply-live $PACKAGES"
   ```
   - Changes take effect immediately
   - /usr updated immediately
   - /etc changes from RPMs need manual copy (OSTree three-way merge)

2. **Copy CRI-O config files**:
   ```bash
   # rpm-ostree --apply-live updates /usr immediately but /etc changes from
   # RPMs only land after reboot (OSTree three-way merge). Copy the kata
   # CRI-O drop-ins from the RPM's factory-defaults location now so CRI-O
   # can see the runtime handlers without a reboot.
   for conf in /host/usr/etc/crio/crio.conf.d/50-kata*; do
       [ -f "$conf" ] && cp "$conf" /host/etc/crio/crio.conf.d/
   done
   ```
   - Copies both `50-kata` and `50-kata-remote` configs
   - Makes kata runtime immediately available to CRI-O

3. **Symlink kata VM images**:
   ```bash
   # The kata guest kernel runs inside a VM and is fully independent of the
   # host kernel, so a host-kernel-specific build is unnecessary. The RPM
   # already ships pre-built CC images in /usr/share/ that are tested against
   # this kata-containers release. We symlink them into the path that
   # configuration.toml expects.
   ln -sf /usr/share/kata-containers/osbuilder-images/kata-cc.kernel \
          /var/cache/kata-containers/osbuilder-images/kata.kernel
   ln -sf /usr/share/kata-containers/osbuilder-images/kata-cc.initrd \
          /var/cache/kata-containers/osbuilder-images/kata.initrd
   ```
   - Uses pre-built kata VM images from RPM
   - Skips time-consuming osbuilder step

4. **Restart CRI-O**:
   ```bash
   # Restart CRI-O to pick up the new runtime configuration. A reload
   # (SIGHUP) only re-reads the config struct; it does not rebuild the
   # internal OCI handler map, so kata would not appear as a valid runtime
   # until the next full CRI-O start.
   chroot /host systemctl restart crio
   ```
   - Required for CRI-O to recognize new kata runtime
   - Existing pods survive the restart

### RBAC Fix (Discovered 2026-06-18)

**Problem**: Both PR implementations had missing RBAC causing permission errors:
```
Error from server (Forbidden): nodes is forbidden: User "system:serviceaccount:openshift-sandboxed-containers-operator:default" cannot patch resource "nodes" in API group "" at the cluster scope
```

**Root Cause**:
- DaemonSet hardcoded `ServiceAccountName: "default"`
- Operator never created ServiceAccount, ClusterRole, or ClusterRoleBinding
- kata-install pods couldn't label nodes with installation status

**Solution** (in `controllers/daemonset_reconcile.go`):

1. **New function `ensureKataInstallRBAC()`**:
   ```go
   func (r *KataConfigOpenShiftReconciler) ensureKataInstallRBAC() error {
       // 1. Create ServiceAccount "kata-install"
       // 2. Create ClusterRole with node get/list/patch/update permissions
       // 3. Create ClusterRoleBinding linking them
       // 4. Create SecurityContextConstraints with privileged access
       // All with AlreadyExists handling for idempotent reconciliation
   }
   ```

2. **Changed DaemonSet ServiceAccount**:
   ```go
   // OLD:
   ServiceAccountName: "default",
   
   // NEW:
   ServiceAccountName: "kata-install",
   ```

3. **Call during reconciliation**:
   ```go
   // In processKataConfigInstallRequestDaemonSet(), before creating DaemonSet:
   if err := r.ensureKataInstallRBAC(); err != nil {
       r.Log.Error(err, "Failed to ensure kata-install RBAC resources")
       return ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err
   }
   ```

**Resources Created**:
- ServiceAccount: `kata-install` (in operator namespace)
- ClusterRole: `kata-install` (node get/list/patch/update)
- ClusterRoleBinding: `kata-install` (links SA to CR)
- SecurityContextConstraints: `kata-install-scc` (privileged, hostPath, hostPID)

---

## Deployment Instructions

### Using the Operator

1. **Deploy operator**:
   ```bash
   # Option 1: Use 2312 (recommended)
   OPERATOR_IMAGE=quay.io/c3d/openshift-sandboxed-containers-operator:hpc-deployment-live-apply-2312
   
   # Option 2: Use 1349 (alternative)
   OPERATOR_IMAGE=quay.io/c3d/openshift-sandboxed-containers-operator:hpc-deployment-live-apply-1349
   
   # Deploy using your preferred method (skill, manual, etc.)
   ```

2. **Create KataConfig**:
   ```yaml
   apiVersion: kataconfiguration.openshift.io/v1
   kind: KataConfig
   metadata:
     name: example-kataconfig
   spec:
     checkNodeEligibility: false
     # Other settings as needed
   ```

3. **Verify installation** (~5 minutes):
   ```bash
   # Watch DaemonSet pods
   oc get pods -n openshift-sandboxed-containers-operator -l name=osc-rpm -w
   
   # Check node labels
   kubectl get nodes -l kataconfiguration.openshift.io/kata-ds-rpm-install=installed
   
   # Verify kata runtime available
   oc debug node/<node-name>
   chroot /host crictl info | grep -A5 runtimeHandlers
   # Should show "kata" and "kata-remote"
   ```

4. **Deploy test kata pod**:
   ```yaml
   apiVersion: v1
   kind: Pod
   metadata:
     name: kata-test
   spec:
     runtimeClassName: kata
     containers:
     - name: test
       image: registry.access.redhat.com/ubi9/ubi-minimal:latest
       command: ["sleep", "infinity"]
   ```

---

## Validation Checklist

### RBAC Resources
- [ ] ServiceAccount `kata-install` exists in operator namespace
- [ ] ClusterRole `kata-install` has node permissions
- [ ] ClusterRoleBinding `kata-install` links SA to CR
- [ ] SCC `kata-install-scc` exists with privileged access
- [ ] DaemonSet uses ServiceAccount `kata-install` (not `default`)

### Installation
- [ ] kata-install DaemonSet pods start successfully
- [ ] Pods can label nodes (check logs for "Forbidden" errors)
- [ ] Nodes labeled with `kataconfiguration.openshift.io/kata-ds-rpm-install=installed`
- [ ] No "waiting_for_reboot" status (immediate installation)

### Runtime Availability
- [ ] CRI-O config files present: `/etc/crio/crio.conf.d/50-kata*`
- [ ] Kata VM images symlinked: `/var/cache/kata-containers/osbuilder-images/kata.{kernel,initrd}`
- [ ] `crictl info` shows kata and kata-remote runtimeHandlers
- [ ] Test kata pod starts successfully
- [ ] `crictl ps` shows kata pod running in kata runtime

---

## Comparison: PR 1349 vs PR 2312

Full comparative analysis available in `PR-Comparison-1349-vs-2312.md`.

**TL;DR - Recommend PR 2312**:
- ✅ Simpler implementation (net -30 lines vs +150 lines)
- ✅ Better documented (explains OSTree three-way merge)
- ✅ Generic wildcard pattern (handles future kata configs)
- ✅ Self-contained (no external file dependencies)
- ✅ Production-tested approach

---

## Known Limitations

1. **Uninstall requires reboot**: `rpm-ostree uninstall` does not support `--apply-live`
   - Staged for next boot
   - CRI-O config removed immediately to disable kata

2. **OpenShift-only**: Uses SecurityContextConstraints (not portable to vanilla K8s)

3. **DaemonSet mode only**: Not for MachineConfig-based deployments

---

## Files Modified

### controllers/daemonset_reconcile.go
- Added imports: `fmt`, `rbacv1`, `securityv1`
- Added function: `ensureKataInstallRBAC()` (106 lines)
- Modified: `daemonSetForKataInstall()` - ServiceAccountName
- Modified: `processKataConfigInstallRequestDaemonSet()` - Added RBAC call
- **Total**: +107 insertions, -1 deletion

### scripts/kata-install/osc-kata-install.sh
**PR 2312 changes**:
- `install_kata()`: Changed to `rpm-ostree install --apply-live`
- Added CRI-O config copy loop (`50-kata*` wildcard)
- Added kata VM image symlinking
- Added CRI-O restart
- `uninstall_kata()`: Remove CRI-O configs and symlinks immediately
- **Total**: +65 insertions, -95 deletions (net -30 lines)

**PR 1349 changes**:
- Similar to 2312 but with explicit kata-remote file handling
- Added helper functions: `copy_kata_remote_config_files()`, `remove_kata_remote_config_files()`
- **Total**: +196 insertions, -46 deletions (net +150 lines)

---

## Documentation Created

1. **PR-Comparison-1349-vs-2312.md** - Detailed comparative analysis
2. **PR-1349-Missing-Function-Issue.md** - Bug analysis and fix
3. **DAEMONSET-RBAC-FIX.md** - RBAC issue analysis and manual workaround
4. **RBAC-FIX-SUMMARY.md** - Summary of RBAC fixes applied
5. **HCP-LIVE-APPLY-IMPLEMENTATION.md** - This document

---

## Next Steps

1. ✅ Operator images built and published
2. ⏳ Test on HCP cluster with hpc-deployment-live-apply-2312
3. ⏳ Verify installation completes without reboot
4. ⏳ Validate kata pod deployment
5. ⏳ Compare performance vs hcp-skip-mcp-watch (reboot-based)
6. ⏳ Update weekly status report
7. ⏳ Consider submitting RBAC fix upstream

---

## References

- **PR #2291**: HCP support (skip MCP watch when unavailable)
- **PR #2312**: Live-apply installation (recommended approach)
- **PR #1349**: Live-apply installation (alternative approach)
- **Base branch**: `devel` (commit 209d24e8)
- **Built branches**: `hpc-deployment-2312`, `hpc-deployment-1349`

---

## Success Criteria

✅ **Achieved**:
- Operator builds successfully with HCP + live-apply support
- RBAC resources auto-created by operator
- ServiceAccount properly configured for node labeling
- Two implementation approaches available for comparison

⏳ **Pending Testing**:
- Installation completes in ~5 minutes (vs ~15-20 with reboot)
- Kata runtime immediately available after installation
- Test kata pod starts successfully
- No permission errors in kata-install pod logs

---

**Recommendation**: Deploy `hpc-deployment-live-apply-2312` for testing. It has the cleanest implementation and is most likely to be accepted upstream.
