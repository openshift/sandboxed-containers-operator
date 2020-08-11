#!/bin/sh


kubectl get namespace kata-operator > /dev/null 2>&1
if [ $? -eq 0 ]; then
	echo "Error: The namespace kata-operator already exists. Please delete it (kubectl delete namespace kata-operator)"
	echo "and make sure no older version of kata-operator is running before continuing"
	exit
else
	echo "Create namespace kata-operator"
	kubectl create namespace kata-operator
fi

set -e

#set up service account
kubectl apply -f https://raw.githubusercontent.com/openshift/kata-operator/master/deploy/role.yaml
kubectl apply -f https://raw.githubusercontent.com/openshift/kata-operator/master/deploy/role_binding.yaml
kubectl apply -f https://raw.githubusercontent.com/openshift/kata-operator/master/deploy/service_account.yaml

kubectl apply -f https://raw.githubusercontent.com/openshift/kata-operator/master/deploy/crds/kataconfiguration.openshift.io_kataconfigs_crd.yaml
kubectl create -f https://raw.githubusercontent.com/openshift/kata-operator/master/deploy/operator.yaml


cat <<EOF 

The kata-operator is ready. Deploy a custom resource to start the installation
See: https://raw.githubusercontent.com/openshift/kata-operator/master/deploy/crds/kataconfiguration.openshift.io_v1alpha1_kataconfig_cr_k8s.yaml as an example
To immediately start installation on all worker nodes,

  kubectl apply -f https://raw.githubusercontent.com/openshift/kata-operator/master/deploy/crds/kataconfiguration.openshift.io_v1alpha1_kataconfig_cr_k8s.yaml

EOF
