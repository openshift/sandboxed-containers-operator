#!/bin/sh

oc apply -f https://raw.githubusercontent.com/openshift/sandboxed-containers-operator/master/deploy/deploy.yaml && \
oc adm policy add-scc-to-user privileged -z default -n sandboxed-containers-operator-system
