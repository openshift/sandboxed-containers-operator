# DaemonSet Mode RBAC Fix for HCP

**Date**: 2026-06-18  
**Applies to**: Both hpc-deployment-1349 and hpc-deployment-2312  
**Issue**: Missing RBAC and SCC configuration for DaemonSet mode on HCP

---

## Problem Summary

Custom operator builds (1349 and 2312) create the kata-install DaemonSet but fail because:

1. **Wrong ServiceAccount**: DaemonSet uses `default` instead of `kata-install`
2. **Missing RBAC**: No ServiceAccount, ClusterRole, or ClusterRoleBinding created
3. **Missing SCC Binding**: SCC exists but not bound to ServiceAccount

This causes the kata-install pods to fail with permission errors when trying to label nodes.

---

## Root Cause

In `controllers/daemonset_reconcile.go`, the `daemonSetForKataInstall()` function hardcodes:

```go
Spec: corev1.PodSpec{
    ServiceAccountName: "default",  // ← WRONG
    ...
}
```

And the operator never creates the required RBAC resources.

---

## Required Fix in Operator Code

### 1. Change ServiceAccount Name

**File**: `controllers/daemonset_reconcile.go`  
**Line**: ~410

```go
// OLD:
ServiceAccountName: "default",

// NEW:
ServiceAccountName: "kata-install",
```

### 2. Add RBAC Resource Creation

Add a new function to ensure RBAC resources exist before creating the DaemonSet:

```go
func (r *KataConfigOpenShiftReconciler) ensureKataInstallRBAC() error {
    ctx := context.TODO()
    namespace := r.kataConfig.Namespace

    // 1. Create ServiceAccount
    sa := &corev1.ServiceAccount{
        ObjectMeta: metav1.ObjectMeta{
            Name:      "kata-install",
            Namespace: namespace,
        },
    }
    if err := controllerutil.SetControllerReference(r.kataConfig, sa, r.Scheme); err != nil {
        return err
    }
    if err := r.Client.Create(ctx, sa); err != nil && !k8serrors.IsAlreadyExists(err) {
        return err
    }

    // 2. Create ClusterRole
    cr := &rbacv1.ClusterRole{
        ObjectMeta: metav1.ObjectMeta{
            Name: "kata-install",
        },
        Rules: []rbacv1.PolicyRule{
            {
                APIGroups: []string{""},
                Resources: []string{"nodes"},
                Verbs:     []string{"get", "list", "patch", "update"},
            },
        },
    }
    if err := r.Client.Create(ctx, cr); err != nil && !k8serrors.IsAlreadyExists(err) {
        return err
    }

    // 3. Create ClusterRoleBinding
    crb := &rbacv1.ClusterRoleBinding{
        ObjectMeta: metav1.ObjectMeta{
            Name: "kata-install",
        },
        RoleRef: rbacv1.RoleRef{
            APIGroup: "rbac.authorization.k8s.io",
            Kind:     "ClusterRole",
            Name:     "kata-install",
        },
        Subjects: []rbacv1.Subject{
            {
                Kind:      "ServiceAccount",
                Name:      "kata-install",
                Namespace: namespace,
            },
        },
    }
    if err := r.Client.Create(ctx, crb); err != nil && !k8serrors.IsAlreadyExists(err) {
        return err
    }

    // 4. Create SecurityContextConstraints (OpenShift specific)
    scc := &securityv1.SecurityContextConstraints{
        ObjectMeta: metav1.ObjectMeta{
            Name: "kata-install-scc",
        },
        AllowPrivilegedContainer: true,
        AllowHostDirVolumePlugin: true,
        AllowHostPID:             true,
        AllowHostNetwork:         false,
        AllowHostPorts:           false,
        AllowHostIPC:             false,
        ReadOnlyRootFilesystem:   false,
        RunAsUser: securityv1.RunAsUserStrategyOptions{
            Type: securityv1.RunAsUserStrategyRunAsAny,
        },
        SELinuxContext: securityv1.SELinuxContextStrategyOptions{
            Type: securityv1.SELinuxStrategyRunAsAny,
        },
        Users: []string{
            fmt.Sprintf("system:serviceaccount:%s:kata-install", namespace),
        },
        Volumes: []securityv1.FSType{
            securityv1.FSTypeHostPath,
            securityv1.FSTypeSecret,
            securityv1.FSTypeConfigMap,
        },
    }
    if err := r.Client.Create(ctx, scc); err != nil && !k8serrors.IsAlreadyExists(err) {
        return err
    }

    return nil
}
```

