package main

import (
	"context"
	"log"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const runtimeClassName = "kata-remote"

var (
	runtimeClassAvailable = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kata_remote_runtimeclass_available",
		Help: "Indicates if the " + runtimeClassName + " RuntimeClass is available (1) or not (0).",
	})

	kataConfigInstallationSuccess = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kata_config_installation_success",
		Help: "Indicates if KataConfig installation is successful (1) or not (0).",
	})

	kataRemoteWorkloadFailureRatio = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kata_remote_workload_failure_ratio",
		Help: "Percentage of " + runtimeClassName + " workloads that have failed.",
	})

	totalKataRemotePods = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kata_total_remote_pods",
		Help: "Total number of " + runtimeClassName + " pods across all namespaces, regardless of their status.",
	})

	failedKataRemotePods = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kata_failed_remote_pods",
		Help: "Total number of " + runtimeClassName + " pods across all namespaces, that status is != 'Running|Succeed'",
	})
)

func collectMetricsData(clientset *kubernetes.Clientset, dynamicClient dynamic.Interface) {
	// Defaults
	runtimeClassAvailable.Set(0)
	kataRemoteWorkloadFailureRatio.Set(0)
	totalKataRemotePods.Set(0)
	failedKataRemotePods.Set(0)

	// Check if kata-remote runtime class is available
	_, err := clientset.NodeV1().RuntimeClasses().Get(context.TODO(), runtimeClassName, metav1.GetOptions{})
	if err == nil {
		runtimeClassAvailable.Set(1)

		// Fetch Pods for kata-remote workload metrics
		pods, err := clientset.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			log.Printf("Error listing pods: %v", err)
		} else {
			totalPods := 0
			successfulPods := 0
			failedPods := 0
			for _, pod := range pods.Items {
				if pod.Spec.RuntimeClassName != nil && *pod.Spec.RuntimeClassName == runtimeClassName {
					totalPods++
					if pod.Status.Phase == "Running" || pod.Status.Phase == "Succeeded" {
						successfulPods++
					} else {
						failedPods++
					}
				}
			}

			if totalPods > 0 {
				kataRemoteWorkloadFailureRatio.Set(float64(failedPods) / float64(totalPods) * 100)
			}

			totalKataRemotePods.Set(float64(totalPods))
			failedKataRemotePods.Set(float64(failedPods))
		}
	}

	// Fetch KataConfig status
	kataConfigGVR := schema.GroupVersionResource{
		Group:    "kataconfiguration.openshift.io",
		Version:  "v1",
		Resource: "kataconfigs",
	}
	kataConfigs, err := dynamicClient.Resource(kataConfigGVR).List(context.TODO(), metav1.ListOptions{})
	if err != nil || len(kataConfigs.Items) == 0 {
		kataConfigInstallationSuccess.Set(0)
	} else {
		kataConfig := &kataConfigs.Items[0]
		status, found, err := unstructured.NestedMap(kataConfig.Object, "status")
		if err != nil || !found {
			kataConfigInstallationSuccess.Set(0)
		} else {
			inProgress, _, _ := unstructured.NestedBool(status, "inProgress")
			readyNodeCount, _, _ := unstructured.NestedInt64(status, "readyNodeCount")
			totalNodeCount, _, _ := unstructured.NestedInt64(status, "totalNodeCount")
			if !inProgress && readyNodeCount == totalNodeCount {
				kataConfigInstallationSuccess.Set(1)
			} else {
				kataConfigInstallationSuccess.Set(0)
			}
		}
	}
}

func getKubernetesClients() (*kubernetes.Clientset, dynamic.Interface, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, nil, err
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, nil, err
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, nil, err
	}

	return clientset, dynamicClient, nil
}

func metricsHandler(clientset *kubernetes.Clientset, dynamicClient dynamic.Interface) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		collectMetricsData(clientset, dynamicClient)
		promhttp.Handler().ServeHTTP(w, r)
	})
}

func main() {
	prometheus.MustRegister(
		runtimeClassAvailable,
		kataConfigInstallationSuccess,
		kataRemoteWorkloadFailureRatio,
		totalKataRemotePods,
		failedKataRemotePods,
	)

	clientset, dynamicClient, err := getKubernetesClients()
	if err != nil {
		log.Fatalf("Error setting up Kubernetes clients: %v", err)
	}

	http.Handle("/metrics", metricsHandler(clientset, dynamicClient))

	log.Println("Starting OSC metrics server on port :8091")
	log.Fatal(http.ListenAndServe(":8091", nil))
}
