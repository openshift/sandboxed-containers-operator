#!/bin/sh


oc get namespace kata-operator > /dev/null 2>&1
if [ $? -eq 0 ]; then
	echo "Error: The namespace kata-operator already exists. Please delete it (oc delete namespace kata-operator)"
	echo "and make sure no older version of kata-operator is running before continuing"
	exit
else
	echo "Create namespace kata-operator"
	oc create namespace kata-operator
fi

set -e

#set up service account
oc apply -f deploy/role.yaml
oc apply -f deploy/role_binding.yaml
oc apply -f deploy/service_account.yaml
oc adm policy add-scc-to-user privileged -z kata-operator

oc apply -f deploy/crds/kataconfiguration.openshift.io_kataconfigs_crd.yaml
oc create -f deploy/operator.yaml

cat <<EOF 

The kata-operator is ready. Deploy a custom resource to start the installation
See: deploy/crds/kataconfiguration.openshift.io_v1alpha1_kataconfig_cr.yaml as an example
To immediately start installation on all worker nodes just do

  oc apply -f deploy/crds/kataconfiguration.openshift.io_v1alpha1_kataconfig_cr.yaml

EOF

