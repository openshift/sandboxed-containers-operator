FROM registry.access.redhat.com/ubi9/skopeo:9.8-1784753251

ARG ORG_ID
ARG ACTIVATION_KEY

# This registering RHEL when building on an unsubscribed system
# If you are running a UBI container on a registered and subscribed RHEL host,
# the main RHEL Server repository is enabled inside the standard UBI container.
# Uncomment this and provide the associated ARG variables to register.
#RUN if [[ -n "${ACTIVATION_KEY}" && -n "${ORG_ID}" ]]; then \
#    rm -f /etc/rhsm-host && rm -f /etc/pki/entitlement-host; \
#    subscription-manager register --org=${ORG_ID} --activationkey=${ACTIVATION_KEY}; \
#    fi

RUN mkdir -p /scripts

ADD osc-kata-install.sh osc-log-level.sh lib.sh osc-kata-addons-install.sh /scripts/

# Install golang for building umoci
RUN dnf install -y golang && dnf clean all

# Copy go.mod and go.sum to use our dependency versions
COPY go.mod go.sum /tmp/tools/
WORKDIR /tmp/tools

# Build and install umoci using our module's dependency versions
RUN GOSUMDB=off GOBIN=/usr/local/bin go install github.com/opencontainers/umoci/cmd/umoci

# Clean up the tools directory and remove golang to reduce image size
RUN cd / && rm -rf /tmp/tools && dnf remove -y golang && dnf clean all

WORKDIR /

CMD ["/scripts/osc-kata-install.sh"]
