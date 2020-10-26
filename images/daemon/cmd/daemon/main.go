package main

import (
	"flag"
	"fmt"
	"os"

	kataDaemon "github.com/openshift/kata-operator-daemon/pkg/daemon"
	kataController "github.com/openshift/kata-operator/pkg/controller/kataconfig"
	kataClient "github.com/openshift/kata-operator/pkg/generated/clientset/versioned"
	"k8s.io/client-go/tools/clientcmd"
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

	isOpenShift, err := kataController.IsOpenShift()
	if err != nil {
		fmt.Println("Unable to determine if we are running in OpenShift or not")
		os.Exit(1)
	}

	//config, err := clientcmd.BuildConfigFromFlags("", "/tmp/kubeconfig")
	config, err := clientcmd.BuildConfigFromFlags("", "")
	if err != nil {
		// TODO - remove printfs with logger
		fmt.Println("error creating config")
		os.Exit(1)
	}

	kc, err := kataClient.NewForConfig(config)
	if err != nil {
		fmt.Println("Unable to get client set")
		os.Exit(1)
	}

	var kataActions kataDaemon.KataActions

	if isOpenShift {
		kataActions = &kataDaemon.KataOpenShift{
			KataClientSet: kc,
			// KataBinaryInstaller: func() error {
			// 	return nil
			// },
			// KataBinaryUnInstaller: func() error {
			// 	return nil
			// },
		}
	} else {
		kataActions = &kataDaemon.KataKubernetes{
			KataClientSet: kc,
		}
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
