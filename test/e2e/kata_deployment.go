package kata

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"
	exutil "github.com/openshift/origin/test/extended/util"
)

// DeploymentDescription describes a kata deployment to be created from a Go template.
type DeploymentDescription struct {
	name             string
	namespace        string
	runtimeClassName string
	image            string
	replicas         int
	port             int
timeout          time.Duration
	pollInterval     time.Duration
	attributes       map[string]interface{}
	annotations      map[string]string
}

// NewDeploymentDescription returns a DeploymentDescription with defaults from the test run config.
func NewDeploymentDescription(testrun *TestRunDescription, name string, replicas int) *DeploymentDescription {
	return &DeploymentDescription{
		name:             name,
		namespace:        "",
		runtimeClassName: testrun.runtimeClassName,
		image:            testrun.workloadImage,
		replicas:         replicas,
		port:             8888,
		timeout:          600 * time.Second,
		pollInterval:     10 * time.Second,
		attributes:       make(map[string]interface{}),
		annotations:      make(map[string]string),
	}
}

func createKataDeploymentFromDescription(oc *exutil.CLI, deploy *DeploymentDescription) error {
	if deploy.namespace == "" {
		deploy.namespace = oc.Namespace()
	}
	if deploy.name == "" {
		return fmt.Errorf("deployment name is required")
	}

	configFile, err := processDeploymentGoTemplate(deploy)
	if err != nil {
		return err
	}

	ginkgo.By(fmt.Sprintf("Applying deployment configuration from %s", configFile))
	err = oc.AsAdmin().WithoutNamespace().Run("apply").Args("-f", configFile, "-n", deploy.namespace).Execute()
	if err != nil {
		return fmt.Errorf("could not apply configFile %v: %v", configFile, err)
	}

	ginkgo.By(fmt.Sprintf("Waiting for deployment %v to be ready", deploy.name))
	readyReplicas, elapsedSeconds, err := waitForDeployment(oc, deploy)
	if err != nil {
		return fmt.Errorf("deployment (%v) did not become ready: %v", deploy.name, err)
	}
	Logf("Deployment %v took %v seconds to be ready: %v", deploy.name, elapsedSeconds, readyReplicas)

	return nil
}

func processDeploymentGoTemplate(deploy *DeploymentDescription) (string, error) {
	templatePath := getTemplatePath("kataDeploymentTemplate.go.tmpl")
	configFile := setConfigFilePath(getRandomString()+"Deployment.yaml", deploy.namespace)

	if deploy.attributes["name"] == nil {
		deploy.attributes["name"] = deploy.name
	}
	if deploy.attributes["runtimeClassName"] == nil {
		deploy.attributes["runtimeClassName"] = deploy.runtimeClassName
	}
	if deploy.attributes["image"] == nil {
		deploy.attributes["image"] = deploy.image
	}
	if deploy.attributes["replicas"] == nil {
		deploy.attributes["replicas"] = deploy.replicas
	}
	if deploy.attributes["port"] == nil {
		deploy.attributes["port"] = deploy.port
	}

	if len(deploy.annotations) > 0 && deploy.attributes["annotations"] == nil {
		annotationList := make([]string, 0, len(deploy.annotations))
		for key, value := range deploy.annotations {
			annotationList = append(annotationList, fmt.Sprintf("%s%s: \"%s\"", kataAnnotationPrefix, key, value))
		}
		deploy.attributes["annotations"] = annotationList
	}

	err := createConfigFileFromTemplate(deploy.attributes, templatePath, configFile)
	if err != nil {
		return "", fmt.Errorf("failed to process deployment Go template: %v", err)
	}

	return configFile, nil
}

func waitForDeployment(oc *exutil.CLI, deploy *DeploymentDescription) (int, int, error) {
	var (
		intervalSeconds = int(deploy.pollInterval.Seconds())
		maxSeconds      = int(deploy.timeout.Seconds())
		readyReplicas   int
		elapsedSeconds  int
	)

	for readyReplicas != deploy.replicas && elapsedSeconds < maxSeconds {
		msg, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
			"deploy", "-n", deploy.namespace, deploy.name,
			"-o=jsonpath={.status.readyReplicas}",
		).Output()
		if err != nil {
			Logf("failed to get readyReplicas for %v: %v", deploy.name, err)
		} else {
			parsed, parseErr := strconv.Atoi(msg)
			if parseErr != nil {
				Logf("could not parse readyReplicas %q: %v", msg, parseErr)
			} else {
				readyReplicas = parsed
			}
		}
		elapsedSeconds += intervalSeconds
		time.Sleep(deploy.pollInterval)
	}

	if readyReplicas != deploy.replicas {
		return readyReplicas, elapsedSeconds, fmt.Errorf(
			"deployment %v with %v replicas of %v expected is not ready after %v seconds",
			deploy.name, readyReplicas, deploy.replicas, maxSeconds)
	}

	return readyReplicas, elapsedSeconds, nil
}

func scaleDeployment(oc *exutil.CLI, deploy *DeploymentDescription, scaleNumber int) error {
	_, err := oc.AsAdmin().WithoutNamespace().Run("scale").Args(
		"deployment", deploy.name, "--replicas="+strconv.Itoa(scaleNumber), "-n", deploy.namespace,
	).Output()
	o.Expect(err).NotTo(o.HaveOccurred(), fmt.Sprintf("Could not scale deployment %v", err))

	deploy.replicas = scaleNumber

	newReadyReplicas, elapsedSeconds, err := waitForDeployment(oc, deploy)
	o.Expect(err).NotTo(o.HaveOccurred(), fmt.Sprintf("%v", err))
	o.Expect(newReadyReplicas).To(o.Equal(scaleNumber),
		fmt.Sprintf("Deployment %v ready replicas don't match requested %v", deploy.name, scaleNumber))
	Logf("Deployment %v took %v seconds to be ready", deploy.name, elapsedSeconds)
	return err
}

func createServiceAndRoute(oc *exutil.CLI, deployName, podNs string) (string, error) {
	msg, err := oc.WithoutNamespace().Run("expose").Args("deployment", deployName, "-n", podNs).Output()
	if err != nil {
		Logf("Expose deployment failed with: %v %v", msg, err)
		return "", err
	}

	msg, err = oc.WithoutNamespace().Run("expose").Args("service", deployName, "-n", podNs).Output()
	if err != nil {
		Logf("Expose service failed with: %v %v", msg, err)
		return "", err
	}

	host, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
		"routes", deployName, "-n", podNs, "-o=jsonpath={.spec.host}",
	).Output()
	if err != nil || host == "" {
		Logf("Failed to get host from route: %v", err)
		return "", err
	}
	return strings.Trim(host, "'"), nil
}

func deleteRouteAndService(oc *exutil.CLI, deployName, deployNs string) {
	if _, err := deleteResource(oc, "svc", deployName, deployNs, podSnooze*time.Second, 10*time.Second); err != nil {
		Logf("failed to delete service %v: %v", deployName, err)
	}
	if _, err := deleteResource(oc, "route", deployName, deployNs, podSnooze*time.Second, 10*time.Second); err != nil {
		Logf("failed to delete route %v: %v", deployName, err)
	}
}

func getHttpResponse(url string, expStatusCode int) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	res, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer func() {
		if cerr := res.Body.Close(); cerr != nil {
			Logf("failed to close response body: %v", cerr)
		}
	}()
	if res.StatusCode != expStatusCode {
		return "", fmt.Errorf("response from url=%v actual status code=%d doesn't match expected %d", url, res.StatusCode, expStatusCode)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}
