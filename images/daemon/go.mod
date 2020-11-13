module github.com/openshift/kata-operator-daemon

go 1.13

require (
	github.com/containers/image/v5 v5.5.1
	github.com/coreos/go-semver v0.3.0
	github.com/dsnet/compress v0.0.1 // indirect
	github.com/opencontainers/image-tools v1.0.0-rc1.0.20190306063041-93db3b16e673
	github.com/openshift/client-go v0.0.0-20200827190008-3062137373b5
	github.com/openshift/kata-operator v0.0.0-20201106123035-a3bf549cd866
	github.com/openshift/machine-config-operator v0.0.1-0.20200918082730-c08c048584ef
	k8s.io/apimachinery v0.19.0
	k8s.io/client-go v12.0.0+incompatible
	k8s.io/kubernetes v0.19.0
	sigs.k8s.io/controller-runtime v0.6.3
)

// Pinned to kubernetes-1.16.2
replace (
	github.com/Sirupsen/logrus => github.com/sirupsen/logrus v1.0.5

	github.com/docker/docker => github.com/moby/moby v0.7.3-0.20190826074503-38ab9da00309 // Required by Helm
	github.com/go-log/log => github.com/go-log/log v0.1.1-0.20181211034820-a514cf01a3eb
	github.com/openshift/api => github.com/openshift/api v0.0.0-20200916161728-83f0cb093902

	// So that we can import MCO
	k8s.io/api => k8s.io/api v0.19.0
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.19.0
	k8s.io/apimachinery => k8s.io/apimachinery v0.19.0
	k8s.io/apiserver => k8s.io/apiserver v0.19.0
	k8s.io/cli-runtime => k8s.io/cli-runtime v0.19.0
	k8s.io/client-go => k8s.io/client-go v0.19.0
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.19.0
	k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.19.0
	k8s.io/code-generator => k8s.io/code-generator v0.19.0
	k8s.io/component-base => k8s.io/component-base v0.19.0
	k8s.io/cri-api => k8s.io/cri-api v0.19.0
	k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.19.0
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.19.0
	k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.19.0
	k8s.io/kube-proxy => k8s.io/kube-proxy v0.19.0
	k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.19.0
	k8s.io/kubectl => k8s.io/kubectl v0.19.0
	k8s.io/kubelet => k8s.io/kubelet v0.19.0
	k8s.io/kubernetes => k8s.io/kubernetes v1.19.0
	k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.19.0
	k8s.io/metrics => k8s.io/metrics v0.19.0
	k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.19.0
)
