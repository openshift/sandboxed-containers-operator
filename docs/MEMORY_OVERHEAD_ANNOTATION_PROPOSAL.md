# Memory Overhead Annotation Proposal

## Overview

This proposal outlines how to add support for the `io.katacontainers.config.hypervisor.memory_overhead` annotation in the OpenShift Sandboxed Containers Operator. This annotation allows external operators to specify VM memory overhead, which is crucial for proper cgroup management and preventing OOM issues.

## Background

The Kata Containers runtime now supports a memory overhead compensation mechanism that requires the `io.katacontainers.config.hypervisor.memory_overhead` annotation to be set on pods. This annotation works in conjunction with the existing RuntimeClass PodFixed overhead to ensure proper memory accounting.

### The Problem

Consider a scenario where the guest default memory is 2G and the pod overhead is 256M. When we tell the orchestrator about a container that requires 4G of memory, Kata would normally hotplug 4G. This means the guest is configured with 6G of memory. However, the orchestrator only knows about 4G (container) + 256M (overhead).

Within the guest, we set up guest-side cgroups, so the 4G limit for the container will be enforced. However, if the guest uses kernel resources (e.g., doing intensive I/O), that memory is not accounted in the guest cgroup. So the guest could end up using up to 6G of actual memory, exceeding the host-side cgroup, and the pod gets killed.

The memory overhead annotation solves this by telling the Kata runtime to adjust the first memory hotplug to account for the difference.

## Current State

The operator currently:
- Creates RuntimeClass objects with PodFixed overhead (CPU and memory)
- Uses hardcoded memory overhead values: 350Mi for kata, 120Mi for kata-remote
- Does not inject any Kata-specific annotations on pods

## Prerequisites

- Go 1.21+
- Kubernetes 1.24+
- controller-runtime v0.20.2+
- kubebuilder tools

## Implementation

### 1. Add Memory Overhead Configuration to KataConfigSpec

Add a new field to the KataConfigSpec to allow users to configure memory overhead:

```go
// KataConfigSpec defines the desired state of KataConfig
type KataConfigSpec struct {
    // ... existing fields ...

    // MemoryOverheadMB specifies the memory overhead in MB for Kata containers.
    // This value will be used to set the io.katacontainers.config.hypervisor.memory_overhead
    // annotation on pods using Kata runtime classes. If not specified, defaults to 350MB.
    // +optional
    // +kubebuilder:default:=350
    // +kubebuilder:validation:Minimum=1
    // +kubebuilder:validation:Maximum=2048
    MemoryOverheadMB *int32 `json:"memoryOverheadMB,omitempty"`
}
```

After modifying the types, regenerate code:
```bash
make generate
make manifests
```

### 2. Add Validation

Modify `api/v1/kataconfig_webhook.go`:

```go
func (r *KataConfig) ValidateCreate() (admission.Warnings, error) {
    kataconfiglog.Info("validate create", "name", r.Name)
    return nil, r.validateMemoryOverheadMB()
}

func (r *KataConfig) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
    kataconfiglog.Info("validate update", "name", r.Name)
    return nil, r.validateMemoryOverheadMB()
}

func (r *KataConfig) validateMemoryOverheadMB() error {
    if r.Spec.MemoryOverheadMB != nil {
        if *r.Spec.MemoryOverheadMB < 1 {
            return fmt.Errorf("memoryOverheadMB must be at least 1MB")
        }
        if *r.Spec.MemoryOverheadMB > 2048 {
            return fmt.Errorf("memoryOverheadMB must be at most 2048MB")
        }
    }
    return nil
}
```

### 3. Create Pod Mutation Webhook

Create `controllers/pod_mutator.go` with a webhook that implements `webhook.CustomDefaulter`:

```go
// +kubebuilder:webhook:path=/mutate-pods-v1,mutating=true,failurePolicy=fail,sideEffects=None,groups="",resources=pods,verbs=create;update,versions=v1,name=mpods.kb.io,admissionReviewVersions=v1

type PodMutator struct {
    Client client.Client
    Log    logr.Logger
}

func NewPodMutator(mgr ctrl.Manager) *PodMutator {
    return &PodMutator{
        Client: mgr.GetClient(),
        Log:    log.Log.WithName("pod-mutator"),
    }
}

func (m *PodMutator) SetupWebhookWithManager(mgr ctrl.Manager) error {
    return ctrl.NewWebhookManagedBy(mgr).
        For(&corev1.Pod{}).
        WithDefaulter(m).
        Complete()
}

// Default implements webhook.CustomDefaulter
func (m *PodMutator) Default(ctx context.Context, obj runtime.Object) error {
    pod, ok := obj.(*corev1.Pod)
    if !ok {
        return fmt.Errorf("expected a Pod but got a %T", obj)
    }

    memoryOverhead, err := m.getMemoryOverheadFromKataConfig(ctx, pod)
    if err != nil {
        return err
    }

    if memoryOverhead == 0 {
        return nil // Not a Kata pod, skip
    }

    return m.injectMemoryOverheadAnnotation(pod, memoryOverhead)
}
```

