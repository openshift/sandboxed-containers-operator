/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"fmt"
	"os"

	"github.com/go-logr/logr"
	v1 "github.com/openshift/cloud-credential-operator/pkg/apis/cloudcredential/v1"
	kataconfigurationv1 "github.com/openshift/sandboxed-containers-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// SecretReconciler reconciles a Secret object
type SecretReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

const (
	credentialsRequestSecretRefName         = "cco-secret"
	peerpodsCredentialsRequestsPathLocation = "/config/peerpods/credentials-requests"
	peerpodsCredentialsRequestFileFormat    = "credentials_request_%s.yaml"

	// labelCredentialsRequest is to mark Secrets as created using cloud-credentials-operator
	labelCredentialsRequest      = "kataconfiguration.openshift.io/credentials-request-based"
	labelCredentialsRequestValue = "true"
)

//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=secrets/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core,resources=secrets/finalizers,verbs=update
//+kubebuilder:rbac:groups=cloudcredential.openshift.io,resources=credentialsrequests,verbs=create;delete

// TODO: reduce secret's RBAC if possible

// Reconciles cco-secret secret based on the secretsFilterPredicate and maps the cco-secret
// created by the cloud-credentials-operator to peer-pods compatible secret
// KataConfigs are handled by the KataConfigHandler to create/delete credentialRequests from cloud-credentials-operator
// see: https://github.com/openshift/cloud-credential-operator/tree/master?tab=readme-ov-file#openshift-cloud-credential-operator
func (r *SecretReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)
	r.Log.Info("reconciling Secret for OpenShift Sandboxed Containers", "secret", req.Name)

	ccoSecret := &corev1.Secret{}
	if err := r.Client.Get(context.TODO(), req.NamespacedName, ccoSecret); err != nil {
		if k8serrors.IsNotFound(err) {
			r.Log.Info("cco-secret not found")
			return ctrl.Result{}, nil
		} else {
			r.Log.Info("error in getting cco-secret", "err", err)
			return ctrl.Result{Requeue: true}, nil
		}
	}

	peerPodsSecret, err := getPeerPodsSecret(r.Client)
	if k8serrors.IsNotFound(err) {
		peerPodsSecret = r.newOwnedPeerPodsSecret(ccoSecret)
	} else if err != nil {
		r.Log.Info("error in getting peer-pods secret", "err", err)
		return ctrl.Result{Requeue: true}, nil
	}

	// skip if peer-pods-secret was created by the user
	if !isControllerGenerated(peerPodsSecret) {
		r.Log.Info("peerPodsSecret has been created by the user, skipping...")
		return ctrl.Result{}, nil
	}

	if _, err := controllerutil.CreateOrUpdate(context.TODO(), r.Client, peerPodsSecret, func() error {
		r.secretMapping(peerPodsSecret, ccoSecret)
		return nil
	}); err != nil {
		r.Log.Info("error in creating or updating peer-pods secret", "err", err)
		return ctrl.Result{Requeue: true}, nil
	}

	r.Log.Info("cco-secret created and mapped to peer-pods secret", "CCO Secret", ccoSecret.GetName(), "Peer-Pods Secret", peerPodsSecret.GetName())
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SecretReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).Named("credentials-controller").
		Watches(
			&corev1.Secret{},
			&handler.EnqueueRequestForObject{}, builder.WithPredicates(secretsFilterPredicate())).
		Watches(&kataconfigurationv1.KataConfig{},
			&KataConfigHandler{r}).
		Complete(r)
}

func secretsFilterPredicate() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool { // to handle Secret rotation
			return e.ObjectNew.GetNamespace() == "openshift-sandboxed-containers-operator" && e.ObjectNew.GetName() == credentialsRequestSecretRefName
		},
		CreateFunc: func(e event.CreateEvent) bool {
			return e.Object.GetNamespace() == "openshift-sandboxed-containers-operator" && e.Object.GetName() == credentialsRequestSecretRefName
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			// cco-secret deletion is done by cloud-credentials-operator, followed by owned peer-pods secret deletion
			return false
			// consider dynamic triggering of cco support using reconciliation against deletion of peer-pods secreta and creation of credentialsRequest
			//return e.Object.GetNamespace() == "openshift-sandboxed-containers-operator" && e.Object.GetName() == peerPodsSecretName
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
	}
}

