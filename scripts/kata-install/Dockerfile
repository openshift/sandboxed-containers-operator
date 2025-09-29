FROM registry.access.redhat.com/ubi9/skopeo:9.6-1759106102

RUN mkdir -p /files

ADD 50-kata-remote configuration-remote.toml /files/

RUN mkdir -p /scripts

ADD osc-kata-install.sh osc-configs-script.sh osc-log-level.sh lib.sh /scripts/

RUN curl -sSL "https://github.com/opencontainers/umoci/releases/download/v0.4.7/umoci.amd64" -o "/usr/local/bin/umoci" &&\
    chmod +x "/usr/local/bin/umoci"

CMD ["/scripts/osc-kata-install.sh"]
 