FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

ARG GH_VERSION=2.103.3
ARG TKN_VERSION=0.45.3

RUN microdnf install -y \
        bash \
        jq \
        util-linux \
        findutils \
        tar \
        gzip \
    && microdnf clean all

RUN ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/') && \
    curl -fsSL "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_${ARCH}.tar.gz" | \
    tar xz --strip-components=1 -C /usr/local && \
    gh --version

RUN ARCH=$(uname -m) && \
    curl -fsSL "https://github.com/tektoncd/cli/releases/download/v${TKN_VERSION}/tkn_${TKN_VERSION}_Linux_${ARCH}.tar.gz" | \
    tar xz -C /usr/local/bin tkn && \
    tkn version

COPY *.sh /app/
RUN chmod +x /app/*.sh

WORKDIR /reports

# GH_TOKEN or GITHUB_TOKEN: GitHub personal access token with write access
#   to the repositories in scope.
# KUBECONFIG: path to a mounted kubeconfig file for Tekton pipeline checks.
#   Mount the file and set e.g. KUBECONFIG=/secrets/kubeconfig.
# SLACK_WEBHOOK_URL (optional): Slack incoming webhook URL for posting reports.

ENTRYPOINT ["/app/process-konflux-prs.sh"]
