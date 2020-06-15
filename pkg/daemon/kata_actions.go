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
)

// KataActions declares the possible actions the daemon can take.
type KataActions interface {
	Install(kataConfigResourceName string) error
	Upgrade() error
	Uninstall() error
}

type updateStatus = func(a *kataTypes.KataConfigStatus)

func updateKataConfigStatus(kataClientSet kataClient.Interface, kataConfigResourceName string, us updateStatus) (err error) {

	attempts := 5
	for i := 0; i < attempts; i++ {
		kataconfig, err := kataClientSet.KataconfigurationV1alpha1().KataConfigs(v1.NamespaceAll).Get(context.TODO(), kataConfigResourceName, metaV1.GetOptions{})
		if err != nil {
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

func getFailedNode(err error) (fn kataTypes.FailedNode, retErr error) {
	// TODO - this may not be correct. Make sure you get the right hostname
	hostname, hErr := os.Hostname()
	if hErr != nil {
		return kataTypes.FailedNode{}, hErr
	}

	return kataTypes.FailedNode{
		Name:  hostname,
		Error: fmt.Sprintf("%+v", err),
	}, nil
}
