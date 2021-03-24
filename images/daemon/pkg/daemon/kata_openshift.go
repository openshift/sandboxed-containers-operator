package daemon

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/containers/image/v5/copy"
	"github.com/containers/image/v5/signature"
	"github.com/containers/image/v5/transports/alltransports"
	"github.com/containers/image/v5/types"
	"github.com/coreos/go-semver/semver"
	"github.com/opencontainers/image-tools/image"
	confv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	kataTypes "github.com/openshift/kata-operator/api/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// KataExistance checkes if kata is already installed or uninstalled on the node
type KataExistance func() (bool, bool, error)

// KataBinaryOperation installs the kata binaries on the node
type KataBinaryOperation func(k *KataOpenShift) error

//KataOpenShift is used for KataActions on OpenShift cluster nodes
type KataOpenShift struct {
	KataClient            client.Client
	KataInstallChecker    KataExistance
	KataUninstallChecker  KataExistance
	KataBinaryInstaller   KataBinaryOperation
	KataBinaryUnInstaller KataBinaryOperation
	KataConfigPoolLabels  map[string]string
	CRIODropinPath        string
	PayloadTag            string
}

var _ KataActions = (*KataOpenShift)(nil)

// Install the kata binaries on Openshift
func (k *KataOpenShift) Install(kataConfigResourceName string) error {

	if k.KataInstallChecker == nil {
		k.KataInstallChecker = func() (bool, bool, error) {
			var (
				isKataInstalled       bool
				isCrioDropInInstalled bool
				err                   error
				kataConfig            kataTypes.KataConfig
			)

			err = k.KataClient.Get(context.Background(), client.ObjectKey{
				Name: kataConfigResourceName,
			}, &kataConfig)
			if err != nil {
				return isKataInstalled, isCrioDropInInstalled, err
			}

			nodeName, err := getNodeName()
			if err != nil {
				return isKataInstalled, isCrioDropInInstalled, err
			}

			for _, n := range kataConfig.Status.InstallationStatus.InProgress.BinariesInstalledNodesList {
				if n == nodeName {
					isKataInstalled = true
					break
				}
			}

			for _, n := range kataConfig.Status.InstallationStatus.Completed.CompletedNodesList {
				if n == nodeName {
					isCrioDropInInstalled = true
					break
				}
			}

			return isKataInstalled, isCrioDropInInstalled, err
		}
	}

	isKataInstalled, isCrioDropInInstalled, err := k.KataInstallChecker()
	if err != nil {
		return err
	}

	if isCrioDropInInstalled {
		return nil
	}

	k.PayloadTag, err = getClusterVersion()
	if err != nil {
		fmt.Println(err)
		return err
	}
	log.Println("Kata operator payload tag: " + k.PayloadTag)

	if k.KataBinaryInstaller == nil {
		k.KataBinaryInstaller = installRPMs
	}

	nodeName, err := getNodeName()
	if err != nil {
		return err
	}

	if isKataInstalled {
		// kata exist - mark completion if crio drop in file exists
		if k.CRIODropinPath == "" {
			k.CRIODropinPath = "/host/etc/crio/crio.conf.d/50-kata.conf"
		}
		if _, err := os.Stat(k.CRIODropinPath); err == nil {
			err = updateKataConfigStatus(k.KataClient, kataConfigResourceName, func(ks *kataTypes.KataConfigStatus) {
				ks.InstallationStatus.Completed.CompletedNodesList = append(ks.InstallationStatus.Completed.CompletedNodesList, nodeName)
				ks.InstallationStatus.Completed.CompletedNodesCount = len(ks.InstallationStatus.Completed.CompletedNodesList)
				if ks.InstallationStatus.InProgress.InProgressNodesCount > 0 {
					ks.InstallationStatus.InProgress.InProgressNodesCount--
				}
				for i, node := range ks.InstallationStatus.InProgress.BinariesInstalledNodesList {
					if node == nodeName {
						ks.InstallationStatus.InProgress.BinariesInstalledNodesList =
							append(ks.InstallationStatus.InProgress.BinariesInstalledNodesList[:i],
								ks.InstallationStatus.InProgress.BinariesInstalledNodesList[i+1:]...)
						break
					}
				}
			})

			if err != nil {
				return fmt.Errorf("kata exists on the node, error updating kataconfig status %+v", err)
			}
		} else if os.IsNotExist(err) {
			// Kata is installed but no crio drop in yet, we will wait.
			return nil
		} else {
			return err
		}

	} else {
		// kata doesn't exist, install it.
		err = updateKataConfigStatus(k.KataClient, kataConfigResourceName, func(ks *kataTypes.KataConfigStatus) {
			ks.InstallationStatus.InProgress.InProgressNodesCount++
		})

		if err != nil {
			return fmt.Errorf("kata is not installed on the node, error updating kataconfig status %+v", err)
		}

		err = k.KataBinaryInstaller(k)

		if err != nil {
			// kata installation failed. report it.
			err = updateKataConfigStatus(k.KataClient, kataConfigResourceName, func(ks *kataTypes.KataConfigStatus) {
				ks.InstallationStatus.InProgress.InProgressNodesCount--

				fn, err := getFailedNode(err)
				if err != nil {
					return
				}

				ks.InstallationStatus.Failed.FailedNodesList = append(ks.InstallationStatus.Failed.FailedNodesList, fn)
				ks.InstallationStatus.Failed.FailedNodesCount = len(ks.InstallationStatus.Failed.FailedNodesList)
			})

			if err != nil {
				return fmt.Errorf("kata installation failed, error updating kataconfig status %+v", err)
			}

		} else {
			// mark binaries installed
			err = updateKataConfigStatus(k.KataClient, kataConfigResourceName, func(ks *kataTypes.KataConfigStatus) {
				ks.InstallationStatus.InProgress.BinariesInstalledNodesList = append(ks.InstallationStatus.InProgress.BinariesInstalledNodesList, nodeName)
			})

			if err != nil {
				return fmt.Errorf("kata installation succeeded, but error updating kataconfig status %+v", err)
			}
		}
	}

	return nil
}

