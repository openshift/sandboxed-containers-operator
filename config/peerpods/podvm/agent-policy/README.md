# Kata Agent Policy

The Agent Policy feature in Kata Containers allows the Guest VM to perform additional validation on
each agent API request. You can change the default policies to a custom agent policy you provide
or to specify policy as a k8s annotation at runtime (if configured to be allowed).

## Specify Custom Policy To Be Used As Default

By default Openshift Sandboxed Containers set a preconfigured policy, Peer-Pods images will be set with an
allow-all policy, whereas CoCo images will be set with an allow-all exept for the `ReadStreamRequest` and
`ExecProcessRequest` calls.

### Set Custom Policy As Default At Image Creation Time

To set a default custom policy at image creation time, make sure to encode the policy file (e.g.,
[allow-all-except-exec-process.rego](allow-all-except-exec-process.rego)) in base64 format and set it as
the value for the AGENT_POLICY key in your `<azure/aws-podvm>-image-cm` ConfigMap.

```sh
ENCODED_POLICY=$(cat allow-all-except-exec-process.rego | base64 -w 0)
kubectl patch cm aws-podvm-image-cm -p "{\"data\":{\"AGENT_POLICY\":\"${ENCODED_POLICY}\"}}" -n openshift-sandboxed-containers-operator
```

**note:** InitData custom default policy will override policy that was set at image creation.

### Set Custom Policy As Default Using InitData

See [InitData documention](https://github.com/confidential-containers/cloud-api-adaptor/blob/main/src/cloud-api-adaptor/docs/initdata.md)

## Specify Policy At Runtime Through Pod Annotation

As long as the `SetPolicyRequest` call was not disabled by the default policy, users specify custom
policy through annotation at pod creation time. To set policy through annotation, encode your policy
file (e.g., [allow-all-except-exec-process.rego](allow-all-except-exec-process.rego)) in base64 format
and set it to the `io.katacontainers.config.agent.policy` annotation.

**note:** annotation policy will override any previous policy (as long as `SetPolicyRequest` is allowed)

```sh
ENCODED_POLICY=$(cat allow-all-except-exec-process.rego | base64 -w 0)
cat <<-EOF | kubectl apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: sleep
  annotations:
    io.containerd.cri.runtime-handler: kata-remote
    io.katacontainers.config.agent.policy: ${ENCODED_POLICY}
spec:
  runtimeClassName: kata-remote
  containers:
    - name: sleeping
      image: fedora
      command: ["sleep"]
      args: ["infinity"]
EOF
```

## Example Policies
- [allow-all.rego](allow-all.rego)
- [allow-all-except-exec-process.rego](allow-all-except-exec-process.rego)
