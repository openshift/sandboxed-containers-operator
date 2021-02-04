#!/bin/sh

oc apply -f https://raw.githubusercontent.com/openshift/kata-operator/master/deploy/deploy.yaml && \
oc adm policy add-scc-to-user privileged -z default -n kata-operator-system
