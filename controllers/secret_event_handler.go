package controllers

import (
	"context"

	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const monitorCertSecretName = "kata-monitor-certs"

type SecretEventHandler struct {
	reconciler *KataConfigOpenShiftReconciler
}

func (sh *SecretEventHandler) Create(ctx context.Context, event event.CreateEvent, queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	if sh.reconciler.kataConfig == nil {
		return
	}

	secret := event.Object
	if secret.GetNamespace() != OperatorNamespace || secret.GetName() != monitorCertSecretName {
		return
	}

	log := sh.reconciler.Log.WithName("SecretCreate").WithValues("secret name", secret.GetName())
	log.Info("kata-monitor-certs secret created")

	queue.Add(sh.reconciler.makeReconcileRequest())
}

func (sh *SecretEventHandler) Update(ctx context.Context, event event.UpdateEvent, queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	if sh.reconciler.kataConfig == nil {
		return
	}

	secret := event.ObjectNew
	if secret.GetNamespace() != OperatorNamespace || secret.GetName() != monitorCertSecretName {
		return
	}

	if event.ObjectOld.GetResourceVersion() == secret.GetResourceVersion() {
		return
	}

	log := sh.reconciler.Log.WithName("SecretUpdate").WithValues("secret name", secret.GetName())
	log.Info("kata-monitor-certs secret updated")

	queue.Add(sh.reconciler.makeReconcileRequest())
}

func (sh *SecretEventHandler) Delete(ctx context.Context, event event.DeleteEvent, queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	if sh.reconciler.kataConfig == nil {
		return
	}

	secret := event.Object
	if secret.GetNamespace() != OperatorNamespace || secret.GetName() != monitorCertSecretName {
		return
	}

	log := sh.reconciler.Log.WithName("SecretDelete").WithValues("secret name", secret.GetName())
	log.Info("kata-monitor-certs secret deleted")

	queue.Add(sh.reconciler.makeReconcileRequest())
}

func (sh *SecretEventHandler) Generic(ctx context.Context, event event.GenericEvent, queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {
}
