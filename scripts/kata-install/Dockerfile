FROM registry.access.redhat.com/ubi9/skopeo:9.7-1777555946

RUN mkdir -p /scripts

ADD osc-kata-install.sh osc-log-level.sh lib.sh osc-kata-addons-install.sh /scripts/

# Multi-arch umoci support (amd64 and s390x)
ARG TARGETARCH=amd64
RUN curl -sSL "https://github.com/opencontainers/umoci/releases/download/v0.5.0/umoci.linux.${TARGETARCH}" -o "/usr/local/bin/umoci" &&\
    chmod +x "/usr/local/bin/umoci"

CMD ["/scripts/osc-kata-install.sh"]
