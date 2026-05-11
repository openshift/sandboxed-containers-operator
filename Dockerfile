FROM registry.access.redhat.com/ubi9/go-toolset:1.25.9-1778171507 as builder

# Required by the ubi based go-toolset image
USER root

WORKDIR /workspace

COPY Makefile Makefile
COPY hack hack/
COPY PROJECT PROJECT
COPY go.mod go.mod
COPY go.sum go.sum
COPY cmd/ cmd/
COPY api api/
COPY config config/
COPY controllers controllers/

# Copy our controller-gen script to work around hermetic build issues
# See comments in the script itself for more details.
COPY controller-gen bin/

# get the version of controller-gen in an env variable for reusing
RUN echo "export CONTROLLER_TOOLS_VERSION=$(grep -m 1 controller-tools go.mod | awk '{print $2}')" > controller-tools-ver

# rename the script to use the same version as defined in our go.mod file
RUN . ./controller-tools-ver && mv bin/controller-gen bin/controller-gen-$CONTROLLER_TOOLS_VERSION

# make sure 'make' uses the right version of controller-gen
RUN . ./controller-tools-ver && make build

# Use OpenShift base image
FROM registry.access.redhat.com/ubi9/ubi-minimal:9.7-1778072020
WORKDIR /
COPY --from=builder /workspace/bin/manager .
COPY --from=builder /workspace/bin/metrics-server .
COPY --from=builder /workspace/config/peerpods /config/peerpods

RUN useradd  -r -u 499 nonroot
RUN getent group nonroot || groupadd -o -g 499 nonroot

# Red Hat labels
LABEL name="openshift-sandboxed-containers/osc-rhel9-operator" \
cpe="cpe:/a:redhat:confidential_compute_attestation:1.12::el9" \
version="1.12" \
com.redhat.component="osc-operator-container" \
summary="This operator manages the Openshift Sandboxed Containers runtime installation" \
maintainer="redhat@redhat.com" \
description="The Openshift Sandboxed containers operator manages runtime configuration and lifecycle" \
io.k8s.display-name="openshift-sandboxed-containers-operator" \
io.k8s.description="This operator manages the Openshift Sandboxed Containers runtime installation" \
io.openshift.tags=""

USER 499:499
ENTRYPOINT ["/manager"]
