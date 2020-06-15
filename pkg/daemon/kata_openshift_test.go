package daemon

import (
	"context"
	"crypto/rand"
	"fmt"
	"io/ioutil"
	"os"
	"testing"

	kataconfigurationv1alpha1 "github.com/openshift/kata-operator/pkg/apis/kataconfiguration/v1alpha1"
	kataClient "github.com/openshift/kata-operator/pkg/generated/clientset/versioned/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestKataInstall(t *testing.T) {
	kataconfig := &kataconfigurationv1alpha1.KataConfig{
		TypeMeta: v1.TypeMeta{
			APIVersion: "kataconfiguration.openshift.io/v1alpha1",
			Kind:       "KataConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "example-kataconfig",
			Labels: map[string]string{
				"label-key": "label-value",
			},
		},
	}

	kc := kataClient.NewSimpleClientset(kataconfig)

	b := make([]byte, 8)
	_, err := rand.Read(b)
	if err != nil {
		t.Fatalf("Unable to generate rand buffer: %+v", err)
	}
	s := fmt.Sprintf("%x", b)

	fileName := "/tmp/kata-daemon-test-" + s
	defer os.Remove(fileName)

	ko := KataOpenShift{
		KataClientSet: kc,
		KataInstallChecker: func() (bool, error) {
			var isKataInstalled bool
			var err error
			if _, err := os.Stat(fileName); err == nil {
				isKataInstalled = true
				err = nil
			} else if os.IsNotExist(err) {
				isKataInstalled = false
				err = nil
			} else {
				isKataInstalled = false
				err = fmt.Errorf("Unknown error while checking kata installation: %+v", err)
			}
			return isKataInstalled, err
		},
		kataBinaryInstaller: func() error {
			err := ioutil.WriteFile(fileName, []byte(""), 0644)
			return err
		},
	}

	err = ko.Install(kataconfig.Name)
	if err != nil {
		t.Fatalf("Unable to install Kata: %+v", err)
	}

	kataconfig, err = kc.KataconfigurationV1alpha1().KataConfigs(v1.NamespaceAll).Get(context.TODO(), kataconfig.Name, v1.GetOptions{})
	if err != nil {
		t.Errorf("Unable to retrieve kata config: %+v", err)
	}

	if kataconfig.Status.CompletedDaemons != 1 || kataconfig.Status.CompletedNodesCount != 0 {
		t.Errorf("Incorrect values returned for CompletedDaemons and CompletedNodesCount - update CompletedDaemons: %d, %d", kataconfig.Status.CompletedDaemons, kataconfig.Status.CompletedNodesCount)
	}

	err = ko.Install(kataconfig.Name)
	if err != nil {
		t.Fatalf("Unable to install Kata: %+v", err)
	}

	kataconfig, err = kc.KataconfigurationV1alpha1().KataConfigs(v1.NamespaceAll).Get(context.TODO(), kataconfig.Name, v1.GetOptions{})
	if err != nil {
		t.Errorf("Unable to retrieve kata config: %+v", err)
	}

	if kataconfig.Status.CompletedDaemons != 1 || kataconfig.Status.CompletedNodesCount != 1 {
		t.Errorf("Incorrect values returned for CompletedDaemons and CompletedNodesCount, update CompletedNodesCount: %d, %d", kataconfig.Status.CompletedDaemons, kataconfig.Status.CompletedNodesCount)
	}
}
