#!/bin/sh

curl https://raw.githubusercontent.com/openshift/kata-operator/release-4.7/deploy/deploy.sh | bash
oc apply -f https://raw.githubusercontent.com/openshift/kata-operator/release-4.7/config/samples/kataconfiguration_v1_kataconfig.yaml

