package kata

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"text/template"
	"time"

	"github.com/onsi/ginkgo/v2"
)

const (
	podSnooze            time.Duration = 600
	podDeleteTimeout     time.Duration = 900
	resSnoose            time.Duration = 300
	podRunState                        = "Running"
	kataocLabel                        = "node-role.kubernetes.io/kata-oc"
	kataAnnotationPrefix               = "io.katacontainers.config.hypervisor."

	opNamespace          = "openshift-sandboxed-containers-operator"
	testrunConfigmapNs   = "default"
	testrunConfigmapName = "osc-config"
)

var (
	allowedRuntimeClasses = [2]string{"kata", "kata-remote"}
	allowedWorkloadTypes  = [3]string{"kata", "peer-pods", "coco"}
)

// Logf writes a timestamped message to the ginkgo test output.
func Logf(format string, args ...interface{}) {
	_, _ = fmt.Fprintf(ginkgo.GinkgoWriter, time.Now().Format(time.RFC3339)+" "+format+"\n", args...)
}

// Failf fails the current test with a formatted message.
func Failf(format string, args ...interface{}) {
	ginkgo.Fail(fmt.Sprintf(format, args...))
}

func getRandomString() string {
	chars := "abcdefghijklmnopqrstuvwxyz0123456789"
	buffer := make([]byte, 8)
	for index := range buffer {
		buffer[index] = chars[rand.Intn(len(chars))]
	}
	return string(buffer)
}

func getTemplatePath(templateName string) string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "testdata", "kata", templateName)
}

func setConfigFilePath(fileName, namespace string) string {
	return filepath.Join(os.TempDir(), namespace+"-"+fileName)
}

func createConfigFileFromTemplate(attributesConfig map[string]interface{}, templateFile, configFile string) error {
	tmpl, err := template.ParseFiles(templateFile)
	if err != nil {
		return fmt.Errorf("could not parse template file %v: %v", templateFile, err)
	}
	file, err := os.Create(configFile)
	if err != nil {
		return fmt.Errorf("could not create config file %v: %v", configFile, err)
	}
	defer func() { _ = file.Close() }()
	err = tmpl.Execute(file, attributesConfig)
	if err != nil {
		return fmt.Errorf("could not execute template file %v: %v", templateFile, err)
	}
	return nil
}
