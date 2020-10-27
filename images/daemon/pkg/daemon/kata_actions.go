package daemon

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	kataTypes "github.com/openshift/kata-operator/pkg/apis/kataconfiguration/v1alpha1"
	kataClient "github.com/openshift/kata-operator/pkg/generated/clientset/versioned"
	v1 "k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// KataActions declares the possible actions the daemon can take.
type KataActions interface {
	Install(kataConfigResourceName string) error
	Upgrade() error
	Uninstall(kataConfigResourceName string) error
}

type updateStatus = func(a *kataTypes.KataConfigStatus)

func updateKataConfigStatus(kataClientSet kataClient.Interface, kataConfigResourceName string, us updateStatus) (err error) {

	attempts := 5
	for i := 0; i < attempts; i++ {
		kataconfig, err := kataClientSet.KataconfigurationV1alpha1().KataConfigs(v1.NamespaceAll).Get(context.TODO(), kataConfigResourceName, metaV1.GetOptions{})
		if err != nil {
			// TODO - we need to return error
			break
		}

		us(&kataconfig.Status)

		_, err = kataClientSet.KataconfigurationV1alpha1().KataConfigs(v1.NamespaceAll).UpdateStatus(context.TODO(), kataconfig, metaV1.UpdateOptions{FieldManager: "kata-install-daemon"})
		if err != nil {
			log.Println("retrying after error:", err)
			continue
		}

		if err == nil {
			break
		}

		time.Sleep(5 * time.Second)
	}

	return err
}

func getFailedNode(err error) (fn kataTypes.FailedNodeStatus, retErr error) {
	nodeName, hErr := getNodeName()
	if hErr != nil {
		return kataTypes.FailedNodeStatus{}, hErr
	}

	return kataTypes.FailedNodeStatus{
		Name:  nodeName,
		Error: fmt.Sprintf("%+v", err),
	}, nil
}

func getHostName() (string, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return "", err
	}
	return hostname, nil
}

func getNodeName() (string, error) {
	return getHostName()
}

func getClientSet() (*kubernetes.Clientset, error) {
	config, err := clientcmd.BuildConfigFromFlags("", "")
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return clientset, nil
}
