## Release notes

Following are the prerequisites
- OCP release must be 4.18
- At least one baremetal node supporting either AMD SNP or Intel TDX

## Disconnected installs

For disconnected setup, you'll need to mirror the following by creating
an `ImageSetConfiguration`

## operator packages

- sandboxed-containers-operator
- nfd
- trustee-operator


## Additional images

- quay.io/openshift_sandboxed_containers/rhcos-layer/ocp-4.18:snp-0.2.0
- quay.io/openshift_sandboxed_containers/rhcos-layer/ocp-4.18:tdx-0.2.0
- registry.redhat.io/ubi9/ubi:latest
- registry.redhat.io/openshift4/ose-node-feature-discovery-rhel9:v4.18