### 3. Required Imports

Add to imports in `daemonset_reconcile.go`:

```go
import (
    // ... existing imports ...
    rbacv1 "k8s.io/api/rbac/v1"
    securityv1 "github.com/openshift/api/security/v1"
    "fmt"
)
```

### 4. Call ensureKataInstallRBAC

In `processKataConfigInstallRequestDaemonSet()`, before creating the DaemonSet:

```go
func (r *KataConfigOpenShiftReconciler) processKataConfigInstallRequestDaemonSet() (ctrl.Result, error) {
    // ... existing code ...

    // Ensure RBAC resources exist
    if err := r.ensureKataInstallRBAC(); err != nil {
        r.Log.Error(err, "Failed to ensure kata-install RBAC resources")
        return ctrl.Result{Requeue: true, RequeueAfter: 15 * time.Second}, err
    }

    // ... rest of function ...
}
```

---

## Manual Workaround (Until Operator is Fixed)

If using the current unfixed operator builds, manually create these resources:

### 1. ServiceAccount

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: kata-install
  namespace: openshift-sandboxed-containers-operator
```

### 2. ClusterRole

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kata-install
rules:
- apiGroups: [""]
  resources: ["nodes"]
  verbs: ["get", "list", "patch", "update"]
```

### 3. ClusterRoleBinding

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: kata-install
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: kata-install
subjects:
- kind: ServiceAccount
  name: kata-install
  namespace: openshift-sandboxed-containers-operator
```

### 4. SecurityContextConstraints

```yaml
apiVersion: security.openshift.io/v1
kind: SecurityContextConstraints
metadata:
  name: kata-install-scc
allowPrivilegedContainer: true
allowHostDirVolumePlugin: true
allowHostPID: true
allowHostNetwork: false
allowHostPorts: false
allowHostIPC: false
readOnlyRootFilesystem: false
runAsUser:
  type: RunAsAny
seLinuxContext:
  type: RunAsAny
users:
- system:serviceaccount:openshift-sandboxed-containers-operator:kata-install
volumes:
- hostPath
- secret
- configMap
```

### 5. Apply Workaround

```bash
oc apply -f kata-install-rbac.yaml
```

Then delete and recreate the KataConfig to trigger DaemonSet recreation with proper permissions.

---

## Testing the Fix

After applying the fix (either in code or manually):

1. **Verify ServiceAccount**:
   ```bash
   oc get sa kata-install -n openshift-sandboxed-containers-operator
   ```

2. **Verify ClusterRole**:
   ```bash
   oc get clusterrole kata-install
   ```

3. **Verify ClusterRoleBinding**:
   ```bash
   oc get clusterrolebinding kata-install
   ```

4. **Verify SCC**:
   ```bash
   oc get scc kata-install-scc
   ```

5. **Verify DaemonSet uses correct SA**:
   ```bash
   oc get daemonset osc-rpm -n openshift-sandboxed-containers-operator -o jsonpath='{.spec.template.spec.serviceAccountName}'
   ```
   Should output: `kata-install`

6. **Verify pods can label nodes**:
   ```bash
   oc logs -n openshift-sandboxed-containers-operator -l name=osc-rpm
   ```
   Should NOT show permission errors

---

## Impact

**Affects**:
- ✅ All OSC DaemonSet mode deployments (not just HCP)
- ✅ Both custom builds: hpc-deployment-1349 and hpc-deployment-2312  
- ❌ OperatorHub OSC 1.12.1 (doesn't have DaemonSet mode at all)

**Severity**: **Critical** - DaemonSet mode completely non-functional without this fix

---

## Recommendation

1. **Short term**: Use manual workaround for testing current builds
2. **Medium term**: Fix in both hpc-deployment branches and rebuild
3. **Long term**: Submit upstream PR to add proper RBAC creation

---

## Related Issues

- Missing node labeling permissions
- DaemonSet pods in CrashLoopBackOff
- "Forbidden: nodes is forbidden" errors in pod logs
- ServiceAccount "default" has no privileges

