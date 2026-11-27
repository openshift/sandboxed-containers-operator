# RuntimeClass labels

Set `spec.runtimeClassLabels` on the cluster's KataConfig to manage metadata
labels on RuntimeClasses owned by that KataConfig.

`defaults` applies to all owned classes, including classes added in later
operator releases. `overrides` uses exact RuntimeClass names. An override takes
precedence when both maps contain the same label key. It can also add labels.
It cannot remove a default label from one class. Use only overrides when a
label should apply to a subset of classes.

## Examples

Each example is independent. Merge its `runtimeClassLabels` section into the
`spec` of your existing KataConfig, keeping the other fields you already use.

### Apply the same label to every class

Use `defaults` for labels that belong on every owned RuntimeClass, such as a
team label used for inventory:

```yaml
spec:
  runtimeClassLabels:
    defaults:
      example.com/team: sandbox-platform
```

This labels `kata`, `kata-nvidia-gpu`, `kata-cc`, `kata-cc-nvidia-gpu`, and
`kata-remote` when those classes exist. A class added in a future operator
release also receives the team label.

### Approve only selected classes for an application

Use `overrides` without `defaults` when an application should accept only a
chosen set of classes. For an application that requires
`example.com/sandbox-approved: "true"`, approve two classes by name:

```yaml
spec:
  runtimeClassLabels:
    overrides:
      kata:
        example.com/sandbox-approved: "true"
      kata-nvidia-gpu:
        example.com/sandbox-approved: "true"
```

Only these two classes receive the approval label from this configuration.
Other classes, including future classes, need their own entry before this
configuration approves them. An entry does not enable the runtime or create
the class. The application still selects a class by name; the label lets it
check whether that class is approved.

When switching from a shared approval label to this configuration, remove
that label from `defaults`. Leaving it there still approves every owned class.

### Change a default value and add a label for one class

Combine both maps when classes share labels but one needs different values
or extra metadata:

```yaml
spec:
  runtimeClassLabels:
    defaults:
      example.com/team: sandbox-platform
      example.com/workload-type: general
    overrides:
      kata-nvidia-gpu:
        example.com/workload-type: gpu
        example.com/device: nvidia
```

Every owned class keeps the shared team label. For the keys configured here,
the resulting labels are:

| RuntimeClass | `example.com/workload-type` | `example.com/device` |
| --- | --- | --- |
| `kata-nvidia-gpu` | `gpu` | `nvidia` |
| All other owned classes, including future classes | `general` | Not set by this configuration |

Removing the `workload-type` override makes `kata-nvidia-gpu` fall back to
`general`. Removing the `device` entry removes that label, because no default
supplies it. An empty override map inherits every default.

To leave a label absent on some classes, use the selected-classes example
instead of a default. An override value of `"false"` or `""` still writes a
label with that value; neither removes the key.

These labels describe the classes. They do not configure GPU support or
change node selection.

## Lifecycle and ownership

The option does not enable runtimes or create classes named in overrides.
Existing classes receive changes even when no nodes currently pass eligibility
checks. New classes carry the labels in their creation request. If the operator
recreates a class, it applies the current configuration again. This works with
both MachineConfig and DaemonSet deployment modes.

The operator repairs changes to configured keys. Removing a key from the
configuration removes that label from each owned class, unless a default or
override still supplies it. Removing `runtimeClassLabels` removes all labels
previously managed through this option. Other labels and annotations remain.
If a configured key already exists, the operator takes ownership of its value;
removing the key later deletes it rather than restoring the old value.

The operator records managed keys in the annotation
`kataconfiguration.openshift.io/managed-runtimeclass-labels`. Do not edit that
annotation. A malformed value blocks label reconciliation and is reported in
the operator logs. Label management skips classes being deleted and stops when
the KataConfig is being deleted.

Label keys and values must follow Kubernetes label rules. Override names must
be valid RuntimeClass names. Invalid values are rejected on both creation and
update of the KataConfig.

Labels do not choose a RuntimeClass or change scheduling. A workload still
selects its class through `spec.runtimeClassName`. Applications can inspect
labels to decide whether to permit that class. Use defaults only when that
permission should apply to every owned class, including future classes.

When managing KataConfig through GitOps, put this configuration in its desired
manifest. Give each label key one owner. A second controller writing different
values to the same RuntimeClass keys will cause repeated updates.