// map ccoSecret fields to peer-pods compatible fields and set to peerPodsSecret
func (r *SecretReconciler) secretMapping(peerPodsSecret *corev1.Secret, ccoSecret *corev1.Secret) {
	ccoToPp := map[string]string{
		"aws_access_key_id":     "AWS_ACCESS_KEY_ID",
		"aws_secret_access_key": "AWS_SECRET_ACCESS_KEY",
		"azure_subscription_id": "AZURE_SUBSCRIPTION_ID",
		"azure_client_id":       "AZURE_CLIENT_ID",
		"azure_client_secret":   "AZURE_CLIENT_SECRET",
		"azure_tenant_id":       "AZURE_TENANT_ID",
		// the following are usually set in them CM, ignore them for now
		//"azure_region":          "AZURE_REGION",
		//"azure_resourcegroup":   "AZURE_RESOURCE_GROUP",
	}

	if peerPodsSecret.Data == nil {
		r.Log.Info("secretMapping: peerPodsSecret data is uninitialized")
		return
	}

	if len(ccoSecret.Data) == 0 {
		r.Log.Info("secretMapping: ccoSecret data is uninitialized or empty")
		return
	}

	// mapping is done explicitly to avoid conversion mistakes
	for ccoKey, ppKey := range ccoSecret.Data {
		if ccoToPp[ccoKey] != "" {
			peerPodsSecret.Data[ccoToPp[ccoKey]] = ppKey
		}
	}
}

func (r *SecretReconciler) newOwnedPeerPodsSecret(ownedSecret *corev1.Secret) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            peerPodsSecretName,
			Namespace:       "openshift-sandboxed-containers-operator",
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(ownedSecret, corev1.SchemeGroupVersion.WithKind("Secret"))},
			Labels: map[string]string{
				labelCredentialsRequest: labelCredentialsRequestValue, // used to mark it's owned by a secret (cco-secret) created by cloud-credentials-operator
			},
		},
		Data: make(map[string][]byte),
	}
}

func isControllerGenerated(secret *corev1.Secret) bool {
	return secret != nil && secret.Labels != nil && secret.Labels[labelCredentialsRequest] == labelCredentialsRequestValue
}

type KataConfigHandler struct {
	reconciler *SecretReconciler
}

func (kh *KataConfigHandler) Generic(context.Context, event.GenericEvent, workqueue.RateLimitingInterface) {
	kh.reconciler.Log.Info("KataConfig Generic event")
}

// kataConfig created, create credentialRequest if peerPods enabled
func (kh *KataConfigHandler) Create(ctx context.Context, event event.CreateEvent, queue workqueue.RateLimitingInterface) {
	kh.reconciler.Log.Info("KataConfig Create event")
	if !event.Object.(*kataconfigurationv1.KataConfig).Spec.EnablePeerPods {
		return
	}

	// consider checking if user's secret exists
	if err := kh.createCredentialsRequests(); err != nil {
		kh.reconciler.Log.Info("error in creating credentialsRequests", "err", err) // invalid logging
	}

}

// kataConfig updated, create/delete credentialRequest if peerPods enabled/disabled
func (kh *KataConfigHandler) Update(ctx context.Context, event event.UpdateEvent, queue workqueue.RateLimitingInterface) {
	kh.reconciler.Log.Info("KataConfig Update event")
	if event.ObjectNew.(*kataconfigurationv1.KataConfig).Spec.EnablePeerPods {
		if err := kh.createCredentialsRequests(); err != nil {
			kh.reconciler.Log.Info("error in creating credentialsRequests", "err", err)
		}
	} else {
		if err := kh.deleteCredentialsRequests(); err != nil {
			kh.reconciler.Log.Info("error in deleting credentialsRequests", "err", err)
		}
	}
}

