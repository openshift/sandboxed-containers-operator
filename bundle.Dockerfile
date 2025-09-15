FROM registry.access.redhat.com/ubi9/go-toolset:9.6-1756993846 as builder

# Required by the ubi based go-toolset image
USER root

WORKDIR /workspace

COPY Makefile Makefile
COPY PROJECT PROJECT
COPY go.mod go.mod
COPY go.sum go.sum
COPY api api/
COPY cmd cmd/
COPY config config/
COPY controllers controllers/

# Copy our controller-gen script to work around hermetic build issues
# See comments in the script itself for more details.
COPY controller-gen bin/

# get the version of controller-gen in an env variable for reusing
RUN echo "export CONTROLLER_TOOLS_VERSION=$(grep controller-tools go.mod | awk '{print $2}')" > make-bundle.env

# rename the script to use the same version as defined in our go.mod file
RUN . ./make-bundle.env && mv bin/controller-gen bin/controller-gen-$CONTROLLER_TOOLS_VERSION

# Copy our kustomize script to work around hermetic build issues
COPY kustomize bin/

# get the version of kustomize in an env variable for reusing
RUN echo "export KUSTOMIZE_VERSION=$(grep kustomize/kustomize go.mod | awk '{print $2}')" >> make-bundle.env

# rename the script to use the same version as defined in our go.mod file
RUN . ./make-bundle.env && mv bin/kustomize bin/kustomize-$KUSTOMIZE_VERSION

# copy prefetched operator-sdk
RUN sdk=/cachi2/output/deps/generic/operator-sdk && if [ -f "${sdk}" ]; then cp "${sdk}" bin/ && chmod +x bin/operator-sdk; fi

# copy our yq script to work around hermetic build issues (no specific version needed)
COPY yq bin/

# save the current version of the operator image in an env variable for reusing
RUN echo "export IMG=$(./bin/yq 'select(document_index == 1).spec.template.spec.containers[] | select(.name == "manager").env[] | select(.name == "RELATED_IMAGE_OPERATOR").value' config/manager/manager.yaml)" >> make-bundle.env

# make sure 'make bundle' uses the right version of controller-gen
# NOTE: the go-toolset image exposes the version of go in VERSION. Unset
#       it to avoid the makefile picking it up for the operator version
RUN . ./make-bundle.env && unset VERSION && make bundle

FROM scratch

# Core bundle labels.
LABEL operators.operatorframework.io.bundle.mediatype.v1=registry+v1
LABEL operators.operatorframework.io.bundle.manifests.v1=manifests/
LABEL operators.operatorframework.io.bundle.metadata.v1=metadata/
LABEL operators.operatorframework.io.bundle.package.v1=sandboxed-containers-operator
LABEL operators.operatorframework.io.bundle.channels.v1=stable
LABEL operators.operatorframework.io.bundle.channel.default.v1=stable
LABEL operators.operatorframework.io.metrics.builder=operator-sdk-v1.39.1
LABEL operators.operatorframework.io.metrics.mediatype.v1=metrics+v1
LABEL operators.operatorframework.io.metrics.project_layout=go.kubebuilder.io/v4

# Labels for testing.
LABEL operators.operatorframework.io.test.mediatype.v1=scorecard+v1
LABEL operators.operatorframework.io.test.config.v1=tests/scorecard/

# Copy files to locations specified by labels.
COPY --from=builder /workspace/bundle/manifests /manifests/
COPY --from=builder /workspace/bundle/metadata /metadata/
COPY --from=builder /workspace/bundle/tests/scorecard /tests/scorecard/

# Red Hat labels
LABEL io.k8s.display-name='OpenShift sandboxed containers operator'
LABEL io.k8s.description='This operator manages the sandboxed-containers runtime'
LABEL com.redhat.delivery.appregistry=''
LABEL maintainer='support@redhat.com'
LABEL name="openshift-sandboxed-containers/osc-operator-bundle"
LABEL cpe="cpe:/a:redhat:confidential_compute_attestation:1.10::el9"
LABEL com.redhat.component="osc-operator-bundle-container"
LABEL io.openshift.maintainer.product='OpenShift Container Platform'
LABEL io.openshift.maintainer.component='Sandboxed Containers'
LABEL version=1.10.1
LABEL com.redhat.delivery.operator.bundle=true
LABEL com.redhat.openshift.versions=v4.15
LABEL summary="This operator manages the sandboxed-containers runtime"
LABEL description="This operator manages the sandboxed-containers runtime"
LABEL io.openshift.tags=""
LABEL distribution-scope=public
LABEL release="1"
LABEL url="https://access.redhat.com/"
LABEL vendor="Red Hat, Inc."
