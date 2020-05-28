package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"syscall"
	"time"

	kataTypes "github.com/openshift/kata-operator/pkg/apis/kataconfiguration/v1alpha1"
	kataClient "github.com/openshift/kata-operator/pkg/generated/clientset/versioned"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	"github.com/containers/image/v5/copy"
        "github.com/containers/image/v5/signature"
        "github.com/containers/image/v5/transports/alltransports"
        "github.com/opencontainers/image-tools/image"
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

	//config, err := clientcmd.BuildConfigFromFlags("", "/tmp/kubeconfig")
	config, err := clientcmd.BuildConfigFromFlags("", "")
	if err != nil {
		fmt.Println("error creating config")
	}
	fmt.Println("got config")

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

func doCmd(cmd *exec.Cmd) {
	err := cmd.Start()
	fmt.Printf(cmd.String())
	if err != nil {
		log.Println(err)
	}
	log.Println("Waiting for command to finish...")
	err = cmd.Wait()
	log.Printf("Command finished with error: %v\n", err)
}

func rpmostreeOverrideReplace(rpms string) {
	cmd := exec.Command("/bin/bash", "-c", "/usr/bin/rpm-ostree override replace /opt/kata-install/packages/"+rpms)
	doCmd(cmd)
}

func installBinaries() error {
	fmt.Fprintf(os.Stderr, "%s\n", os.Getenv("PATH"))
	log.SetOutput(os.Stdout)
	cmd := exec.Command("mkdir", "-p", "/host/opt/kata-install")
	doCmd(cmd)

	if err := syscall.Chroot("/host"); err != nil {
		log.Fatalf("Unable to chroot to %s: %s", "/host", err)
	}

	if err := syscall.Chdir("/"); err != nil {
		log.Fatalf("Unable to chdir to %s: %s", "/", err)
	}

	fmt.Println("in INSTALLBINARIES")
	policy, err := signature.DefaultPolicy(nil)
	if err != nil {
		fmt.Println(err)
	}
	policyContext, err := signature.NewPolicyContext(policy)
	if err != nil {
		fmt.Println(err)
	}
	srcRef, err := alltransports.ParseImageName("docker://quay.io/jensfr/kata-artifacts:latest")
	if err != nil {
		fmt.Println("Invalid source name")
	}
	destRef, err := alltransports.ParseImageName("oci:/opt/kata-install/kata-image:latest")
	if err != nil {
		fmt.Println("Invalid destination name")
	}
	fmt.Println("copying down image...")
	_, err = copy.Image(context.Background(), policyContext, destRef, srcRef, &copy.Options{})
	fmt.Println("done with copying image")
	err = image.CreateRuntimeBundleLayout("/opt/kata-install/kata-image/", "/usr/local/kata", "latest", "linux", "v1.0")
	if err != nil {
		fmt.Println("error creating Runtime bundle layout in /usr/local/kata")
	}
	fmt.Println("created Runtime bundle layout in /usr/local/kata")
	fmt.Println(err)

	//FIXME from here on
	cmd = exec.Command("/usr/bin/cp", "-f", "/usr/local/kata/linux/packages.repo", "/etc/yum.repos.d/")
	cmd.Path = "/usr/bin/cp"
	err = cmd.Run()

	if err != nil {
		fmt.Fprintf(os.Stderr, "cp packages.repo failed failed\n")
	}

	cmd = exec.Command("/usr/bin/cp", "-a", "/usr/local/kata/linux/usr/src/kata-containers/packages", "/opt/kata-install/packages")
	cmd.Path = "/usr/bin/cp"
	err = cmd.Run()

	if err != nil {
		fmt.Fprintf(os.Stderr, "cp packages.repo failed failed\n")
	}


	out, err = exec.Command("/usr/bin/rpm-ostree", "status").Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "rpm-ostree status failed\n")

	}
	fmt.Fprintf(os.Stderr, "%s\n", out)

	out, err = exec.Command("pwd").Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ostree override linux firmware failed\n")
		log.Println(err)
	}
	fmt.Fprintf(os.Stderr, "%s\n", out)

	rpmostreeOverrideReplace("linux-firmware-20191202-97.gite8a0f4c9.el8.noarch.rpm")
	rpmostreeOverrideReplace("kernel-*.rpm")
	rpmostreeOverrideReplace("{rdma-core-*.rpm,libibverbs*.rpm}")

	out, err = exec.Command("/usr/bin/rpm-ostree", "install", "--idempotent", "--reboot", "kata-runtime", "kata-osbuilder").Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ostree install kata failed\n")
	}
	fmt.Fprintf(os.Stderr, "%s\n", out)

	return err
}