// kataConfig deleted, delete credentialRequest if peerPods enabled
func (kh *KataConfigHandler) Delete(ctx context.Context, event event.DeleteEvent, queue workqueue.RateLimitingInterface) {
	kh.reconciler.Log.Info("KataConfig Delete event")
	if !event.Object.(*kataconfigurationv1.KataConfig).Spec.EnablePeerPods {
		return // try anyway?
	}
	if err := kh.deleteCredentialsRequests(); err != nil {
		kh.reconciler.Log.Info("error in deleting credentialsRequests", "err", err)
	}
}

// create credentialRequests for all supported providers
func (kh *KataConfigHandler) createCredentialsRequests() error {
	if kh.skipCredentialRequests() {
		return nil
	}

	credentialsRequest, err := kh.getCredentialsRequest()
	if err != nil {
		return err
	}

	if credentialsRequest == nil {
		return nil // skip silently
	}

	if err := kh.reconciler.Client.Create(context.TODO(), credentialsRequest); err != nil {
		if k8serrors.IsAlreadyExists(err) {
			return nil
		} else {
			return err
		}
	}
	kh.reconciler.Log.Info("credentialRequest created", "credentialsRequestName", credentialsRequest.Name)
	return nil
}

// delete credentialRequests for all supported providers
func (kh *KataConfigHandler) deleteCredentialsRequests() error {
	credentialsRequest, err := kh.getCredentialsRequest()
	if err != nil {
		return err
	}

	if credentialsRequest == nil {
		return nil // skip silently
	}

	if err := kh.reconciler.Client.Delete(context.TODO(), credentialsRequest); err != nil {
		if k8serrors.IsNotFound(err) || k8serrors.IsGone(err) {
			return nil
		} else {
			return err
		}
	}
	kh.reconciler.Log.Info("credentialRequest deleted", "credentialsRequestName", credentialsRequest.Name)
	return nil
}

// read and parse credentialsRequest YAMLs for all supported providers and return a slice of credentialsRequests
func (kh *KataConfigHandler) getCredentialsRequest() (*v1.CredentialsRequest, error) {
	provider, err := getCloudProviderFromInfra(kh.reconciler.Client)
	if err != nil {
		kh.reconciler.Log.Info("error in getting cloud provider from infrastructure", "err", err)
		return nil, err
	}

	fileName := fmt.Sprintf(peerpodsCredentialsRequestFileFormat, provider)
	yamlData, err := readCredentialsRequestYAML(fileName)
	if os.IsNotExist(err) {
		kh.reconciler.Log.Info("no CredentialsRequestYAML for provider", "err", err, "provider", provider)
		return nil, nil
	} else if err != nil {
		kh.reconciler.Log.Info("error in reading CredentialsRequestYAML", "err", err)
		return nil, err
	}

	credentialsRequest, err := parseCredentialsRequestYAML(yamlData)
	if err != nil {
		kh.reconciler.Log.Info("error in parsing CredentialsRequestYAML", "err", err)
		return nil, err
	}
	return credentialsRequest, nil
}

func (kh *KataConfigHandler) skipCredentialRequests() bool {
	// check if peer-pods secret was already created by the user
	if peerPodsSecret, err := getPeerPodsSecret(kh.reconciler.Client); err != nil {
		if !(k8serrors.IsNotFound(err) || k8serrors.IsGone(err)) {
			kh.reconciler.Log.Info("failed to get peer-pods Secret skipping peer-pods Secret check", "error", err)
		}
	} else if !isControllerGenerated(peerPodsSecret) {
		kh.reconciler.Log.Info("peerPodsSecret has already been created by the user, skipping...")
		return true
	}

	// check if CredentialsRequest implementation exists for the cloud provider
	if provider, err := getCloudProviderFromInfra(kh.reconciler.Client); err == nil {
		fileName := fmt.Sprintf(peerpodsCredentialsRequestFileFormat, provider)
		if _, err := readCredentialsRequestYAML(fileName); os.IsNotExist(err) {
			kh.reconciler.Log.Info("no CredentialsRequest yaml file for provider, skipping", "provider", provider, "filename", fileName)
			return true
		}
	}
	return false
}
