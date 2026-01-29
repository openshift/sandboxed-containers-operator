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
	"path/filepath"

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
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
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

// the following is not required by this controller, it's required by the AWS podvm creation scripts (ami-helper.sh)
//+kubebuilder:rbac:groups=cloudcredential.openshift.io,resources=credentialsrequests,verbs=create;delete;get;list

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
			r.Log.Info("no cco-secret has been found")
			return ctrl.Result{}, nil
		} else {
			r.Log.Info("error in getting cco-secret", "err", err)
			return ctrl.Result{Requeue: true}, nil
		}
	}

	peerPodsSecret, err := getPeerPodsSecret(r.Client)
	if k8serrors.IsNotFound(err) {
		r.Log.Info("peer-pods-secret is not found, trying to map cco-secret")
	} else if err != nil {
		r.Log.Info("error in getting peer-pods secret", "err", err)
		return ctrl.Result{Requeue: true}, nil
	} else if !isCCOFlowSecret(peerPodsSecret) { // not a CCO created secret, shouldn't reach here
		r.Log.Info("unexpected unowned peer-pods-secret exist, skipping CCO secret mapping flow...")
		return ctrl.Result{}, nil
	}

	peerpodsData := r.ccoDataMapping(ccoSecret.Data)
	labels := map[string]string{labelCredentialsRequest: labelCredentialsRequestValue}
	if err := r.createOrUpdateSecret(context.TODO(), peerPodsSecretName, OperatorNamespace, peerpodsData, ccoSecret, labels); err != nil {
		r.Log.Info("error in creating or updating peer-pods secret", "err", err)
		return ctrl.Result{Requeue: true}, nil
	}

	r.Log.Info("cco-secret created and mapped to peer-pods secret", "CCO Secret", ccoSecret.GetName(), "Peer-Pods Secret", peerPodsSecretName)
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
			return e.ObjectNew.GetNamespace() == OperatorNamespace && e.ObjectNew.GetName() == credentialsRequestSecretRefName
		},
		CreateFunc: func(e event.CreateEvent) bool {
			return e.Object.GetNamespace() == OperatorNamespace && e.Object.GetName() == credentialsRequestSecretRefName
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			// cco-secret deletion is done by cloud-credentials-operator, followed by owned peer-pods secret deletion
			return false
			// consider dynamic triggering of cco support using reconciliation against deletion of peer-pods secreta and creation of credentialsRequest
			//return e.Object.GetNamespace() == OperatorNamespace && e.Object.GetName() == peerPodsSecretName
		},
		GenericFunc: func(e event.GenericEvent) bool {
			return false
		},
	}
}

// map ccoSecret fields to peer-pods compatible fields
func (r *SecretReconciler) ccoDataMapping(ccoSecretData map[string][]byte) map[string][]byte {
	ccoToPp := map[string]string{
		"aws_access_key_id":     "AWS_ACCESS_KEY_ID",
		"aws_secret_access_key": "AWS_SECRET_ACCESS_KEY",
		"azure_subscription_id": "AZURE_SUBSCRIPTION_ID",
		"azure_client_id":       "AZURE_CLIENT_ID",
		"azure_client_secret":   "AZURE_CLIENT_SECRET",
		"azure_tenant_id":       "AZURE_TENANT_ID",
		"service_account.json":  "GCP_CREDENTIALS",
		// the following are usually set in them CM, ignore them for now
		//"azure_region":          "AZURE_REGION",
		//"azure_resourcegroup":   "AZURE_RESOURCE_GROUP",
	}

	if len(ccoSecretData) == 0 {
		r.Log.Info("ccoDataMapping: ccoSecret data is uninitialized or empty")
		return nil
	}

	peerPodsSecretData := make(map[string][]byte)

	// mapping is done explicitly to avoid conversion mistakes
	for ccoKey, ppKey := range ccoSecretData {
		if ccoToPp[ccoKey] != "" {
			peerPodsSecretData[ccoToPp[ccoKey]] = ppKey
		}
	}
	return peerPodsSecretData
}

func isCCOFlowSecret(secret *corev1.Secret) bool {
	if secret == nil {
		return false
	}
	// Check if owned by cco-secret with controller=true
	owner := metav1.GetControllerOf(secret)
	return owner != nil && owner.Kind == "Secret" && owner.Name == credentialsRequestSecretRefName
}

// createOrUpdateSecret creates or updates a Secret with the given data, optional owner, and optional labels
// Parameters:
//   - ctx: context for the operation
//   - name: name of the Secret
//   - namespace: namespace of the Secret
//   - data: map of secret data (key-value pairs)
//   - owner: optional owner object for setting owner reference (can be nil)
//   - labels: optional map of labels to add to the Secret (can be nil)
func (r *SecretReconciler) createOrUpdateSecret(ctx context.Context, name, namespace string, data map[string][]byte, owner client.Object, labels map[string]string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		// Set the data
		secret.Data = data
		secret.Type = corev1.SecretTypeOpaque

		// Set labels if provided
		if labels != nil {
			if secret.Labels == nil {
				secret.Labels = make(map[string]string)
			}
			for k, v := range labels {
				secret.Labels[k] = v
			}
		}

		// Set owner reference if provided
		if owner != nil {
			if err := controllerutil.SetOwnerReference(owner, secret, r.Scheme); err != nil {
				return err
			}
		}
		return nil
	})

	return err
}

type KataConfigHandler struct {
	reconciler *SecretReconciler
}

func (kh *KataConfigHandler) Generic(context.Context, event.GenericEvent, workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	kh.reconciler.Log.Info("KataConfig Generic event")
}

// kataConfig created, create credentialRequest if peerPods enabled
func (kh *KataConfigHandler) Create(ctx context.Context, event event.CreateEvent, queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {
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
func (kh *KataConfigHandler) Update(ctx context.Context, event event.UpdateEvent, queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {
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
func (kh *KataConfigHandler) Delete(ctx context.Context, event event.DeleteEvent, queue workqueue.TypedRateLimitingInterface[reconcile.Request]) {
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
	credentialsRequestsYamlFile := filepath.Join(peerpodsCredentialsRequestsPathLocation, fileName)
	yamlData, err := readYamlFile(credentialsRequestsYamlFile)
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
	} else if !isCCOFlowSecret(peerPodsSecret) {
		kh.reconciler.Log.Info("peerPodsSecret has already been created by the user, skipping...")
		return true
	}

	// check if CredentialsRequest implementation exists for the cloud provider
	if provider, err := getCloudProviderFromInfra(kh.reconciler.Client); err == nil {
		fileName := fmt.Sprintf(peerpodsCredentialsRequestFileFormat, provider)
		credentialsRequestsYamlFile := filepath.Join(peerpodsCredentialsRequestsPathLocation, fileName)
		if _, err := readYamlFile(credentialsRequestsYamlFile); os.IsNotExist(err) {
			kh.reconciler.Log.Info("no CredentialsRequest yaml file for provider, skipping", "provider", provider, "filename", fileName)
			return true
		}
	}
	return false
}
