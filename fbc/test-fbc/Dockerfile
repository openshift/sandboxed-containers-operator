# The builder image is expected to contain
# /bin/opm (with serve subcommand)
FROM brew.registry.redhat.io/rh-osbs/openshift-ose-operator-registry-rhel9:v4.19 as builder
USER root

WORKDIR /workdir
ADD render.sh icon.png ./
COPY test-fbc ./test-fbc

RUN dnf install -y go && go install github.com/mikefarah/yq/v4@v4.35.1
RUN PATH=/root/go/bin:$PATH ./render.sh test-fbc

# Copy FBC root into image at /configs and pre-populate serve cache
RUN cp -r test-fbc/catalog/ /configs
RUN ["/bin/opm", "serve", "/configs", "--cache-dir=/tmp/cache", "--cache-only"]

FROM brew.registry.redhat.io/rh-osbs/openshift-ose-operator-registry-rhel9:v4.19
# The base image is expected to contain
# /bin/opm (with serve subcommand) and /bin/grpc_health_probe

# Configure the entrypoint and command
ENTRYPOINT ["/bin/opm"]
CMD ["serve", "/configs", "--cache-dir=/tmp/cache"]

COPY --from=builder /configs /configs
COPY --from=builder /tmp/cache /tmp/cache

# Set FBC-specific label for the location of the FBC root directory
# in the image
LABEL operators.operatorframework.io.index.configs.v1=/configs
