
# Enforcing Security in OpenShift with Gatekeeper

In Kubernetes, ensuring security and compliance is essential, especially when
dealing with privileged pods. OpenShift Gatekeeper allows us to enforce
policies that ensure all privileged pods use a specific `RuntimeClass`. Here’s
a simple guide on how to set this up.

## Prerequisites

For this guide, we assume that OpenShift Gatekeeper is already installed in your cluster.

## Defining and Applying the Constraint Template

First, we’ll define a `ConstraintTemplate` to enforce our policy.

### Create the Constraint Template

The `constrainttemplate.yaml` file is located in the
`config/samples/gatekeeper` directory with the following content:

```yaml
apiVersion: templates.gatekeeper.sh/v1beta1
kind: ConstraintTemplate
metadata:
  name: k8srequirekata
spec:
  crd:
    spec:
      names:
        kind: K8sRequireKata
  targets:
    - target: admission.k8s.gatekeeper.sh
      rego: |
        package k8srequirekata

        violation[{"msg": msg}] {
          input.review.kind.kind == "Pod"
          elevated_privileges(input.review.object.spec.containers)
          not has_kata(input.review.object.spec)
          msg := "Pods requiring elevated privileges must specify spec.runtimeClassName: kata"
        }

        elevated_privileges(containers) {
          some i
          containers[i].securityContext.privileged == true
        }

        has_kata(spec) {
          spec.runtimeClassName == "kata"
        }
```

### Apply the Constraint Template

Apply the template:

```sh
oc apply -f config/samples/gatekeeper/constrainttemplate.yaml
```

## Creating and Applying the Constraint

Next, create a `Constraint` that uses the template and restricts it to a specific namespace.

### Create the Constraint

The `constraint.yaml` file is located in the `config/samples/gatekeeper`
directory with the following content:

```yaml
apiVersion: constraints.gatekeeper.sh/v1beta1
kind: K8sRequireKata
metadata:
  name: require-kata-for-privileged-pods
spec:
  match:
    kinds:
      - apiGroups: [""]
        kinds: ["Pod"]
    namespaces:
      - my-restricted-namespace
```

### Apply the Constraint

Apply the constraint:

```sh
oc apply -f config/samples/gatekeeper/constraint.yaml
```

## Defining a Pod to Test the Constraint

Define a pod in the `my-restricted-namespace` that requires elevated
privileges. This pod should have the `runtimeClassName` set to `kata` to comply
with the constraint.

### Create the Pod Manifest

The `pod.yaml` file is located in the `config/samples/gatekeeper` directory
with the following content:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: privileged-pod
  namespace: my-restricted-namespace
spec:
  runtimeClassName: kata
  containers:
    - name: fedora 
      image: quay.io/fedora/fedora:latest 
      securityContext:
        privileged: true
        allowPrivilegeEscalation: true
        capabilities:
          drop:
            - ALL
        runAsNonRoot: true
```

### Apply the Pod Manifest

Apply the pod manifest:

```sh
oc apply -f config/samples/gatekeeper/pod.yaml
```

## Viewing Compliance

When the policy is in place and applied to your clusters, you can verify the
compliance status of your managed clusters.

### Check Policy Compliance

Use `oc get` commands to verify the applied policies and their statuses. For
example, `oc get constraints` will list all the constraints and their statuses.

### Detailed Policy Report

Use `oc describe` to get detailed information about specific constraints and
their enforcement. For example, `oc describe constraint k8srequirekata` will
show detailed information about the enforcement status and any violations.

### Non-Compliant Resources

For non-compliant resources, the detailed policy report will indicate which
pods are not complying with the policy and why. This includes details of the
pods that do not have `runtimeClassName: kata` set, despite requiring elevated
privileges.

### Audit Logs

Review audit logs to track the history of compliance for each cluster and
understand any deviations. This helps in identifying when and how the policy
was applied and any changes made to the compliance status over time.

### Example Commands

- **List Constraints:**
  ```sh
  oc get constraints
  ```

- **Describe a Constraint:**
  ```sh
  oc describe constraint k8srequirekata
  ```

- **View Audit Logs:**
  ```sh
  oc logs -l gatekeeper.sh/audit
  ```

This detailed visibility helps admins ensure that security policies are
consistently enforced across their Kubernetes environments, and any deviations
can be quickly identified and rectified.

## Conclusion

Using OpenShift Gatekeeper, you can centrally manage and enforce security
policies across your Kubernetes clusters. By following these steps, you ensure
that privileged pods in your specified namespace use the `kata` runtime class,
enhancing the security and compliance of your Kubernetes environment. This
setup helps you maintain control over your cluster policies effectively and
efficiently.

By leveraging the built-in tools and commands of OpenShift, you gain
comprehensive insights into policy compliance across your clusters, enabling
proactive management of security policies. This visibility is crucial for
maintaining a secure and compliant Kubernetes ecosystem, especially in complex,
multi-cluster environments.
