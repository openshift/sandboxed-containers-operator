# Pod Overhead Annotation Samples

Samples for the memory overhead annotation feature. See OOM.md (or the memory
overhead documentation) for full context.

## Usage

**ConfigMap** — set memory overhead (default 350 if omitted):

```bash
oc apply -f osc-feature-gates-memory-overhead.yaml
```

**Example Pod** — Kata pod that receives the annotation:

```bash
oc apply -f example-kata-pod.yaml
oc get pod example-kata-pod -o jsonpath='{.metadata.annotations.io\.katacontainers\.config\.hypervisor\.memory_overhead}'
```

**Apply all**:

```bash
oc apply -k config/samples/pod-overhead-annotation
```
