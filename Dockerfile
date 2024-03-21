# Use OpenShift golang builder image
# These images needs to be synced with the images in the Makefile.
ARG BUILDER_IMAGE=${BUILDER_IMAGE:-registry.ci.openshift.org/ocp/builder:rhel-9-golang-1.21-openshift-4.16}
ARG TARGET_IMAGE=${TARGET_IMAGE:-registry.ci.openshift.org/ocp/4.16:base}
FROM ${BUILDER_IMAGE} AS builder

WORKDIR /workspace

COPY Makefile Makefile
COPY hack hack/
COPY PROJECT PROJECT
COPY go.mod go.mod
COPY go.sum go.sum
COPY main.go main.go
COPY api api/
COPY config config/
COPY controllers controllers/
COPY internal internal/

RUN go mod download
# needed for docker build but not for local builds
RUN go mod vendor

RUN make build

# Use OpenShift base image
FROM ${TARGET_IMAGE}
WORKDIR /
COPY --from=builder /workspace/bin/manager .
COPY --from=builder /workspace/config/peerpods /config/peerpods

RUN useradd  -r -u 499 nonroot
RUN getent group nonroot || groupadd -o -g 499 nonroot

USER 499:499
ENTRYPOINT ["/manager"]
