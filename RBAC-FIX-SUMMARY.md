# RBAC Fix Applied to Both HCP Live-Apply Branches

**Date**: 2026-06-18  
**Branches**: hpc-deployment-1349, hpc-deployment-2312  
**Status**: Fix committed and building

---

## What Was Fixed

### Root Problem
Both PR implementations created the kata-install DaemonSet with:
- **Wrong ServiceAccount**: hardcoded `ServiceAccountName: "default"`
- **No RBAC**: operator never created ServiceAccount, ClusterRole, or ClusterRoleBinding  
- **No SCC binding**: SecurityContextConstraints existed but not bound

This caused kata-install pods to fail with permission errors when trying to label nodes.

### Solution Applied
Added comprehensive RBAC support to the operator in `controllers/daemonset_reconcile.go`:

1. **New function `ensureKataInstallRBAC()`** that creates:
   - ServiceAccount "kata-install" in operator namespace
   - ClusterRole with node get/list/patch/update permissions
   - ClusterRoleBinding linking ServiceAccount to ClusterRole
   - SecurityContextConstraints with privileged access
   - Uses AlreadyExists handling for idempotent reconciliation

2. **Changed DaemonSet spec** (line 516):
   - FROM: `ServiceAccountName: "default"`
   - TO: `ServiceAccountName: "kata-install"`

3. **Call ensureKataInstallRBAC()** in `processKataConfigInstallRequestDaemonSet()`  
   - Called after finalizer is added
   - Called before DaemonSet creation
   - Returns with requeue on error

---

## Commits

### hpc-deployment-2312 Branch
```
89a904db fix: daemonset: add proper RBAC and ServiceAccount for kata-install DaemonSet
1b630e71 fix: daemonset: use rpm-ostree --apply-live to avoid node reboots
209d24e8 Merge pull request #2291 from paulczar/hcp-skip-mcp-watch-when-unavailable (base)
```

### hpc-deployment-1349 Branch  
```
1d45b7ed fix: daemonset: add proper RBAC and ServiceAccount for kata-install DaemonSet (cherry-picked)
18b74ebb fix: add missing kata-remote config file handling functions
d95b5c82 daemonset deployment: enable Kata installation without node reboots
209d24e8 Merge pull request #2291 from paulczar/hcp-skip-mcp-watch-when-unavailable (base)
```

**Note**: The RBAC fix (89a904db) was cherry-picked cleanly from 2312 to 1349.

---

## New Operator Images

### Image Tags (with RBAC fixes)
- `quay.io/c3d/openshift-sandboxed-containers-operator:hpc-deployment-live-apply-2312`  
- `quay.io/c3d/openshift-sandboxed-containers-operator:hpc-deployment-live-apply-1349`

### Build Status
- **2312**: Building (podman build in progress)
- **1349**: Pending (will build after 2312 completes)

---

## Code Changes Summary

### Imports Added
```go
import (
    "fmt"                                  // for fmt.Errorf and fmt.Sprintf
    rbacv1 "k8s.io/api/rbac/v1"          // for ClusterRole, ClusterRoleBinding
    securityv1 "github.com/openshift/api/security/v1"  // for SCC
)
```

### New Function (106 lines)
```go
func (r *KataConfigOpenShiftReconciler) ensureKataInstallRBAC() error {
    // Creates ServiceAccount, ClusterRole, ClusterRoleBinding, SCC
    // All with AlreadyExists handling
    // Returns error with context for requeue handling
}
```

### Modified Lines
- Line 516: `ServiceAccountName: "kata-install"` (was "default")
- processKataConfigInstallRequestDaemonSet(): Added ensureKataInstallRBAC() call

### Total Changes
- +107 insertions, -1 deletion

---

## Testing After Deployment

### Verify RBAC Resources Created
```bash
# ServiceAccount
oc get sa kata-install -n openshift-sandboxed-containers-operator

# ClusterRole
oc get clusterrole kata-install
oc describe clusterrole kata-install

# ClusterRoleBinding
oc get clusterrolebinding kata-install
oc describe clusterrolebinding kata-install

# SecurityContextConstraints
oc get scc kata-install-scc
oc describe scc kata-install-scc | grep -A2 Users
```

### Verify DaemonSet Configuration
```bash
# Check ServiceAccount assignment
oc get daemonset osc-rpm -n openshift-sandboxed-containers-operator \
  -o jsonpath='{.spec.template.spec.serviceAccountName}'
# Should output: kata-install

# Check pod permissions
oc get pods -n openshift-sandboxed-containers-operator -l name=osc-rpm
oc logs -n openshift-sandboxed-containers-operator -l name=osc-rpm
# Should NOT see "Forbidden: nodes is forbidden" errors
```

### Verify Node Labeling Works
```bash
# Pods should successfully label nodes
kubectl get nodes -l kataconfiguration.openshift.io/kata-ds-rpm-install
# Should show nodes with installation status labels
```

---

## Differences from Manual Workaround

The RBAC fix is now **built into the operator**, so:
- ✅ No manual RBAC YAML needed
- ✅ Resources auto-created on KataConfig install
- ✅ Resources cleaned up via OwnerReference when KataConfig deleted
- ✅ Idempotent - safe to reconcile multiple times
- ✅ Proper error handling and requeue on failure

---

## Impact

### Affected Deployments
- ✅ All DaemonSet mode deployments (HCP and non-HCP)
- ✅ Both hpc-deployment-1349 and hpc-deployment-2312
- ❌ NOT OperatorHub OSC 1.12.1 (doesn't have DaemonSet mode)

### Severity
**Critical** - DaemonSet mode was completely non-functional without this fix.

### Recommendation
- **For testing**: Use either 2312 or 1349 images (both now have RBAC fix)
- **For production**: Still recommend 2312 (simpler implementation, better documented)
- **For upstream**: This fix should be submitted as a PR to add proper RBAC

---

## Related Documentation
- `DAEMONSET-RBAC-FIX.md` - Detailed analysis and manual workaround instructions
- `PR-Comparison-1349-vs-2312.md` - Comparative analysis of the two approaches
- `PR-1349-Missing-Function-Issue.md` - Analysis of missing function bug (now fixed)

---

## Next Steps

1. ✅ RBAC fix committed to both branches
2. 🔄 Building hpc-deployment-live-apply-2312 image  
3. ⏳ Will build hpc-deployment-live-apply-1349 image next
4. ⏳ Push both images to quay.io/c3d
5. ⏳ Test on HCP cluster
6. ⏳ Update weekly status report
