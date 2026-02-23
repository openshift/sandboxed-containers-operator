# How to build the image

## Multi-architecture images

This guide explains how to build and push multi-architecture container images using Podman or Docker.

### Prerequisites
- A Linux system with QEMU and `binfmt` properly configured for cross-platform builds.
- Access to a container registry (e.g., `quay.io`).

The image uses an environment variable `TARGETARCH`, which should match one of the supported architectures (e.g., `amd64`, `s390x`) recognized by your build tool and `umoci`.

### Building with Podman

Run the following commands from the project root directory:

```bash
VERSION=1.12
IMAGE_BASE=quay.io/openshift_sandboxed_containers/osc-daemonset

# Build for amd64
podman build \
  --platform linux/amd64 \
  --build-arg TARGETARCH=amd64 \
  -t "${IMAGE_BASE}:${VERSION}-amd64" \
  -f scripts/kata-install/Dockerfile \
  ./scripts/kata-install

# Build for s390x
podman build \
  --platform linux/s390x \
  --build-arg TARGETARCH=s390x \
  -t "${IMAGE_BASE}:${VERSION}-s390x" \
  -f scripts/kata-install/Dockerfile \
  ./scripts/kata-install

# Create a multi-arch manifest
podman manifest create "${IMAGE_BASE}:${VERSION}"
podman manifest add "${IMAGE_BASE}:${VERSION}" "${IMAGE_BASE}:${VERSION}-amd64"
podman manifest add "${IMAGE_BASE}:${VERSION}" "${IMAGE_BASE}:${VERSION}-s390x"

# Push the multi-arch image
podman manifest push --all "${IMAGE_BASE}:${VERSION}"
```
Tip: Verify the manifest after creation:

```bash
podman manifest inspect "${IMAGE_BASE}:${VERSION}"
```

### Building with Docker buildx

Run the following command from the project root directory:

```bash
docker buildx build \
  --platform linux/amd64,linux/s390x \
  -t quay.io/openshift_sandboxed_containers/osc-daemonset:1.12 \
  -f scripts/kata-install/Dockerfile \
  ./scripts/kata-install \
  --push
```

### Additional information

Multi-architecture manifests allow a single image tag to support multiple CPU architectures, enabling automatic selection based on the client’s platform.
Learn more about multi-platform builds:
- [Docker official documentation](https://docs.docker.com/build/building/multi-platform)
- [Red Hat guide on multi-architecture](https://developers.redhat.com/learning/learn:openshift:simplify-certificate-management-openshift-across-multiple-architectures/resource/resources:create-multi-architecture-images-cross-platform-applications)
