#!/bin/sh

oc apply -f https://raw.githubusercontent.com/openshift/kata-operator/release-4.7/deploy/deploy.yaml && \
oc adm policy add-scc-to-user privileged -z default -n kata-operator-system
