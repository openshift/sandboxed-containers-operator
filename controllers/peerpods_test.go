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
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	kataconfigurationv1 "github.com/openshift/sandboxed-containers-operator/api/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

var _ = Describe("CAA DaemonSet reconciliation", func() {
	const (
		testNamespace = "test-peerpods-ns"
		testCAAImage  = "registry.example.com/caa:v1.0"
	)

	var (
		reconciler *KataConfigOpenShiftReconciler
		origImage  string
		origNS     string
		// directClient bypasses the manager cache for reads
		directClient client.Client
	)

	BeforeEach(func() {
		origImage = os.Getenv("RELATED_IMAGE_CAA")
		origNS = os.Getenv("PEERPODS_NAMESPACE")
		os.Setenv("RELATED_IMAGE_CAA", testCAAImage)
		os.Setenv("PEERPODS_NAMESPACE", testNamespace)

		// Create a direct (non-cached) client for test assertions.
		// The manager's k8sClient uses an informer cache that may not
		// reflect writes immediately. For CreateOrUpdate we need k8sClient
		// (which CreateOrUpdate uses internally), but for assertions we
		// use directClient to avoid cache lag.
		var err error
		directClient, err = client.New(cfg, client.Options{Scheme: k8sManager.GetScheme()})
		Expect(err).ToNot(HaveOccurred())

		// Create test namespace (idempotent)
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: testNamespace},
		}
		_ = directClient.Create(context.TODO(), ns)

		// Ensure Infrastructure "cluster" resource exists with Azure platform status.
		// This is cluster-scoped and shared across tests; not deleted in AfterEach.
		infra := &configv1.Infrastructure{}
		err = directClient.Get(context.TODO(), types.NamespacedName{Name: "cluster"}, infra)
		if k8serrors.IsNotFound(err) {
			infra = &configv1.Infrastructure{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Spec: configv1.InfrastructureSpec{
					PlatformSpec: configv1.PlatformSpec{
						Type: configv1.AzurePlatformType,
					},
				},
			}
			Expect(directClient.Create(context.TODO(), infra)).To(Succeed())
			infra.Status.PlatformStatus = &configv1.PlatformStatus{
				Type: configv1.AzurePlatformType,
			}
			infra.Status.ControlPlaneTopology = configv1.HighlyAvailableTopologyMode
			infra.Status.InfrastructureTopology = configv1.HighlyAvailableTopologyMode
			Expect(directClient.Status().Update(context.TODO(), infra)).To(Succeed())
		} else {
			Expect(err).ToNot(HaveOccurred())
		}

		// Create reconciler with in-memory KataConfig (UID set for owner reference)
		reconciler = &KataConfigOpenShiftReconciler{
			Client: directClient,
			Log:    ctrl.Log.WithName("test").WithName("peerpods"),
			Scheme: k8sManager.GetScheme(),
			kataConfig: &kataconfigurationv1.KataConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name: "example-kataconfig",
					UID:  types.UID("test-uid-12345"),
				},
			},
		}
	})

	AfterEach(func() {
		// Clean up DaemonSet
		ds := &appsv1.DaemonSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      caaDsName,
				Namespace: testNamespace,
			},
		}
		_ = directClient.Delete(context.TODO(), ds)

		// Restore env
		if origImage != "" {
			os.Setenv("RELATED_IMAGE_CAA", origImage)
		} else {
			os.Unsetenv("RELATED_IMAGE_CAA")
		}
		if origNS != "" {
			os.Setenv("PEERPODS_NAMESPACE", origNS)
		} else {
			os.Unsetenv("PEERPODS_NAMESPACE")
		}
	})

	Context("mutateCAADaemonSet idempotency", func() {
		It("should produce identical spec when applied twice via CreateOrUpdate", func() {
			ds := &appsv1.DaemonSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      caaDsName,
					Namespace: testNamespace,
				},
			}

			// First call: creates the DaemonSet
			result, err := controllerutil.CreateOrUpdate(context.TODO(), directClient, ds, func() error {
				return reconciler.mutateCAADaemonSet(ds)
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(controllerutil.OperationResultCreated))

			// Read back the DS to get server-applied defaults
			createdDS := &appsv1.DaemonSet{}
			Eventually(func() error {
				return directClient.Get(context.TODO(), types.NamespacedName{
					Name:      caaDsName,
					Namespace: testNamespace,
				}, createdDS)
			}, 5*time.Second, 100*time.Millisecond).Should(Succeed())
			initialGeneration := createdDS.Generation

			// Second call: should detect no changes (unchanged)
			result, err = controllerutil.CreateOrUpdate(context.TODO(), directClient, createdDS, func() error {
				return reconciler.mutateCAADaemonSet(createdDS)
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(controllerutil.OperationResultNone),
				"second CreateOrUpdate should be a no-op but detected changes")

			// Verify generation did not increment
			updatedDS := &appsv1.DaemonSet{}
			Expect(directClient.Get(context.TODO(), types.NamespacedName{
				Name:      caaDsName,
				Namespace: testNamespace,
			}, updatedDS)).To(Succeed())
			Expect(updatedDS.Generation).To(Equal(initialGeneration),
				"DaemonSet generation should not increment on idempotent reconciliation")
		})

		It("should detect and apply legitimate changes", func() {
			ds := &appsv1.DaemonSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      caaDsName,
					Namespace: testNamespace,
				},
			}

			// Create the DaemonSet
			_, err := controllerutil.CreateOrUpdate(context.TODO(), directClient, ds, func() error {
				return reconciler.mutateCAADaemonSet(ds)
			})
			Expect(err).ToNot(HaveOccurred())

			// Change the image to simulate a new operator version
			os.Setenv("RELATED_IMAGE_CAA", "registry.example.com/caa:v2.0")

			// Re-fetch to get current state
			Expect(directClient.Get(context.TODO(), types.NamespacedName{
				Name:      caaDsName,
				Namespace: testNamespace,
			}, ds)).To(Succeed())
			genBefore := ds.Generation

			// Third call: should detect the image change
			result, err := controllerutil.CreateOrUpdate(context.TODO(), directClient, ds, func() error {
				return reconciler.mutateCAADaemonSet(ds)
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(controllerutil.OperationResultUpdated),
				"CreateOrUpdate should detect image change")

			// Verify the image was updated
			Expect(directClient.Get(context.TODO(), types.NamespacedName{
				Name:      caaDsName,
				Namespace: testNamespace,
			}, ds)).To(Succeed())
			Expect(ds.Spec.Template.Spec.Containers[0].Image).To(Equal("registry.example.com/caa:v2.0"))
			Expect(ds.Generation).To(BeNumerically(">", genBefore),
				"generation should increment for a real spec change")
		})
	})

	Context("mutateCAADaemonSet spec correctness", func() {
		It("should set the expected fields on a new DaemonSet", func() {
			ds := &appsv1.DaemonSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      caaDsName,
					Namespace: testNamespace,
				},
			}
			err := reconciler.mutateCAADaemonSet(ds)
			Expect(err).ToNot(HaveOccurred())

			// Owner reference
			Expect(ds.OwnerReferences).To(HaveLen(1))
			Expect(ds.OwnerReferences[0].Name).To(Equal("example-kataconfig"))

			// Selector
			Expect(ds.Spec.Selector.MatchLabels).To(HaveKeyWithValue("name", caaDsName))

			// Container
			Expect(ds.Spec.Template.Spec.Containers).To(HaveLen(1))
			container := ds.Spec.Template.Spec.Containers[0]
			Expect(container.Name).To(Equal("caa-pod"))
			Expect(container.Image).To(Equal(testCAAImage))
			Expect(container.Command).To(Equal([]string{"/usr/local/bin/entrypoint.sh"}))

			// Env - NODE_NAME with explicit APIVersion
			Expect(container.Env).To(HaveLen(1))
			Expect(container.Env[0].Name).To(Equal("NODE_NAME"))
			Expect(container.Env[0].ValueFrom.FieldRef.APIVersion).To(Equal("v1"))

			// EnvFrom
			Expect(container.EnvFrom).To(HaveLen(2))

			// Volumes - base (4) volumes for non-STS Azure
			Expect(ds.Spec.Template.Spec.Volumes).To(HaveLen(4))

			// HostNetwork
			Expect(ds.Spec.Template.Spec.HostNetwork).To(BeTrue())

			// NodeSelector
			Expect(ds.Spec.Template.Spec.NodeSelector).To(
				HaveKeyWithValue("node-role.kubernetes.io/kata-oc", ""))
		})

		It("should not change the selector on an existing DaemonSet", func() {
			ds := &appsv1.DaemonSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      caaDsName,
					Namespace: testNamespace,
				},
			}
			// First mutation sets selector
			err := reconciler.mutateCAADaemonSet(ds)
			Expect(err).ToNot(HaveOccurred())

			// Simulate an existing selector (as if API server set it)
			existingSelector := ds.Spec.Selector.DeepCopy()

			// Second mutation should not overwrite it
			err = reconciler.mutateCAADaemonSet(ds)
			Expect(err).ToNot(HaveOccurred())
			Expect(ds.Spec.Selector).To(Equal(existingSelector))
		})

		It("should set HostPath Type explicitly to avoid server-default diffs", func() {
			ds := &appsv1.DaemonSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:      caaDsName,
					Namespace: testNamespace,
				},
			}
			err := reconciler.mutateCAADaemonSet(ds)
			Expect(err).ToNot(HaveOccurred())

			for _, vol := range ds.Spec.Template.Spec.Volumes {
				if vol.VolumeSource.HostPath != nil {
					Expect(vol.VolumeSource.HostPath.Type).ToNot(BeNil(),
						"HostPath.Type for volume %q must be explicitly set", vol.Name)
					Expect(*vol.VolumeSource.HostPath.Type).To(Equal(corev1.HostPathUnset),
						"HostPath.Type for volume %q should be HostPathUnset", vol.Name)
				}
			}
		})
	})
})