// Upgrade the kata binaries and configure the runtime on Openshift
func (k *KataOpenShift) Upgrade() error {
	return fmt.Errorf("Not Implemented Yet")
}

// Uninstall the kata binaries and configure the runtime on Openshift
func (k *KataOpenShift) Uninstall(kataConfigResourceName string) error {
	if k.KataUninstallChecker == nil {
		k.KataUninstallChecker = func() (bool, bool, error) {

			var (
				isKataUnInstalled       bool
				isCrioDropInUnInstalled bool
				err                     error
				kataConfig              kataTypes.KataConfig
			)

			err = k.KataClient.Get(context.Background(), client.ObjectKey{
				Name: kataConfigResourceName,
			}, &kataConfig)
			if err != nil {
				return isKataUnInstalled, isCrioDropInUnInstalled, err
			}

			// Storing it locally so that we can avoid one more call to API server further down
			if kataConfig.Spec.KataConfigPoolSelector != nil {
				k.KataConfigPoolLabels = kataConfig.Spec.KataConfigPoolSelector.MatchLabels
			}

			nodeName, err := getNodeName()
			if err != nil {
				return isKataUnInstalled, isCrioDropInUnInstalled, err
			}

			for _, n := range kataConfig.Status.UnInstallationStatus.InProgress.BinariesUnInstalledNodesList {
				if n == nodeName {
					isKataUnInstalled = true
					break
				}
			}

			for _, n := range kataConfig.Status.UnInstallationStatus.Completed.CompletedNodesList {
				if n == nodeName {
					isCrioDropInUnInstalled = true
					break
				}
			}

			return isKataUnInstalled, isCrioDropInUnInstalled, err
		}
	}

	isKataUnInstalled, isCrioDropInUnInstalled, err := k.KataUninstallChecker()
	if err != nil {
		return err
	}

	if isCrioDropInUnInstalled {
		return nil
	}

	nodeName, err := getNodeName()
	if err != nil {
		return err
	}

	if !isKataUnInstalled {
		// Kata binaries need to be uninstalled
		err = updateKataConfigStatus(k.KataClient, kataConfigResourceName, func(ks *kataTypes.KataConfigStatus) {
			ks.UnInstallationStatus.InProgress.InProgressNodesCount++
		})

		if err != nil {
			return fmt.Errorf("kata is not installed on the node, error updating kataconfig status %+v", err)
		}

		if k.KataBinaryUnInstaller == nil {
			k.KataBinaryUnInstaller = uninstallRPMs
		}

		err = k.KataBinaryUnInstaller(k)

		if err != nil {
			// kata uninstallation failed. report it.
			err = updateKataConfigStatus(k.KataClient, kataConfigResourceName, func(ks *kataTypes.KataConfigStatus) {
				ks.UnInstallationStatus.InProgress.InProgressNodesCount--

				fn, err := getFailedNode(err)
				if err != nil {
					return
				}

				ks.UnInstallationStatus.Failed.FailedNodesList = append(ks.UnInstallationStatus.Failed.FailedNodesList, fn)
				ks.UnInstallationStatus.Failed.FailedNodesCount = len(ks.UnInstallationStatus.Failed.FailedNodesList)
			})

			if err != nil {
				return fmt.Errorf("kata installation failed, error updating kataconfig status %+v", err)
			}

		}
		// mark binaries uninstalled
		err = updateKataConfigStatus(k.KataClient, kataConfigResourceName, func(ks *kataTypes.KataConfigStatus) {
			ks.UnInstallationStatus.InProgress.BinariesUnInstalledNodesList = append(ks.UnInstallationStatus.InProgress.BinariesUnInstalledNodesList, nodeName)
		})

		if err != nil {
			return fmt.Errorf("kata uninstallation succeeded, but error updating kataconfig status %+v", err)
		}
	}

	return nil
}

