# Feature Gates

Feature Gates in the Openshift Sandboxed Containers operator allow
administrators to enable or disable specific features. These features are
controlled through the feature gates configmap (`osc-feature-gates`),
providing a flexible approach to testing new features and managing the rollout
of stable functionalities.

## Design choices

The feature gates are handled within the KataConfig reconcile loop.  If there
are any errors with feature gate processing, the reconciliation process
continues and doesn't re-queue.  In case of any errors in determining whether a
feature gate is enabled or not (for example if the configMap is deleted or some
other API server errors), the processing of the  feature gate is same as the
default compiled in state of the respective feature gate.

The status of individual feature gates is stored in the `FeatureGateStatus`
struct that is populated in the beginning of the reconcile loop with the status
of the feature gates from the  configMap. This ensures that the entire feature
gate configMap is only read once from the API server instead of making repeated
calls to the API server for checking individual feature gates.
Any errors in reading the configMap from the API server will requeue a reconciliation request,
except for when configMap is not found.

## Maturity Levels

Our feature gates adhere to simplified lifecycle stages inspired by Kubernetes:

- **DevPreview**:
  - Disabled by default.
  - May contain bugs; enabling the feature could expose these bugs.
  - No long-term support guarantees.

- **TechPreview**:
  - Usually disabled by default.
  - Support for the feature will not be dropped, but details may change.
  - Recommended for non-business-critical usage due to potential for changes.

- **GA (General Availability)**:
  - Enabled by default
  - Well-tested and considered safe.
  - Stable features will be maintained in future software releases.

## Disclaimer

> [!WARNING]
> Remember, the availability and default state of each feature may change between
> releases as features progress through their maturity levels. Always refer to
> the latest documentation for up-to-date information on feature support and
> configuration.

## Configuring Feature Gates

Feature gates can be enabled or disabled by editing the `osc-feature-gates` ConfigMap
resource. Each feature gate allows you to toggle specific functionalities,
adjusting your cluster's behavior as needed.

### Enabling and disabling features

A feature is enabled by explicitly setting it's value to `"true"` (case sensitive)
and disabled by setting it's value to `"false"` (case sensitive)

To enable a feature, you modify the `ConfigMap` object like so:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: osc-feature-gates
  namespace: openshift-sandboxed-containers-operator
data:
  timeTravel: "true"
```

In this example, `timeTravel` is explicitly enabled,
showcasing how to manage the state of each feature individually. Regardless the
default values they have.
