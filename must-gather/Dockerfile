FROM quay.io/openshift/origin-must-gather:latest as builder

FROM registry.access.redhat.com/ubi9/ubi-minimal:9.5-1741850109

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

ENTRYPOINT /usr/bin/gather
