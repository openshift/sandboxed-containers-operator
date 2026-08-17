package kata

import (
	"fmt"
	"time"

	"github.com/onsi/ginkgo/v2"
	exutil "github.com/openshift/origin/test/extended/util"
)

// PodDescription describes a kata pod to be created from a Go template.
type PodDescription struct {
	name             string
	baseName         string
	namespace        string
	runtimeClassName string
	image            string
	phase            string
	template         string
	timeout          time.Duration
	pollInterval     time.Duration
	attributes       map[string]interface{}
	annotations      map[string]string
	volumes          []VolumeConfig
}

// VolumeConfig describes a volume mount for a kata pod.
type VolumeConfig struct {
	name       string
	mountPath  string
	volumeType string
	sizeLimit  string
	medium     string
}

// NewPodDescription returns a PodDescription with defaults from the test run config.
func NewPodDescription(testrun *TestRunDescription, baseName string) *PodDescription {
	return &PodDescription{
		baseName:         baseName,
		runtimeClassName: testrun.runtimeClassName,
		image:            testrun.workloadImage,
		phase:            podRunState,
		timeout:          podSnooze * time.Second,
		pollInterval:     10 * time.Second,
		attributes:       make(map[string]interface{}),
		annotations:      make(map[string]string),
		volumes:          []VolumeConfig{},
	}
}

func createKataPodFromDescription(oc *exutil.CLI, pod *PodDescription) error {
	if pod.namespace == "" {
		pod.namespace = oc.Namespace()
	}
	if pod.name == "" {
		pod.name = getRandomString() + "-" + pod.baseName
	}

	configFile, err := processGoTemplate(pod)
	if err != nil {
		return err
	}

	ginkgo.By(fmt.Sprintf("Applying pod configuration from %s", configFile))
	_, err = oc.AsAdmin().WithoutNamespace().Run("apply").Args("-f", configFile, "-n", pod.namespace).Output()
	if err != nil {
		return fmt.Errorf("could not apply configFile %v: %v", configFile, err)
	}

	ginkgo.By(fmt.Sprintf("Checking if pod %v reaches phase %v", pod.name, pod.phase))
	_, err = checkResourceJsonpath(oc, "pod", pod.name, pod.namespace,
		"-o=jsonpath={.status.phase}", pod.phase, pod.timeout, pod.pollInterval)
	if err != nil {
		return fmt.Errorf("pod (%v) did not reach phase %v: %v", pod.name, pod.phase, err)
	}

	actualRC, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
		"pods", pod.name, "-n", pod.namespace, "-o=jsonpath={.spec.runtimeClassName}",
	).Output()
	if err != nil || actualRC != pod.runtimeClassName {
		return fmt.Errorf("pod %v has wrong runtime %v, expecting %v: %v",
			pod.name, actualRC, pod.runtimeClassName, err)
	}

	return nil
}

func processGoTemplate(pod *PodDescription) (string, error) {
	pod.template = getTemplatePath("kataPodDefaultTemplate.go.tmpl")
	configFile := setConfigFilePath(getRandomString()+"Pod-common.yaml", pod.namespace)

	if pod.attributes["name"] == nil {
		pod.attributes["name"] = pod.name
	}
	if pod.attributes["runtimeClassName"] == nil {
		pod.attributes["runtimeClassName"] = pod.runtimeClassName
	}
	if pod.attributes["image"] == nil {
		pod.attributes["image"] = pod.image
	}

	if len(pod.annotations) > 0 && pod.attributes["annotations"] == nil {
		annotationList := make([]string, 0, len(pod.annotations))
		for key, value := range pod.annotations {
			annotationList = append(annotationList, fmt.Sprintf("%s%s: \"%s\"", kataAnnotationPrefix, key, value))
		}
		pod.attributes["annotations"] = annotationList
	}

	if len(pod.volumes) > 0 {
		volumeList := make([]map[string]string, 0, len(pod.volumes))
		for _, vol := range pod.volumes {
			volMap := map[string]string{
				"type": vol.volumeType,
			}
			if vol.name != "" {
				volMap["name"] = vol.name
			}
			if vol.mountPath != "" {
				volMap["mountPath"] = vol.mountPath
			}
			if vol.sizeLimit != "" {
				volMap["sizeLimit"] = vol.sizeLimit
			}
			if vol.medium != "" {
				volMap["medium"] = vol.medium
			}
			volumeList = append(volumeList, volMap)
		}
		pod.attributes["volume"] = volumeList
	}

	err := createConfigFileFromTemplate(pod.attributes, pod.template, configFile)
	if err != nil {
		return "", fmt.Errorf("failed to process Go template: %v", err)
	}

	return configFile, nil
}
