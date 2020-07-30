#!/bin/bash

set -xe

IMAGE_NAME="quay.io/isolatedcontainers/kata-operator"

if "${TRAVIS_BRANCH}" == "master"; then
	TAG="latest-${TRAVIS_CPU_ARCH}"
else
	TAG="$(echo ${TRAVIS_BRANCH} | awk '{print $2}' FS='-')-${TRAVIS_CPU_ARCH}"
fi

# This is needed as even on a generic env, a really old version of
# go is installed (but only on AMD64).
export GOROOT=/usr/local/${TRAVIS_CPU_ARCH}/go
export PATH=${GOROOT}/bin:$PATH
export GOTOOLDIR=${GOROOT}/pkg/tool/linux_${TRAVIS_CPU_ARCH}

docker login \
	--username="${QUAY_USERNAME}" \
	--password="${QUAY_PASSWORD}" \
	quay.io

operator-sdk-${TRAVIS_CPU_ARCH} build "${IMAGE_NAME}:${TAG}"

docker push "${IMAGE_NAME}:${TAG}"
