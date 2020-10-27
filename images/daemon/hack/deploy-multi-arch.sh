#!/bin/bash

set -xe

IMAGE_NAME="quay.io/isolatedcontainers/kata-operator-daemon"

if "${TRAVIS_BRANCH}" == "master"; then
	TAG="latest"
else
	TAG="$(echo ${TRAVIS_BRANCH} | awk '{print $2}' FS='-')"
fi

docker login \
	--username="${QUAY_USERNAME}" \
	--password="${QUAY_PASSWORD}" \
	quay.io

env DOCKER_CLI_EXPERIMENTAL="enabled" docker manifest create \
	"$IMAGE_NAME:$TAG" \
	"$IMAGE_NAME:$TAG-amd64" \
	"$IMAGE_NAME:$TAG-ppc64le" \
	"$IMAGE_NAME:$TAG-s390x"

env DOCKER_CLI_EXPERIMENTAL="enabled" docker manifest push \
	--purge \
	"$IMAGE_NAME:$TAG"
