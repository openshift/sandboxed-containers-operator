// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controllers

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	nodeapi "k8s.io/api/node/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// migratePeerPodsLimit moves the PeerPodConfig "Limit" value to peer-pods-cm
func (r *KataConfigOpenShiftReconciler) migratePeerPodsLimit() error {
	peerPodConfig := &unstructured.Unstructured{}
	peerPodConfig.SetAPIVersion("confidentialcontainers.org/v1alpha1")
	peerPodConfig.SetKind("PeerPodConfig")

	err := r.Client.Get(context.TODO(), client.ObjectKey{
		Name:      "peerpodconfig-openshift",
		Namespace: OperatorNamespace,
	}, peerPodConfig)

	if err != nil {
		if meta.IsNoMatchError(err) {
			r.Log.Info("PeerPodConfig CRD is unknown, skipping migration")
			return nil
		}
		if k8serrors.IsNotFound(err) {
			r.Log.Info("No PeerPodConfig found, skipping migration")
			return nil
		}
		return err
	}

	limitValue, found, _ := unstructured.NestedString(peerPodConfig.Object, "spec", "limit")
	if !found {
		r.Log.Info("spec.limit not found, skipping migration, in favor of default value.")
		r.Log.Info("Removing deprecated PeerPodConfig...")
		return r.Client.Delete(context.TODO(), peerPodConfig)
	}

	configMap := &corev1.ConfigMap{}
	err = r.Client.Get(context.TODO(), client.ObjectKey{
		Name:      "peer-pods-cm",
		Namespace: OperatorNamespace,
	}, configMap)

	if err != nil {
		if k8serrors.IsNotFound(err) {
			r.Log.Info("No peer-pods-cm found, skipping migration")
			r.Log.Info("Removing deprecated PeerPodConfig...")
			return r.Client.Delete(context.TODO(), peerPodConfig)
		}
		return err
	}

	configMap.Data["PEERPODS_LIMIT_PER_NODE"] = limitValue
	err = r.Client.Update(context.TODO(), configMap)
	if err != nil {
		return err
	}

	err = r.Client.Delete(context.TODO(), peerPodConfig)
	if err != nil {
		return err
	}

	r.Log.Info("Successfully migrated PeerPodConfig Limit to peer-pods-cm and deleted PeerPodConfig")
	return nil
}

// ensureRuntimeClassFinalizers adds finalizers to existing runtime classes that don't have them
func (r *KataConfigOpenShiftReconciler) ensureRuntimeClassFinalizers() error {
	runtimeClasses := []string{
		kataCCRuntimeClassName,
		peerpodsRuntimeClassName,
		kataRuntimeClassName,
	}

	for _, name := range runtimeClasses {
		rc := &nodeapi.RuntimeClass{}
		err := r.Client.Get(context.TODO(), types.NamespacedName{Name: name}, rc)
		if k8serrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return err
		}

		if !controllerutil.ContainsFinalizer(rc, runtimeClassFinalizerName) {
			r.Log.Info("Adding finalizer to existing runtime class", "runtimeClass", name)
			controllerutil.AddFinalizer(rc, runtimeClassFinalizerName)
			if err := r.Client.Update(context.TODO(), rc); err != nil {
				return err
			}
		}
	}
	return nil
}
