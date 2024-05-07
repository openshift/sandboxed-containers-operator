FROM registry.access.redhat.com/ubi9/ubi:9.3

# azure-podvm-image-handler.sh script under /scripts/azure-podvm-image-handler.sh
# aws-podvm-image-handler.sh script under /scripts/aws-podvm-image-handler.sh
# sources for cloud-api-adaptor under /src/cloud-api-adaptor
# The podvm binaries are expected to be under /payload/podvm-binaries.tar.gz
# Binaries like kubectl, packer and yq under /usr/local/bin will be installed by the scripts


LABEL kata_src=https://github.com/kata-containers/kata-containers/tree/CC-0.8.0
LABEL kata_src_commit=8de1f8e19f858134ba455a7c04edcb21d8bcf6b1

RUN mkdir -p /scripts

ADD lib.sh aws-podvm-image-handler.sh azure-podvm-image-handler.sh  /scripts/

RUN /scripts/azure-podvm-image-handler.sh -- install_rpms

ARG CAA_SRC=https://github.com/confidential-containers/cloud-api-adaptor
ARG CAA_REF=main
ARG CERT_RPM
ENV CAA_SRC=$CAA_SRC
ENV CAA_REF=$CAA_REF
ENV CERT_RPM=$CERT_RPM

RUN if [[ -n "$CERT_RPM" ]] ; then \
    dnf install -y $CERT_RPM ; \
    fi

RUN git clone ${CAA_SRC} -b ${CAA_REF} /src/cloud-api-adaptor

ADD podvm-builder.sh /podvm-builder.sh

ENTRYPOINT ["/podvm-builder.sh"]
