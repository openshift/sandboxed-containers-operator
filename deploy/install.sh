#!/bin/sh

curl https://raw.githubusercontent.com/openshift/kata-operator/master/deploy/deploy.sh | bash
oc apply -f https://raw.githubusercontent.com/openshift/kata-operator/master/config/samples/kataconfiguration_v1_kataconfig.yaml

