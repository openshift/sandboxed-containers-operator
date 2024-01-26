# Feature Gates

Feature Gates in the Openshift Sandboxed Containers operator allow
administrators to enable or disable specific features. These features are
controlled through the `FeatureGates` struct in the operator's configuration,
providing a flexible approach to testing new features and managing the rollout
of stable functionalities.

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

To enable a feature, you modify your `ConfigMap` object like so:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: osc-feature-gates
  namespace: openshift-sandboxed-containers-operator
data:
  timeTravel: "true"
  quantumEntanglementSync: "false"
  autoHealingWithAI: "true"
```

In this example, `timeTravel` is explicitly enabled, while
`quantumEntanglementSync` is disabled, and `autoHealingWithAI` is enabled,
showcasing how to manage the state of each feature individually. Regardless the
default values they have.
