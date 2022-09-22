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
COPY api/ api/
#COPY controllers/ controllers/

COPY Makefile Makefile
COPY hack/ hack/
RUN go mod vendor
COPY PROJECT PROJECT
COPY config/ config/

RUN make generate
RUN make manifests
RUN make kustomize

RUN export ARCH=$(case $(uname -m) in x86_64) echo -n amd64 ;; aarch64) echo -n arm64 ;; *) echo -n $(uname -m) ;; esac) \
OS=$(uname | awk '{print tolower($0)}') \
OPERATOR_SDK_DL_URL=https://github.com/operator-framework/operator-sdk/releases/download/v1.23.0; \
curl -LO ${OPERATOR_SDK_DL_URL}/operator-sdk_${OS}_${ARCH}; \
mv operator-sdk_${OS}_${ARCH} operator-sdk; \
chmod +x operator-sdk

RUN ./operator-sdk generate kustomize manifests -q
RUN cd config/manager && ../../bin/kustomize edit set image controller=controller:latest
RUN bin/kustomize build config/manifests | ./operator-sdk generate bundle -q --version 1.3.0  


FROM scratch

# Core bundle labels.
LABEL operators.operatorframework.io.bundle.mediatype.v1=registry+v1
LABEL operators.operatorframework.io.bundle.manifests.v1=manifests/
LABEL operators.operatorframework.io.bundle.metadata.v1=metadata/
LABEL operators.operatorframework.io.bundle.package.v1=sandboxed-containers-operator
LABEL operators.operatorframework.io.bundle.channels.v1=alpha
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