#### Memory Overhead Lookup

The webhook looks up the KataConfig to find the memory overhead value:

```go
func (m *PodMutator) getMemoryOverheadFromKataConfig(ctx context.Context, pod *corev1.Pod) (int32, error) {
    if pod.Spec.RuntimeClassName == nil {
        return 0, nil
    }

    runtimeClassName := *pod.Spec.RuntimeClassName

    // List all KataConfigs
    kataConfigs := &kataconfigurationv1.KataConfigList{}
    if err := m.Client.List(ctx, kataConfigs); err != nil {
        return 0, fmt.Errorf("failed to list KataConfigs: %w", err)
    }

    // Find matching KataConfig by checking Status.RuntimeClasses
    for _, kataConfig := range kataConfigs.Items {
        for _, rtc := range kataConfig.Status.RuntimeClasses {
            if runtimeClassName == rtc {
                if kataConfig.Spec.MemoryOverheadMB != nil {
                    return *kataConfig.Spec.MemoryOverheadMB, nil
                }
                return KataMemoryOverheadDefault, nil // 350
            }
        }
    }

    return 0, nil // Not a Kata runtime class
}
```

#### Annotation Injection

```go
func (m *PodMutator) injectMemoryOverheadAnnotation(pod *corev1.Pod, memoryOverhead int32) error {
    if pod.Annotations == nil {
        pod.Annotations = make(map[string]string)
    }
    pod.Annotations[KataMemoryOverheadAnnotation] = strconv.FormatInt(int64(memoryOverhead), 10)
    return nil
}
```

### 4. RBAC Configuration

Add to `config/rbac/role.yaml`:

```yaml
- apiGroups:
  - admissionregistration.k8s.io
  resources:
  - mutatingwebhookconfigurations
  verbs:
  - get
  - list
  - watch
  - create
  - update
  - patch
  - delete
- apiGroups:
  - ""
  resources:
  - pods
  verbs:
  - get
  - list
  - watch
  - create
  - update
  - patch
```

### 5. Webhook Configuration

Create `config/webhook/manifests.yaml`:

```yaml
apiVersion: admissionregistration.k8s.io/v1
kind: MutatingWebhookConfiguration
metadata:
  name: mpods.kb.io
webhooks:
- name: mpods.kb.io
  clientConfig:
    service:
      name: webhook-service
      namespace: system
      path: "/mutate-pods-v1"
  rules:
  - operations: ["CREATE", "UPDATE"]
    apiGroups: [""]
    apiVersions: ["v1"]
    resources: ["pods"]
  failurePolicy: Fail
  sideEffects: None
  admissionReviewVersions: ["v1"]
```

Create `config/webhook/service.yaml`:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: webhook-service
  namespace: system
spec:
  ports:
  - port: 443
    targetPort: 9443
  selector:
    app.kubernetes.io/name: sandboxed-containers-operator
```

### 6. Deployment Configuration

Modify `config/manager/manager.yaml`:

```yaml
spec:
  template:
    spec:
      containers:
      - name: manager
        args:
        - --webhook-port=9443
        ports:
        - containerPort: 9443
          name: webhook-server
          protocol: TCP
        volumeMounts:
        - name: webhook-certs
          mountPath: /tmp/k8s-webhook-server/serving-certs
          readOnly: true
      volumes:
      - name: webhook-certs
        secret:
          secretName: webhook-server-cert
