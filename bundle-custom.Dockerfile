# Use OpenShift golang builder image
FROM registry.ci.openshift.org/ocp/builder:rhel-9-golang-1.21-openshift-4.16 AS builder

WORKDIR /workspace

COPY Makefile Makefile
COPY PROJECT PROJECT
COPY go.mod go.mod
COPY go.sum go.sum
COPY api api/
COPY config config/
COPY controllers controllers/
COPY internal internal/

RUN go mod download
# needed for docker build but not for local builds
RUN go mod vendor

# Install operator-sdk
RUN export ARCH=$(case $(uname -m) in x86_64) echo -n amd64 ;; aarch64) echo -n arm64 ;; *) echo -n $(uname -m) ;; esac) \
OS=$(uname | awk '{print tolower($0)}') \
OPERATOR_SDK_DL_URL=https://github.com/operator-framework/operator-sdk/releases/download/v1.25.3; \
curl -LO ${OPERATOR_SDK_DL_URL}/operator-sdk_${OS}_${ARCH}; \
mv operator-sdk_${OS}_${ARCH} operator-sdk; \
chmod +x operator-sdk

# Set path to include local dir so standard make target can be used
ENV PATH=$PATH:.

# Unsetting VERSION here is workaround because the buildroot image sets VERSION to the golang version
RUN unset VERSION; GOFLAGS="" make bundle IMAGE_TAG_BASE=proxy.engineering.redhat.com/rh-osbs/openshift-sandboxed-containers-operator

FROM scratch

# Core bundle labels.
LABEL operators.operatorframework.io.bundle.mediatype.v1=registry+v1
LABEL operators.operatorframework.io.bundle.manifests.v1=manifests/
LABEL operators.operatorframework.io.bundle.metadata.v1=metadata/
LABEL operators.operatorframework.io.bundle.package.v1=sandboxed-containers-operator
LABEL operators.operatorframework.io.bundle.channels.v1=stable
LABEL operators.operatorframework.io.bundle.channel.default.v1=stable
LABEL operators.operatorframework.io.metrics.builder=operator-sdk-v1.19.0+git
LABEL operators.operatorframework.io.metrics.mediatype.v1=metrics+v1
LABEL operators.operatorframework.io.metrics.project_layout=go.kubebuilder.io/v3

# Labels for testing.
LABEL operators.operatorframework.io.test.mediatype.v1=scorecard+v1
LABEL operators.operatorframework.io.test.config.v1=tests/scorecard/

# Copy files to locations specified by labels.
COPY --from=builder /workspace/bundle/manifests /manifests/
COPY --from=builder /workspace/bundle/metadata /metadata/
COPY --from=builder /workspace/bundle/tests/scorecard /tests/scorecard/
