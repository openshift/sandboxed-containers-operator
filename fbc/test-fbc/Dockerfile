# The builder image is expected to contain
# /bin/opm (with serve subcommand)
FROM brew.registry.redhat.io/rh-osbs/openshift-ose-operator-registry-rhel9:v4.19 as builder
ARG GIT_URL=""
ARG GIT_REVISION=""
USER root

WORKDIR /workdir
ADD render.sh icon.png ./
COPY test-fbc ./test-fbc

RUN dnf install -y go git jq skopeo && go install github.com/mikefarah/yq/v4@v4.35.1
RUN PATH=/root/go/bin:$PATH ./render.sh test-fbc

# Copy FBC root into image at /configs and pre-populate serve cache
RUN cp -r test-fbc/catalog/ /configs
RUN ["/bin/opm", "serve", "/configs", "--cache-dir=/tmp/cache", "--cache-only"]

# Optionally clone the git repo and generate changelog from it
ENV GIT_URL=${GIT_URL}
ENV GIT_REVISION=${GIT_REVISION}
RUN if [[ -n "${GIT_URL}" && -n "${GIT_REVISION}" ]]; then \
        echo GIT_URL="${GIT_URL}" ; \
        echo GIT_REVISION="${GIT_REVISION}" ; \
        git clone "${GIT_URL}" repo && \
        pushd repo && \
        git checkout "${GIT_REVISION}" && \
        cd fbc/test-fbc && \
        ./changelog.sh 2>&1 | tee CHANGELOG.err || true && \
        mv CHANGELOG CHANGELOG.err /workdir/ && \
        popd ; \
    fi
# Create empty changelog files if they do not exist
RUN touch CHANGELOG && touch CHANGELOG.err

FROM brew.registry.redhat.io/rh-osbs/openshift-ose-operator-registry-rhel9:v4.19
# The base image is expected to contain
# /bin/opm (with serve subcommand) and /bin/grpc_health_probe

# Configure the entrypoint and command
ENTRYPOINT ["/bin/opm"]
CMD ["serve", "/configs", "--cache-dir=/tmp/cache"]

COPY --from=builder /configs /configs
COPY --from=builder /tmp/cache /tmp/cache
COPY --from=builder /workdir/CHANGELOG /CHANGELOG
COPY --from=builder /workdir/CHANGELOG.err /CHANGELOG.err

# Set FBC-specific label for the location of the FBC root directory
# in the image
LABEL operators.operatorframework.io.index.configs.v1=/configs