func doCmd(cmd *exec.Cmd) error {
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	fmt.Println(cmd.String())
	if err != nil {
		log.Println(err)
		return err
	}

	return nil
}

func cleanupHost() error {
	cmd := exec.Command("/usr/bin/rm", "-rf", "/opt/kata-install")
	err := doCmd(cmd)
	if err != nil {
		return err
	}

	cmd = exec.Command("/usr/bin/rm", "-rf", "/usr/local/kata")
	err = doCmd(cmd)
	if err != nil {
		return err
	}

	return nil
}

func uninstallRPMs(k *KataOpenShift) error {
	log.SetOutput(os.Stdout)

	if err := syscall.Chroot("/host"); err != nil {
		log.Fatalf("Unable to chroot to %s: %s", "/host", err)
	}

	if err := syscall.Chdir("/"); err != nil {
		log.Fatalf("Unable to chdir to %s: %s", "/", err)
	}

	err := cleanupHost()
	if err != nil {
		log.Println("cleanupHost failed")
	}

	cmd := exec.Command("rpm-ostree", "uninstall", "--idempotent", "--all") //FIXME not -a but kata-runtime, kata-osbuilder,...
	err = doCmd(cmd)
	if err != nil {
		return err
	}

	return nil
}

func installRPMs(k *KataOpenShift) error {
	fmt.Fprintf(os.Stderr, "%s\n", os.Getenv("PATH"))
	log.SetOutput(os.Stdout)

	cmd := exec.Command("mkdir", "-p", "/host/opt/kata-install")
	err := doCmd(cmd)
	if err != nil {
		return err
	}

	if err := syscall.Chroot("/host"); err != nil {
		log.Fatalf("Unable to chroot to %s: %s", "/host", err)
	}

	if !nodeCanRunKata() {
		log.Fatalf("This node cannot run kata")
	}

	if err := syscall.Chdir("/"); err != nil {
		log.Fatalf("Unable to chdir to %s: %s", "/", err)
	}

	policy, err := signature.DefaultPolicy(nil)
	if err != nil {
		fmt.Println(err)
	}
	policyContext, err := signature.NewPolicyContext(policy)
	if err != nil {
		fmt.Println(err)
	}

	payloadImage := os.Getenv("KATA_PAYLOAD_IMAGE")
	sourceCtx := &types.SystemContext{}
	if payloadImage != "" {
		username := strings.Replace(os.Getenv("PAYLOAD_REGISTRY_USERNAME"), "\n", "", -1)
		password := strings.Replace(os.Getenv("PAYLOAD_REGISTRY_PASSWORD"), "\n", "", -1)
		if username != "" && password != "" {
			sourceCtx = &types.SystemContext{
				DockerAuthConfig: &types.DockerAuthConfig{
					Username: username,
					Password: password,
				},
			}
		}
		log.Println("WARNING: private payload image in use")
		log.Println("Using env variable KATA_PAYLOAD_IMAGE " + payloadImage)
		payloadImage = "docker://" + payloadImage
	} else {
		payloadImage = "docker://quay.io/isolatedcontainers/kata-operator-payload:" + k.PayloadTag
	}

	srcRef, err := alltransports.ParseImageName(payloadImage)
	if err != nil {
		fmt.Println("Invalid source name of payload container image: " + payloadImage)
		return err
	}
	destRef, err := alltransports.ParseImageName("oci:/opt/kata-install/kata-image:latest")
	if err != nil {
		fmt.Println("Invalid destination name")
		return err
	}

	_, err = copy.Image(context.Background(), policyContext, destRef, srcRef,
		&copy.Options{SourceCtx: sourceCtx})

	if err != nil {
		fmt.Println("Error occured when downloading payload image:")
		fmt.Println(err)
		if os.Getenv("PAYLOAD_REGISTRY_USERNAME") != "" {
			fmt.Println("payload secret env vars are set and used. Please check the credentials used?")
		}
		return err
	}

	err = image.CreateRuntimeBundleLayout("/opt/kata-install/kata-image/",
		"/usr/local/kata", "latest", "linux", []string{"name=latest"})
	if err != nil {
		fmt.Println("error creating Runtime bundle layout in /usr/local/kata")
		return err
	}

	cmd = exec.Command("mkdir", "-p", "/etc/yum.repos.d/")
	err = doCmd(cmd)
	if err != nil {
		return err
	}

	cmd = exec.Command("/usr/bin/cp", "-f", "/usr/local/kata/latest/packages.repo",
		"/etc/yum.repos.d/")
	if err := doCmd(cmd); err != nil {
		return err
	}

	cmd = exec.Command("/usr/bin/cp", "-a",
		"/usr/local/kata/latest/packages", "/opt/kata-install/packages")
	if err = doCmd(cmd); err != nil {
		return err
	}

	cmd = exec.Command("/bin/bash", "-c", "/usr/bin/rpm-ostree install --idempotent kata-containers")
	err = doCmd(cmd)
	if err != nil {
		return err
	}

	err = cleanupHost()
	if err != nil {
		log.Println("cleanupHost failed")
	}

	return nil

}

func getClusterVersion() (string, error) {
	myconfig, err := clientcmd.BuildConfigFromFlags("", "")
	if err != nil {
		return "", err
	}
	myconfclient, err := confv1client.NewForConfig(myconfig)

	myversion := "version"
	clusterversion, err := myconfclient.ClusterVersions().Get(context.Background(), myversion, metaV1.GetOptions{})
	if err != nil {
		return "", err
	}
	mysemver, err := semver.NewVersion(clusterversion.Status.Desired.Version)
	if err != nil {
		return "", err
	}
	versl := mysemver.Slice()
	return strings.Trim(strings.Replace(fmt.Sprint(versl), " ", ".", -1), "[]"), nil
}
