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
		KataClientSet:  kc,
		CRIODropinPath: fileName,
	}

	ko.KataInstallChecker = func() (bool, bool, error) {
		return false, false, nil
	}

	ko.KataBinaryInstaller = func() error {
		err := ioutil.WriteFile(fileName, []byte(""), 0644)
		return err
	}

	err = ko.Install(kataconfig.Name)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	kataconfig, err = kc.KataconfigurationV1alpha1().KataConfigs(v1.NamespaceAll).Get(context.TODO(), kataconfig.Name, v1.GetOptions{})
	if err != nil {
		t.Errorf("Error fetching kata config %+v", err)
	}

	if len(kataconfig.Status.InstallationStatus.InProgress.BinariesInstalledNodesList) != 1 {
		t.Errorf("Unexpected number of installed nodes found %d", len(kataconfig.Status.InstallationStatus.InProgress.BinariesInstalledNodesList))
	}

	ko.KataInstallChecker = func() (bool, bool, error) {
		return true, false, nil
	}

	err = ko.Install(kataconfig.Name)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	kataconfig, err = kc.KataconfigurationV1alpha1().KataConfigs(v1.NamespaceAll).Get(context.TODO(), kataconfig.Name, v1.GetOptions{})
	if err != nil {
		t.Errorf("Error fetching kata config %+v", err)
	}

	if len(kataconfig.Status.InstallationStatus.InProgress.BinariesInstalledNodesList) != 0 {
		t.Errorf("Unexpected number of binary installed nodes found %d", len(kataconfig.Status.InstallationStatus.InProgress.BinariesInstalledNodesList))
	}

	if kataconfig.Status.InstallationStatus.Completed.CompletedNodesCount != 1 {
		t.Errorf("Unexpected number of completed install nodes found %d", kataconfig.Status.InstallationStatus.Completed.CompletedNodesCount)
	}

	nodeName, err := getNodeName()
	if err != nil {
		t.Errorf("Unable to retrieve node name: %+v", err)
	}

	if kataconfig.Status.InstallationStatus.Completed.CompletedNodesList[0] != nodeName {
		t.Errorf("Unexpected completed install node found %s", kataconfig.Status.InstallationStatus.Completed.CompletedNodesList[0])
	}

}

func TestKataInstallFail(t *testing.T) {
	kataconfig := &kataconfigurationv1alpha1.KataConfig{
		TypeMeta: v1.TypeMeta{
			APIVersion: "kataconfiguration.openshift.io/v1alpha1",
			Kind:       "KataConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "example-kataconfig",
		},
	}

	kc := kataClient.NewSimpleClientset(kataconfig)

	ko := KataOpenShift{
		KataClientSet: kc,
	}
	fakeError := "Test returning fake error"
	ko.KataBinaryInstaller = func() error {
		return fmt.Errorf(fakeError)
	}

	err := ko.Install(kataconfig.Name)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	kataconfig, err = kc.KataconfigurationV1alpha1().KataConfigs(v1.NamespaceAll).Get(context.TODO(), kataconfig.Name, v1.GetOptions{})
	if err != nil {
		t.Errorf("Error fetching kata config %+v", err)
	}

	if kataconfig.Status.InstallationStatus.Failed.FailedNodesCount != 1 {
		t.Errorf("Unexpected number of failed nodes found %d", kataconfig.Status.InstallationStatus.Failed.FailedNodesCount)
	}

	nodeName, err := getNodeName()
	if err != nil {
		t.Errorf("Unable to retrieve node name: %+v", err)
	}

	if kataconfig.Status.InstallationStatus.Failed.FailedNodesList[0].Name != nodeName {
		t.Errorf("Unexpect failed node name found %s", kataconfig.Status.InstallationStatus.Failed.FailedNodesList[0].Name)
	}

	if kataconfig.Status.InstallationStatus.Failed.FailedNodesList[0].Error != fakeError {
		t.Errorf("Unexpect failed node error found %s", kataconfig.Status.InstallationStatus.Failed.FailedNodesList[0].Error)
	}
}

func TestKataUnInstall(t *testing.T) {
	kataconfig := &kataconfigurationv1alpha1.KataConfig{
		TypeMeta: v1.TypeMeta{
			APIVersion: "kataconfiguration.openshift.io/v1alpha1",
			Kind:       "KataConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "example-kataconfig",
		},
	}

	kc := kataClient.NewSimpleClientset(kataconfig)

	ko := KataOpenShift{
		KataClientSet: kc,
	}

	ko.KataUninstallChecker = func() (bool, bool, error) {
		return false, false, nil
	}

	ko.KataBinaryUnInstaller = func() error {
		return nil
	}

	err := ko.Uninstall(kataconfig.Name)
	if err != nil {
		t.Errorf("Unexpect error while executing uninstall %+v", err)
	}

	kataconfig, err = kc.KataconfigurationV1alpha1().KataConfigs(v1.NamespaceAll).Get(context.TODO(), kataconfig.Name, v1.GetOptions{})
	if err != nil {
		t.Errorf("Error fetching kata config %+v", err)
	}

	if len(kataconfig.Status.UnInstallationStatus.InProgress.BinariesUnInstalledNodesList) != 1 {
		t.Errorf("Unexpect number of uninstalled nodes found %d", len(kataconfig.Status.UnInstallationStatus.InProgress.BinariesUnInstalledNodesList))
	}

	nodeName, err := getNodeName()
	if err != nil {
		t.Errorf("Unable to retrieve node name: %+v", err)
	}

	if kataconfig.Status.UnInstallationStatus.InProgress.BinariesUnInstalledNodesList[0] != nodeName {
		t.Errorf("Unexpect uninstalled node name found %s", kataconfig.Status.UnInstallationStatus.InProgress.BinariesUnInstalledNodesList[0])
	}
}

func TestKataUnInstallFail(t *testing.T) {
	kataconfig := &kataconfigurationv1alpha1.KataConfig{
		TypeMeta: v1.TypeMeta{
			APIVersion: "kataconfiguration.openshift.io/v1alpha1",
			Kind:       "KataConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "example-kataconfig",
		},
	}

	kc := kataClient.NewSimpleClientset(kataconfig)

	ko := KataOpenShift{
		KataClientSet: kc,
	}
	fakeError := "Test returning fake error"
	ko.KataBinaryUnInstaller = func() error {
		return fmt.Errorf(fakeError)
	}

	err := ko.Uninstall(kataconfig.Name)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	kataconfig, err = kc.KataconfigurationV1alpha1().KataConfigs(v1.NamespaceAll).Get(context.TODO(), kataconfig.Name, v1.GetOptions{})
	if err != nil {
		t.Errorf("Error fetching kata config %+v", err)
	}

	if kataconfig.Status.UnInstallationStatus.Failed.FailedNodesCount != 1 {
		t.Errorf("Unexpected number of failed nodes found %d", kataconfig.Status.InstallationStatus.Failed.FailedNodesCount)
	}

	nodeName, err := getNodeName()
	if err != nil {
		t.Errorf("Unable to retrieve node name: %+v", err)
	}

	if kataconfig.Status.UnInstallationStatus.Failed.FailedNodesList[0].Name != nodeName {
		t.Errorf("Unexpect failed node name found %s", kataconfig.Status.UnInstallationStatus.Failed.FailedNodesList[0].Name)
	}

	if kataconfig.Status.UnInstallationStatus.Failed.FailedNodesList[0].Error != fakeError {
		t.Errorf("Unexpect failed node error found %s", kataconfig.Status.UnInstallationStatus.Failed.FailedNodesList[0].Error)
	}
}
