package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	kataTypes "github.com/openshift/kata-operator/pkg/apis/kataconfiguration/v1alpha1"
	kataClient "github.com/openshift/kata-operator/pkg/generated/clientset/versioned"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
)

func main() {

	var kataOperation string
	flag.StringVar(&kataOperation, "operation", "", "Specify kata operations. Valid options are 'install', 'upgrade', 'uninstall'")

	var kataConfigResourceName string
	flag.StringVar(&kataConfigResourceName, "resource", "", "Kata Config Custom Resource Name")
	flag.Parse()

	if kataOperation == "" {
		os.Exit(1)
	}
	if kataConfigResourceName == "" {
		os.Exit(1)
	}

	switch kataOperation {
	case "install":
		installKata(kataConfigResourceName)
	case "upgrade":
		upgradeKata()
	case "uninstall":
		uninstallKata()
	default:
		fmt.Println("invalid operation")
		os.Exit(1)
	}

}

func upgradeKata() error {
	return nil
}

func uninstallKata() error {
	return nil
}

func installKata(kataConfigResourceName string) {

	fmt.Println("HEYHHO HHO HEY")
	//config, err := clientcmd.BuildConfigFromFlags("", "/tmp/kubeconfig")
	config, err := clientcmd.BuildConfigFromFlags("", "")
	if err != nil {
		fmt.Println(err)
	}

	kataClientSet, err := kataClient.NewForConfig(config)

	attempts := 5
	for i := 0; ; i++ {
		kataconfig, err := kataClientSet.KataconfigurationV1alpha1().KataConfigs("default").Get(context.TODO(), kataConfigResourceName, metaV1.GetOptions{})
		if err != nil {
			fmt.Println("error 0")
			fmt.Println(err)
		}

		// TODO - remove this line, no longer required.
		kataconfig.Status.FailedNodes = []kataTypes.FailedNode{}
		kataconfig.Status.InProgressNodesCount = kataconfig.Status.InProgressNodesCount + 1
		_, err = kataClientSet.KataconfigurationV1alpha1().KataConfigs("default").UpdateStatus(context.TODO(), kataconfig, metaV1.UpdateOptions{FieldManager: "kata-install-daemon"})
		if err != nil {
			fmt.Println("error 1")
			fmt.Println(err)

		}

		if err == nil {
			// return
			break
		}

		if i >= (attempts - 1) {
			break
		}

		time.Sleep(5 * time.Second)

		log.Println("retrying after error:", err)
	}

	err = installBinaries()
	if err != nil {
		os.Exit(1)
	}
	time.Sleep(10 * time.Second)

	attempts = 5
	for i := 0; ; i++ {
		kataconfig, err := kataClientSet.KataconfigurationV1alpha1().KataConfigs("default").Get(context.TODO(), kataConfigResourceName, metaV1.GetOptions{})
		if err != nil {
			fmt.Println("error 0")
			fmt.Println(err)
		}

		kataconfig.Status.InProgressNodesCount = kataconfig.Status.InProgressNodesCount - 1
		kataconfig.Status.CompletedNodesCount = kataconfig.Status.CompletedNodesCount + 1

		_, err = kataClientSet.KataconfigurationV1alpha1().KataConfigs("default").UpdateStatus(context.TODO(), kataconfig, metaV1.UpdateOptions{FieldManager: "kata-install-daemon"})
		if err != nil {
			fmt.Println("error 1")
			fmt.Println(err)

		}

		if err == nil {
			// return
			break
		}

		if i >= (attempts - 1) {
			break
		}

		time.Sleep(5 * time.Second)

		log.Println("retrying after error:", err)

	}

	for {
		c := make(chan int)
		<-c
	}
}

func installBinaries() error {
	return nil
}
