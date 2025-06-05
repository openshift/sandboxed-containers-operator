FROM brew.registry.redhat.io/rh-osbs/openshift-golang-builder:rhel_9_golang_1.23 AS builder

WORKDIR /workspace

COPY Makefile Makefile
COPY hack hack/
COPY PROJECT PROJECT
COPY go.mod go.mod
COPY go.sum go.sum
COPY main.go main.go
COPY cmd/metrics cmd/metrics/
COPY api api/
COPY config config/
COPY controllers controllers/

# Copy our controller-gen script to work around hermetic build issues
# See comments in the script itself for more details.
COPY controller-gen bin/

# get the version of controller-gen in an env variable for reusing
RUN echo "export CONTROLLER_TOOLS_VERSION=$(grep controller-tools go.mod | awk '{print $2}')" > controller-tools-ver

# rename the script to use the same version as defined in our go.mod file
RUN . ./controller-tools-ver && mv bin/controller-gen bin/controller-gen-$CONTROLLER_TOOLS_VERSION

# make sure 'make' uses the right version of controller-gen
RUN . ./controller-tools-ver && make build

# Use OpenShift base image
FROM registry.access.redhat.com/ubi9/ubi-minimal:9.5-1741850109
WORKDIR /
COPY --from=builder /workspace/bin/manager .
COPY --from=builder /workspace/bin/metrics-server .
COPY --from=builder /workspace/config/peerpods /config/peerpods

RUN useradd  -r -u 499 nonroot
RUN getent group nonroot || groupadd -o -g 499 nonroot

# Red Hat labels
LABEL name="openshift-sandboxed-containers-operator" \
version="1.10.0" \
com.redhat.component="osc-operator-container" \
summary="This operator manages the Openshift Sandboxed Containers runtime installation" \
maintainer="redhat@redhat.com" \
description="The Openshift Sandboxed containers operator manages runtime configuration and lifecycle" \
io.k8s.display-name="openshift-sandboxed-containers-operator" \
io.k8s.description="This operator manages the Openshift Sandboxed Containers runtime installation" \
io.openshift.tags=""

USER 499:499
ENTRYPOINT ["/manager"]
