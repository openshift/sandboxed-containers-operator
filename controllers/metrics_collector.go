package controllers

import (
	"context"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	nodeapi "k8s.io/api/node/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	kataconfigurationv1 "github.com/openshift/sandboxed-containers-operator/api/v1"
)

const kataRemoteRuntimeClass = "kata-remote"

var oscMetricsLog = ctrl.Log.WithName("osc-metrics-collector")

var (
	descRuntimeClassAvailable = prometheus.NewDesc(
		"kata_remote_runtimeclass_available",
		"Indicates if the kata-remote RuntimeClass is available (1) or not (0).", nil, nil)
	descKataConfigSuccess = prometheus.NewDesc(
		"kata_config_installation_success",
		"Indicates if KataConfig installation is successful (1) or not (0).", nil, nil)
	descFailureRatio = prometheus.NewDesc(
		"kata_remote_workload_failure_ratio",
		"Percentage of kata-remote workloads that have failed.", nil, nil)
	descTotalPods = prometheus.NewDesc(
		"kata_total_remote_pods",
		"Total number of kata-remote pods across all namespaces.", nil, nil)
	descFailedPods = prometheus.NewDesc(
		"kata_failed_remote_pods",
		"Total number of kata-remote pods that are not Running or Succeeded.", nil, nil)
	descSetupInfo = prometheus.NewDesc(
		"osc_setup_info",
		"Info-style metric indicating the setup type, cloud provider, and TEE silicon when a KataConfig exists.",
		[]string{"setup_type", "cloud_provider", "tee_type"}, nil)
)

// OscMetricsCollector implements prometheus.Collector and exposes kata_* metrics
// on each scrape using the manager's cache — preserving the pull-model semantics
// of the standalone metrics server without a separate HTTP endpoint.
type OscMetricsCollector struct {
	client client.Client
}

func (c *OscMetricsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- descRuntimeClassAvailable
	ch <- descKataConfigSuccess
	ch <- descFailureRatio
	ch <- descTotalPods
	ch <- descFailedPods
	ch <- descSetupInfo
}

func (c *OscMetricsCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rcAvail := 0.0
	if err := c.client.Get(ctx, client.ObjectKey{Name: kataRemoteRuntimeClass}, &nodeapi.RuntimeClass{}); err == nil {
		rcAvail = 1.0
	} else if !k8serrors.IsNotFound(err) {
		oscMetricsLog.Error(err, "failed to get kata-remote RuntimeClass")
	}
	ch <- prometheus.MustNewConstMetric(descRuntimeClassAvailable, prometheus.GaugeValue, rcAvail)

	var totalPods, failedPods float64
	if rcAvail == 1.0 {
		podList := &corev1.PodList{}
		if err := c.client.List(ctx, podList, client.MatchingFields{"spec.runtimeClassName": kataRemoteRuntimeClass}); err != nil {
			oscMetricsLog.Error(err, "failed to list kata-remote pods")
		} else {
			for _, pod := range podList.Items {
				totalPods++
				if pod.Status.Phase != corev1.PodRunning && pod.Status.Phase != corev1.PodSucceeded {
					failedPods++
				}
			}
		}
	}
	ratio := 0.0
	if totalPods > 0 {
		ratio = failedPods / totalPods * 100
	}
	ch <- prometheus.MustNewConstMetric(descTotalPods, prometheus.GaugeValue, totalPods)
	ch <- prometheus.MustNewConstMetric(descFailedPods, prometheus.GaugeValue, failedPods)
	ch <- prometheus.MustNewConstMetric(descFailureRatio, prometheus.GaugeValue, ratio)

	success := 0.0
	kataConfigList := &kataconfigurationv1.KataConfigList{}
	if err := c.client.List(ctx, kataConfigList); err != nil {
		oscMetricsLog.Error(err, "failed to list KataConfig")
	} else if len(kataConfigList.Items) > 0 {
		kc := kataConfigList.Items[0]
		inProgress := false
		for _, cond := range kc.Status.Conditions {
			if cond.Type == kataconfigurationv1.KataConfigInProgress && cond.Status == "True" {
				inProgress = true
				break
			}
		}
		nodes := kc.Status.KataNodes
		if !inProgress && nodes.NodeCount > 0 && nodes.ReadyNodeCount == nodes.NodeCount {
			success = 1.0
		}

		setupType := c.computeSetupType(ctx, &kc)
		cloudProvider := c.computeCloudProvider(ctx)
		teeType := c.computeTEEType(ctx)
		ch <- prometheus.MustNewConstMetric(descSetupInfo, prometheus.GaugeValue, 1.0, setupType, cloudProvider, teeType)
	}
	ch <- prometheus.MustNewConstMetric(descKataConfigSuccess, prometheus.GaugeValue, success)
}

func (c *OscMetricsCollector) computeSetupType(ctx context.Context, kc *kataconfigurationv1.KataConfig) string {
	isPeerPods := kc.Spec.EnablePeerPods

	isConfidential := false
	cm := &corev1.ConfigMap{}
	if err := c.client.Get(ctx, client.ObjectKey{Name: FgConfigMapName, Namespace: OperatorNamespace}, cm); err != nil {
		if !k8serrors.IsNotFound(err) {
			oscMetricsLog.Error(err, "failed to get feature gate ConfigMap")
		}
	} else if val, ok := cm.Data[ConfidentialFeatureGate]; ok {
		parsed, perr := strconv.ParseBool(val)
		if perr != nil {
			oscMetricsLog.Error(perr, "invalid confidential feature gate value", "value", val)
		}
		isConfidential = parsed
	}

	switch {
	case isPeerPods && isConfidential:
		return "confidential_peerpods"
	case isPeerPods:
		return "peerpods"
	case isConfidential:
		return "confidential_baremetal"
	default:
		return "baremetal"
	}
}

func (c *OscMetricsCollector) computeCloudProvider(ctx context.Context) string {
	provider, err := getCloudProviderFromInfraWithContext(ctx, c.client)
	if err != nil {
		oscMetricsLog.Error(err, "failed to get cloud provider")
		return "unknown"
	}
	switch provider {
	case "aws", "azure", "gcp", LibvirtProvider, IBMCloudProvider:
		return provider
	case "":
		return "none"
	default:
		return "unknown"
	}
}

func (c *OscMetricsCollector) computeTEEType(ctx context.Context) string {
	teeLabels := []struct {
		label   string
		teeType string
	}{
		{intelTDXNodeLabel, "tdx"},
		{amdSNPNodeLabel, "snp"},
		{ibmSENodeLabel, "se"},
	}
	for _, tee := range teeLabels {
		nodes := &corev1.NodeList{}
		if err := c.client.List(ctx, nodes, client.MatchingLabels{tee.label: "true"}); err != nil {
			oscMetricsLog.Error(err, "failed to list nodes for TEE detection", "label", tee.label)
			continue
		}
		if len(nodes.Items) > 0 {
			return tee.teeType
		}
	}
	return "none"
}

// RegisterOscMetricsCollector registers the collector with the controller-runtime metrics
// registry. Must be called after the manager is created but before mgr.Start().
func RegisterOscMetricsCollector(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &corev1.Pod{}, "spec.runtimeClassName", func(obj client.Object) []string {
		rc := obj.(*corev1.Pod).Spec.RuntimeClassName
		if rc == nil {
			return nil
		}
		return []string{*rc}
	}); err != nil {
		return err
	}
	return ctrlmetrics.Registry.Register(&OscMetricsCollector{client: mgr.GetClient()})
}
