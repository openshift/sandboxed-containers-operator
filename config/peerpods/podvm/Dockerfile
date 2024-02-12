FROM quay.io/openshift_sandboxed_containers/openshift-sandboxed-containers-payload:37c1125179912e5d1104c43fd83d567914278bcb as payload

# podvm binaries under /payload/podvm-binaries.tar.gz
# azure-podvm-image-handler.sh script under /scripts/azure-podvm-image-handler.sh
# aws-podvm-image-handler.sh script under /scripts/aws-podvm-image-handler.sh
# sources for cloud-api-adaptor under /src/cloud-api-adaptor
# Binaries like kubectl, packer and yq under /usr/local/bin


FROM registry.access.redhat.com/ubi9/ubi:9.3

LABEL kata_src=https://github.com/kata-containers/kata-containers/tree/CC-0.8.0
LABEL kata_src_commit=8de1f8e19f858134ba455a7c04edcb21d8bcf6b1

RUN mkdir -p /payload /scripts
COPY --from=payload /podvm-binaries.tar.gz /payload/

ADD lib.sh aws-podvm-image-handler.sh azure-podvm-image-handler.sh  /scripts/

RUN /scripts/azure-podvm-image-handler.sh -- install_rpms
RUN /scripts/azure-podvm-image-handler.sh -- install_cli
RUN /scripts/aws-podvm-image-handler.sh -- install_cli
RUN /scripts/aws-podvm-image-handler.sh -- install_binaries

ARG CAA_SRC
ARG CAA_REF
ENV CAA_SRC=$CAA_SRC
ENV CAA_REF=$CAA_REF
RUN git clone ${CAA_SRC} -b ${CAA_REF} /src/cloud-api-adaptor

ADD podvm-builder.sh /podvm-builder.sh

ENTRYPOINT ["/podvm-builder.sh"]
