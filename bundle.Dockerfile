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
COPY bundle/manifests /manifests/
COPY bundle/metadata /metadata/
COPY bundle/tests/scorecard /tests/scorecard/

# Red Hat labels
LABEL io.k8s.display-name='OpenShift sandboxed containers operator'
LABEL io.k8s.description='This operator manages the sandboxed-containers runtime'
LABEL com.redhat.delivery.appregistry=''
LABEL maintainer='support@redhat.com'
LABEL name="openshift/sandboxed-containers-operator-bundle"
LABEL com.redhat.component="osc-operator-bundle-container"
LABEL io.openshift.maintainer.product='OpenShift Container Platform'
LABEL io.openshift.maintainer.component='Sandboxed Containers'
LABEL version=1.10.0
LABEL com.redhat.delivery.operator.bundle=true
LABEL com.redhat.openshift.versions=v4.15
LABEL summary="This operator manages the sandboxed-containers runtime"
LABEL description="This operator manages the sandboxed-containers runtime"
LABEL io.openshift.tags=""
LABEL distribution-scope=public
LABEL release="1"
LABEL url="https://access.redhat.com/"
LABEL vendor="Red Hat, Inc."
