#!/bin/sh

curl https://raw.githubusercontent.com/openshift/sandboxed-containers-operator/release-4.8/deploy/deploy.sh | bash
oc apply -f https://raw.githubusercontent.com/openshift/sandboxed-containers-operator/release-4.8/config/samples/kataconfiguration_v1_kataconfig.yaml

