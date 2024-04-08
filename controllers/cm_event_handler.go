package controllers

import (
	"context"

	"github.com/openshift/sandboxed-containers-operator/internal/featuregates"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

type ConfigMapEventHandler struct {
	reconciler *KataConfigOpenShiftReconciler
}

func (ch *ConfigMapEventHandler) Create(ctx context.Context, event event.CreateEvent, queue workqueue.RateLimitingInterface) {

	if ch.reconciler.kataConfig == nil {
		return
	}

	cm := event.Object

	// Check if the configmap name is one of the feature gates configmap or
	// feature config
	if cm.GetNamespace() != OperatorNamespace || !featuregates.IsFeatureGateConfigMap(cm.GetName()) {
		return
	}
	log := ch.reconciler.Log.WithName("CMCreate").WithValues("cm name", cm.GetName())
	log.Info("FeatureGates configmap created")

	queue.Add(ch.reconciler.makeReconcileRequest())
}

func (ch *ConfigMapEventHandler) Update(ctx context.Context, event event.UpdateEvent, queue workqueue.RateLimitingInterface) {

	if ch.reconciler.kataConfig == nil {
		return
	}

	cm := event.ObjectNew

	// Check if the configmap name is one of the feature gates configmap or
	// feature config
	if cm.GetNamespace() != OperatorNamespace || !featuregates.IsFeatureGateConfigMap(cm.GetName()) {
		return
	}

	log := ch.reconciler.Log.WithName("CMUpdate").WithValues("cm name", cm.GetName())
	log.Info("FeatureGates configmap updated")

	queue.Add(ch.reconciler.makeReconcileRequest())

}

func (ch *ConfigMapEventHandler) Delete(ctx context.Context, event event.DeleteEvent, queue workqueue.RateLimitingInterface) {
}

func (ch *ConfigMapEventHandler) Generic(ctx context.Context, event event.GenericEvent, queue workqueue.RateLimitingInterface) {
}
