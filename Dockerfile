# Use OpenShift golang builder image
FROM registry.ci.openshift.org/ocp/builder:rhel-8-golang-1.17-openshift-4.10 AS builder

WORKDIR /workspace

# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY main.go main.go
COPY api/ api/
COPY controllers/ controllers/
COPY Makefile Makefile
COPY hack/ hack/
RUN go mod vendor

# Build
RUN make build

# Use OpenShift base image
FROM registry.ci.openshift.org/ocp/4.10:base
WORKDIR /
COPY --from=builder /workspace/bin/manager .

RUN useradd  -r -u 499 nonroot
RUN getent group nonroot || groupadd -o -g 499 nonroot

USER nonroot:nonroot
ENTRYPOINT ["/manager"]