```

## Configuration

### Default Values

| Constant | Value | Description |
|----------|-------|-------------|
| `KataMemoryOverheadDefault` | 350 | Default memory overhead in MB |
| `KataMemoryOverheadAnnotation` | `io.katacontainers.config.hypervisor.memory_overhead` | Annotation key |

### Validation Rules

- Memory overhead must be positive (minimum: 1 MB)
- Maximum reasonable limit: 2048 MB (2 GB)
- Nil value uses the default (350 MB)

## Files Structure

### New Files
- `controllers/pod_mutator.go` - Pod mutation webhook implementation
- `controllers/pod_mutator_test.go` - Unit tests

### Modified Files
- `api/v1/kataconfig_types.go` - Add MemoryOverheadMB field
- `api/v1/kataconfig_webhook.go` - Add validation for MemoryOverheadMB
- `api/v1/kataconfig_webhook_test.go` - Add validation tests
- `config/rbac/role.yaml` - Add webhook permissions
- `config/webhook/manifests.yaml` - Add pod mutation webhook

## Testing

See [MEMORY_OVERHEAD_ANNOTATION_TESTING_GUIDE.md](MEMORY_OVERHEAD_ANNOTATION_TESTING_GUIDE.md) for comprehensive testing instructions.

### Quick Test Commands

```bash
# Run all unit tests
./scripts/webhook-tests/run_pod_overhead_mutator.sh

# Run E2E tests (requires cluster)
./scripts/webhook-tests/test_memory_overhead.sh

# Run specific tests
go test -v ./controllers -run TestPodMutator
go test -v ./api/v1 -run TestKataConfig
```

## Troubleshooting

### Common Issues

1. **Webhook not registered**
   - Check RBAC permissions
   - Verify webhook configuration
   - Check controller logs

2. **Annotation not injected**
   - Verify pod uses Kata runtime class
   - Check that `KataConfig.Status.RuntimeClasses` contains the runtime class name
   - Verify KataConfig exists

3. **Validation errors**
   - Check MemoryOverheadMB value is within range (1-2048)
   - Verify validation logic in webhook logs

### Debug Commands

```bash
# Check webhook status
kubectl get mutatingwebhookconfiguration mpods.kb.io -o yaml

# Check webhook logs
kubectl logs -n openshift-sandboxed-containers-operator \
  deployment/sandboxed-containers-operator-controller-manager | grep "pod-mutator"

# Test webhook with dry-run
kubectl run test-pod --image=nginx --runtime-class=kata --dry-run=client -o yaml

# Check pod annotations
kubectl get pod <pod-name> -o jsonpath='{.metadata.annotations}'

# Check KataConfig status
kubectl get kataconfig -o yaml
```

## Migration Strategy

### Backward Compatibility
- Existing deployments continue to work with default values
- No breaking changes to existing API
- Annotation is only added for Kata pods

### Upgrade Path
- Operator upgrade automatically enables webhook
- Existing KataConfigs get default memory overhead (350 MB)
- Users can customize via KataConfig spec

## Security Considerations

### Webhook Security
- Proper RBAC permissions for webhook
- Secure webhook endpoint (TLS)
- Rate limiting to prevent abuse

### Resource Limits
- Maximum memory overhead limit (2048 MB)
- Validation to prevent excessive values
- Monitoring for unusual patterns

## Performance Considerations

1. **Webhook Performance**
   - Keep webhook logic simple and fast
   - Lists KataConfigs on each pod admission (typically only one exists)
   - Monitor webhook latency

2. **Resource Usage**
   - Monitor webhook pod resource usage
   - Set appropriate resource limits
   - Consider caching KataConfig lookups if needed

## Monitoring and Observability

### Metrics
- Number of pods with memory overhead annotation
- Webhook admission success/failure rates
- Memory overhead distribution

### Logging
- Webhook admission decisions
- Configuration changes
- Error conditions

### Alerts
- Webhook failure rates
- High webhook latency
- Configuration validation errors

## Benefits

1. **Proper Memory Accounting**: Ensures VM memory usage aligns with orchestrator expectations
2. **OOM Prevention**: Reduces risk of pods being killed due to memory overcommit
3. **Flexibility**: Allows customization of memory overhead per cluster
4. **Consistency**: Aligns with Kata Containers upstream implementation
5. **Backward Compatibility**: Existing deployments continue to work

## Risks and Mitigations

| Risk | Mitigation |
|------|------------|
| Webhook Performance | Efficient implementation and monitoring |
| Configuration Complexity | Sensible defaults and documentation |
| Security Concerns | Proper RBAC and validation |
| Upgrade Issues | Backward compatibility and gradual rollout |

## Conclusion

This proposal provides a comprehensive solution for adding memory overhead annotation support to the OpenShift Sandboxed Containers Operator. The implementation is designed to be secure, performant, and backward-compatible while providing the flexibility needed for different deployment scenarios.
