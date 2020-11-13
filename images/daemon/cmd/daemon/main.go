package main

import (
	"flag"
	"fmt"
	"os"

	kataDaemon "github.com/openshift/kata-operator-daemon/pkg/daemon"
	kataTypes "github.com/openshift/kata-operator/api/v1"
	mcfgapi "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	nodeapi "k8s.io/kubernetes/pkg/apis/node/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func main() {

	var kataOperation string
	flag.StringVar(&kataOperation, "operation", "", "Specify kata operations. Valid options are 'install', 'upgrade', 'uninstall'")

	var kataConfigResourceName string
	flag.StringVar(&kataConfigResourceName, "resource", "", "Kata Config Custom Resource Name")
	flag.Parse()

	if kataOperation == "" {
		fmt.Println("Operation type must be specified. Check -h for more information.")
		os.Exit(1)
	}
	if kataConfigResourceName == "" {
		fmt.Println("Kata Custom Resource name must be specified. Check -h for more information.")
		os.Exit(1)
	}

	var kataActions kataDaemon.KataActions

	kataClient, err := getKataConfigClient()
	if err != nil {
		fmt.Printf("Unable to get dynamic kata config client, %+v", err)
		os.Exit(1)
	}

	kataActions = &kataDaemon.KataOpenShift{
		KataClient: kataClient,
	}

	switch kataOperation {
	case "install":
		err := kataActions.Install(kataConfigResourceName)
		if err != nil {
			fmt.Printf("Error while installation: %+v", err)
		}
	case "upgrade":
		kataActions.Upgrade()
	case "uninstall":
		err := kataActions.Uninstall(kataConfigResourceName)
		if err != nil {
			fmt.Printf("Error while uninstallation: %+v", err)
		}
	default:
		fmt.Println("invalid operation. Check -h for more information.")
	}

	// Wait till controller kills us
	for {
		c := make(chan int)
		<-c
	}
}

func getKataConfigClient() (client.Client, error) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = kataTypes.AddToScheme(scheme)
	_ = nodeapi.AddToScheme(scheme)
	_ = mcfgapi.Install(scheme)

	kubeconfig := ctrl.GetConfigOrDie()
	kubeclient, err := client.New(kubeconfig, client.Options{Scheme: scheme})
	if err != nil {
		return nil, err
	}
	return kubeclient, nil
}
