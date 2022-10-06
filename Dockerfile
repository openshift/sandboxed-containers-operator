# Use OpenShift golang builder image
FROM registry.ci.openshift.org/ocp/builder:rhel-8-golang-1.17-openshift-4.10 AS builder

WORKDIR /workspace

COPY Makefile Makefile
COPY hack hack/
COPY PROJECT PROJECT
COPY main.go main.go
COPY api api/
COPY config config/
COPY controllers controllers/
COPY go.mod go.mod
COPY go.sum go.sum

RUN go mod download
# needed for docker build but not for local builds
RUN go mod vendor

RUN make build

# Use OpenShift base image
FROM registry.ci.openshift.org/ocp/4.10:base
WORKDIR /
COPY --from=builder /workspace/bin/manager .

RUN useradd  -r -u 499 nonroot
RUN getent group nonroot || groupadd -o -g 499 nonroot

USER nonroot:nonroot
ENTRYPOINT ["/manager"]
