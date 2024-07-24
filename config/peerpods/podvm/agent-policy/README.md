# Kata Agent Policy

Agent Policy is a Kata Containers feature that enables the Guest VM to perform additional validation
for each agent API request. A custom agent policy can be set either by a policy file provided at
image creation time or through pod annotations.

## Set Policy At Image Creation

By default Openshift Sandboxed Container sets preconfigured policy, Peer-Pods images will be set with an
allow-all policy while CoCo images will be set with an allow-all exept for the `ReadStreamRequest` and
`ExecProcessRequest` calls.

To set custom policy at image creation time, make sure to encode the policy file (e.g.,
[allow-all-except-exec-process.rego](allow-all-except-exec-process.rego)) in base64 format and set it as
the value for the AGENT_POLICY key in your `<azure/aws-podvm>-image-cm` ConfigMap.

```sh
ENCODED_POLICY=$(cat allow-all-except-exec-process.rego | base64 -w 0)
kubectl patch cm aws-podvm-image-cm -p "{\"data\":{\"AGENT_POLICY\":\"${ENCODED_POLICY}\"}}" -n openshift-sandboxed-containers-operator
```

## Set Policy Via Pod Annotation

As long as the `SetPolicyRequest` call was not disabled at image creation time, users set custom
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
