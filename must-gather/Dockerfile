FROM registry.redhat.io/openshift4/ose-must-gather:latest as builder

FROM registry.access.redhat.com/ubi9/ubi-minimal:9.6-1750782676

# For gathering data from nodes
# NOTE for hermetic build: any change to the packages installed here must
# be reflected in the rpms.in.yaml file.
# Also make sure to re-generate the rpms.lock.yaml file when doing so.
# See https://konflux.pages.redhat.com/docs/users/building/prefetching-dependencies.html#rpm
RUN microdnf update -y && microdnf install tar rsync -y && microdnf clean all

COPY --from=builder /usr/bin/oc /usr/bin/oc

# Save original gather script
COPY --from=builder /usr/bin/gather /usr/bin/gather_original

# Copy all collection scripts to /usr/bin
COPY collection-scripts/* /usr/bin/

# Copy node-gather resources to /etc
COPY node-gather/node-gather-crd.yaml /etc/
COPY node-gather/node-gather-ds.yaml /etc/

# Red Hat labels
LABEL name="openshift-sandboxed-containers-operator-must-gather" \
version="1.10.0" \
com.redhat.component="osc-must-gather-container" \
summary="osc-must-gather collects information about the sandboxed containers operator and the kata runtime" \
maintainer="support@redhat.com" \
description="osc-must-gather collects information about the sandboxed containers operator and the kata runtime" \
io.k8s.display-name="openshift-sandboxed-containers-must-gather" \
io.k8s.description="osc-must-gather collects information about the sandboxed containers operator and the kata runtime" \
io.openshift.tags=""

ENTRYPOINT /usr/bin/gather
