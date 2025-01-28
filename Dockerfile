FROM registry.access.redhat.com/ubi9/go-toolset:1.22.9-1738746453 AS builder

USER root
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

RUN go mod download
# needed for docker build but not for local builds
RUN go mod vendor

RUN make build

# Use OpenShift base image
FROM registry.access.redhat.com/ubi9/ubi-minimal:9.5-1736404155
WORKDIR /
COPY --from=builder /workspace/bin/manager .
COPY --from=builder /workspace/bin/metrics-server .
COPY --from=builder /workspace/config/peerpods /config/peerpods

RUN useradd  -r -u 499 nonroot
RUN getent group nonroot || groupadd -o -g 499 nonroot

USER 499:499
ENTRYPOINT ["/manager"]
