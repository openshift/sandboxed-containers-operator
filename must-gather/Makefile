IMAGE_REGISTRY ?= quay.io
IMAGE_TAG ?= latest

build: podman-build podman-push

# check
check:
	shellcheck collection-scripts/*

test:
	bash tests/gather_cni_config_test.sh

ensure-must-gather-image-is-set:
ifndef MUST_GATHER_IMAGE
	$(error MUST_GATHER_IMAGE is not set.)
endif

podman-build: ensure-must-gather-image-is-set
	podman build --squash-all --no-cache . -t ${IMAGE_REGISTRY}/${MUST_GATHER_IMAGE}:${IMAGE_TAG}

podman-push: ensure-must-gather-image-is-set
	podman push ${IMAGE_REGISTRY}/${MUST_GATHER_IMAGE}:${IMAGE_TAG}

.PHONY: build check test podman-build podman-push
