package controllers

import (
	"context"
	"reflect"

	corev1 "k8s.io/api/core/v1"
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

	// Check if the configMap name is relevant to the operator
	if cm.GetNamespace() != OperatorNamespace || !isConfigMapRelevant(cm.GetName()) {
		return
	}
	log := ch.reconciler.Log.WithName("CMCreate").WithValues("cm name", cm.GetName())
	log.Info("FeatureGates configMap created")

	queue.Add(ch.reconciler.makeReconcileRequest())
}

func (ch *ConfigMapEventHandler) Update(ctx context.Context, event event.UpdateEvent, queue workqueue.RateLimitingInterface) {

	if ch.reconciler.kataConfig == nil {
		return
	}

	cm := event.ObjectNew
	cmOld := event.ObjectOld

	// Check if the configMap name is relevant to the operator
	if cm.GetNamespace() != OperatorNamespace || !isConfigMapRelevant(cm.GetName()) {
		return
	}

	log := ch.reconciler.Log.WithName("CMUpdate").WithValues("cm name", cm.GetName())
	log.Info("FeatureGates configMap updated")

	// Check if the configMap data has actually changed
	// Otherwise we don't need to do anything
	dataOld := cmOld.DeepCopyObject().(*corev1.ConfigMap).Data
	dataNew := cm.DeepCopyObject().(*corev1.ConfigMap).Data
	if reflect.DeepEqual(dataOld, dataNew) {
		log.Info("No change in configMap data")
		return
	}

	queue.Add(ch.reconciler.makeReconcileRequest())

}

func (ch *ConfigMapEventHandler) Delete(ctx context.Context, event event.DeleteEvent, queue workqueue.RateLimitingInterface) {
	if ch.reconciler.kataConfig == nil {
		return
	}

	cm := event.Object

	/// Check if the configMap name is relevant to the operator
	if cm.GetNamespace() != OperatorNamespace || !isConfigMapRelevant(cm.GetName()) {
		return
	}
	log := ch.reconciler.Log.WithName("CMDelete").WithValues("cm name", cm.GetName())
	log.Info("FeatureGates configMap deleted")

	queue.Add(ch.reconciler.makeReconcileRequest())
}

func (ch *ConfigMapEventHandler) Generic(ctx context.Context, event event.GenericEvent, queue workqueue.RateLimitingInterface) {
}
