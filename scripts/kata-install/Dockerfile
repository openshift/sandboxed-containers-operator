FROM registry.access.redhat.com/ubi9/skopeo:9.6-1760517870

ARG UMO_VERSION=0.4.7
ARG TARGETARCH

RUN mkdir -p /files /scripts

ADD 50-kata-remote configuration-remote.toml /files/
ADD osc-kata-install.sh osc-configs-script.sh osc-log-level.sh lib.sh /scripts/

# fetch the right umoci binary for the target arch
RUN set -eux; \
    case "${TARGETARCH}" in \
      amd64)    UMO_SUFFIX=amd64 ;; \
      arm64)    UMO_SUFFIX=arm64 ;; \
      ppc64le)  UMO_SUFFIX=ppc64le ;; \
      s390x)    UMO_SUFFIX=s390x ;; \
      *) echo "Unsupported TARGETARCH: ${TARGETARCH}"; exit 1 ;; \
    esac; \
    curl -fsSL "https://github.com/opencontainers/umoci/releases/download/v${UMO_VERSION}/umoci.${UMO_SUFFIX}" \
      -o /usr/local/bin/umoci; \
    chmod +x /usr/local/bin/umoci; \
    # optional: verify it runs and prints a version
    /usr/local/bin/umoci --version || { echo "umoci not runnable"; exit 1; }

CMD ["/scripts/osc-kata-install.sh"]
