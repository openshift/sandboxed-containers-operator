# Configuration Options

> [!WARNING]
> **Historical Context**: In the past, we used to call this "Feature Gates". You
> will see some internal structures still using that name, such as
> `osc-feature-gates` ConfigMap and `FeatureGateStatus` struct. However, we are
> updating our terminology and now calling this simply "Configuration Options",
> because that is what this system actually provides.

> **Future Direction**: Ideally, most of the configuration options should be
> migrated to the `KataConfig` Custom Resource. Our goal is to update these
> structures in future releases to provide a more centralized configuration place.

Configuration Options in the Openshift Sandboxed Containers operator allow
administrators to configure various aspects of the operator's behavior. These options
are controlled through the configuration ConfigMap (`osc-feature-gates`),
providing a flexible approach to customizing the operator's functionality.


## Design choices

The configuration options are handled within the KataConfig reconcile loop.  If there
are any errors with configuration processing, the reconciliation process
continues and doesn't re-queue.  In case of any errors in determining whether a
configuration is enabled or not (for example if the configMap is deleted or some
other API server errors), the processing of the configuration is same as the
default compiled in state of the respective configuration.

The status of individual configurations is stored in the `FeatureGateStatus`
struct that is populated in the beginning of the reconcile loop with the status
of the configurations from the configMap. This ensures that the entire configuration
configMap is only read once from the API server instead of making repeated
calls to the API server for checking individual configurations.
Any errors in reading the configMap from the API server will requeue a reconciliation request,
except for when configMap is not found.

## Feature Maturity Levels

> [!NOTE]
> **Maturity levels apply specifically to features, not all configuration options.**
> While most configuration options are simple settings, features that can be enabled/disabled
> follow maturity level guidelines.

Our features that can be enabled/disabled adhere to simplified lifecycle stages inspired by Kubernetes:

- **DevPreview**:
  - Disabled by default.
  - May contain bugs; enabling the feature could expose these bugs.
  - No long-term support guarantees.
  - Not recommended for production use.

- **TechPreview**:
  - Usually disabled by default.
  - Support for the feature will not be dropped, but details may change.
  - Recommended for non-business-critical usage due to potential for changes.

- **GA (General Availability)**:
  - May be enabled or disabled by default depending on the specific feature.
  - Well-tested and considered safe.
  - Stable features will be maintained in future software releases.
  - Default state is not determined by maturity level alone.

## Important Note on Default Values

> [!IMPORTANT]
> **Default values are not automatically determined by maturity level.** Even GA features
> may be disabled by default. Users must explicitly check the documentation for each
> feature to understand:
> - The current default state
> - The maturity level
> - What is supported and what is not
> - Whether the feature is DevPreview, TechPreview, or GA

## Disclaimer

> [!WARNING]
> Remember, the availability and default state of each feature may change between
> releases as features progress through their maturity levels. Always refer to
> the latest documentation for up-to-date information on feature support and
> configuration.

## Configuring Options

Configuration options can be set by editing the `osc-feature-gates` ConfigMap
resource. Each configuration allows you to customize specific aspects of the operator's
behavior, from enabling features to setting operational parameters.

### Enabling and disabling features

For feature toggles, a feature is enabled by explicitly setting its value to `"true"` (case sensitive)
and disabled by setting its value to `"false"` (case sensitive)

To enable a feature, you modify the `ConfigMap` object like so:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: osc-feature-gates
  namespace: openshift-sandboxed-containers-operator
data:
  confidential: "true"
```

In this example, `confidential` is explicitly enabled,
showcasing how to manage the state of each feature individually. Users should
consult the documentation to understand the default state and maturity level
of each feature before enabling it.

### Other configuration options

For non-feature configuration options (such as image specifications, network settings, etc.),
the values and format depend on the specific option. Refer to the individual
documentation for each configuration option to understand the expected format
and valid values.
