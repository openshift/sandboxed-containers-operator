package kata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	o "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
)

var _ = ginkgo.Describe("[sig-kata] RuntimeClassLabels", ginkgo.Serial, ginkgo.Label("runtimeclass-labels"), func() {
	oc := NewOC("runtimeclass-labels")
	var f *runtimeClassLabelsFixture

	ginkgo.BeforeEach(func() {
		var err error
		f, err = newRuntimeClassLabelsFixture(oc)
		o.Expect(err).NotTo(o.HaveOccurred())
		ginkgo.DeferCleanup(func() { o.Expect(f.cleanup()).To(o.Succeed()) })
	})

	ginkgo.It("applies defaults and overrides to current and future owned classes", func() {
		target := f.classes[0].GetName()
		absent := "e2e-absent-" + getRandomString()
		f.overrideNames = append(f.overrideNames, target, absent)
		ginkgo.By("Configuring shared labels, a per-class override, and an absent class")
		o.Expect(f.configure(func(config map[string]interface{}) {
			labelMap(config, "defaults")[f.sharedKey] = "default"
			labelMap(config, "overrides", target)[f.sharedKey] = "override"
			labelMap(config, "overrides", target)[f.extraKey] = "extra"
			labelMap(config, "overrides", absent)[f.sharedKey] = "absent"
		})).To(o.Succeed())
		foreign, err := f.createClass(false)
		o.Expect(err).NotTo(o.HaveOccurred())

		ginkgo.By("Creating an arbitrary owned class after the configuration is set")
		future, err := f.createClass(true)
		o.Expect(err).NotTo(o.HaveOccurred())
		f.extraOwned = append(f.extraOwned, future.GetName())
		f.waitLabels(map[string]string{f.sharedKey: "default"}, map[string]map[string]string{
			target: {f.sharedKey: "override", f.extraKey: "extra"},
		})

		ginkgo.By("Checking that an unrelated class keeps its own label")
		foreign, err = labelObject(oc, "runtimeclass", foreign.GetName())
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(foreign.GetLabels()).To(o.Equal(map[string]string{f.sharedKey: "foreign"}))
		out, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(
			"runtimeclass", absent, "--ignore-not-found", "-o=json", "--request-timeout=20s",
		).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(out).To(o.BeEmpty(), "an override must not create a RuntimeClass")
	})

	ginkgo.It("repairs drift and removes obsolete keys while preserving other metadata", func() {
		target := f.classes[0].GetName()
		f.overrideNames = append(f.overrideNames, target)
		o.Expect(f.configure(func(config map[string]interface{}) {
			labelMap(config, "defaults")[f.sharedKey] = "default"
			labelMap(config, "overrides", target)[f.sharedKey] = "override"
			labelMap(config, "overrides", target)[f.extraKey] = "extra"
		})).To(o.Succeed())
		overrides := map[string]map[string]string{target: {f.sharedKey: "override", f.extraKey: "extra"}}
		f.waitLabels(map[string]string{f.sharedKey: "default"}, overrides)

		ginkgo.By("Changing one managed value, deleting another, and adding an unrelated label")
		_, err := oc.AsAdmin().WithoutNamespace().Run("label").Args(
			"runtimeclass", target, f.sharedKey+"=drift", f.extraKey+"-", f.unmanagedKey+"=keep",
			"--overwrite", "--request-timeout=20s",
		).Output()
		o.Expect(err).NotTo(o.HaveOccurred())
		f.waitLabels(map[string]string{f.sharedKey: "default"}, overrides)

		ginkgo.By("Removing overrides to check fallback and override-only key removal")
		o.Expect(f.configure(func(config map[string]interface{}) {
			entry := labelMap(config, "overrides", target)
			delete(entry, f.sharedKey)
			delete(entry, f.extraKey)
		})).To(o.Succeed())
		f.waitLabels(map[string]string{f.sharedKey: "default"}, nil)
		ginkgo.By("Removing the default and checking that the unrelated label survives")
		o.Expect(f.configure(func(config map[string]interface{}) {
			delete(labelMap(config, "defaults"), f.sharedKey)
		})).To(o.Succeed())
		f.waitLabels(nil, nil)
		rc, err := labelObject(oc, "runtimeclass", target)
		o.Expect(err).NotTo(o.HaveOccurred())
		o.Expect(rc.GetLabels()).To(o.HaveKeyWithValue(f.unmanagedKey, "keep"))
	})

	ginkgo.It("restores configured labels when an unused RuntimeClass is recreated", func() {
		ginkgo.By("Selecting an operator-created class with no active pods")
		target, err := f.unusedClass()
		o.Expect(err).NotTo(o.HaveOccurred())
		if target == nil {
			ginkgo.Skip("No operator-created RuntimeClass is currently safe to recreate")
		}
		o.Expect(f.configure(func(config map[string]interface{}) {
			labelMap(config, "defaults")[f.sharedKey] = "recreated"
		})).To(o.Succeed())
		f.waitLabels(map[string]string{f.sharedKey: "recreated"}, nil)
		ginkgo.By("Deleting the unused class without bypassing its finalizer")
		unused, err := f.unusedClass()
		o.Expect(err).NotTo(o.HaveOccurred())
		if unused == nil || unused.GetName() != target.GetName() || unused.GetUID() != target.GetUID() {
			ginkgo.Skip("RuntimeClass usage changed before deletion")
		}
		o.Expect(f.deleteClass(target.GetName(), target.GetUID())).To(o.Succeed())
		o.Expect(pollLabelCheck(func() error {
			rc, err := labelObject(oc, "runtimeclass", target.GetName())
			if err != nil {
				return err
			}
			if rc.GetUID() == target.GetUID() || rc.GetDeletionTimestamp() != nil {
				return fmt.Errorf("RuntimeClass %s has not been recreated", target.GetName())
			}
			if rc.GetLabels()[f.sharedKey] != "recreated" {
				return fmt.Errorf("recreated RuntimeClass %s is missing its configured label", target.GetName())
			}
			return nil
		})).To(o.Succeed())
		f.waitLabels(map[string]string{f.sharedKey: "recreated"}, nil)
	})

	ginkgo.It("rejects invalid labels and override names on create and update", func() {
		for _, config := range []map[string]interface{}{
			{"defaults": map[string]interface{}{"invalid key": "true"}},
			{"defaults": map[string]interface{}{f.sharedKey: "invalid value"}},
			{"overrides": map[string]interface{}{"Invalid_Name": map[string]interface{}{f.sharedKey: "true"}}},
		} {
			ginkgo.By("Validating an update without persisting it")
			patch, err := json.Marshal([]map[string]interface{}{{"op": "add", "path": "/spec/runtimeClassLabels", "value": config}})
			o.Expect(err).NotTo(o.HaveOccurred())
			_, err = oc.AsAdmin().WithoutNamespace().Run("patch").Args(
				"kataconfig", f.config.GetName(), "--type=json", "-p", string(patch),
				"--dry-run=server", "--request-timeout=20s",
			).Output()
			o.Expect(err).To(o.HaveOccurred())
			o.Expect(err.Error()).To(o.ContainSubstring("spec.runtimeClassLabels"))

			ginkgo.By("Validating a create without creating a second KataConfig")
			object := map[string]interface{}{
				"apiVersion": "kataconfiguration.openshift.io/v1", "kind": "KataConfig",
				"metadata": map[string]interface{}{"name": "e2e-invalid-" + getRandomString()},
				"spec":     map[string]interface{}{"checkNodeEligibility": false, "runtimeClassLabels": config},
			}
			data, err := json.Marshal(object)
			o.Expect(err).NotTo(o.HaveOccurred())
			_, err = oc.AsAdmin().WithoutNamespace().Run("create").Args(
				"-f", "-", "--dry-run=server", "--request-timeout=20s",
			).Input(string(data)).Output()
			o.Expect(err).To(o.HaveOccurred())
			o.Expect(err.Error()).To(o.ContainSubstring("spec.runtimeClassLabels"))
		}
	})
})

type runtimeClassLabelsFixture struct {
	oc            *CLI
	config        *unstructured.Unstructured
	original      map[string]interface{}
	hadConfig     bool
	classes       []unstructured.Unstructured
	created       map[string]types.UID
	extraOwned    []string
	overrideNames []string
	sharedKey     string
	extraKey      string
	unmanagedKey  string
}

func newRuntimeClassLabelsFixture(oc *CLI) (*runtimeClassLabelsFixture, error) {
	if _, err := oc.AsAdmin().WithoutNamespace().Run("explain").Args(
		"kataconfig.spec.runtimeClassLabels", "--api-version=kataconfiguration.openshift.io/v1", "--request-timeout=20s",
	).Output(); err != nil {
		return nil, fmt.Errorf("install an operator bundle with runtimeClassLabels support: %w", err)
	}
	configs, err := labelObjects(oc, "kataconfig")
	if err != nil {
		return nil, err
	}
	if len(configs) != 1 || configs[0].GetDeletionTimestamp() != nil {
		return nil, fmt.Errorf("expected exactly one active KataConfig, found %d", len(configs))
	}
	if err := checkKataconfigIsCreated(oc, configs[0].GetName()); err != nil {
		return nil, err
	}
	original, found, err := unstructured.NestedMap(configs[0].Object, "spec", "runtimeClassLabels")
	if err != nil {
		return nil, err
	}
	prefix := "e2e-" + getRandomString() + ".sandboxed-containers.openshift.io/"
	f := &runtimeClassLabelsFixture{
		oc: oc, config: &configs[0], original: original, hadConfig: found,
		created: map[string]types.UID{}, sharedKey: prefix + "shared",
		extraKey: prefix + "extra", unmanagedKey: prefix + "unmanaged",
	}
	f.classes, err = f.ownedClasses()
	if err != nil {
		return nil, err
	}
	if len(f.classes) == 0 {
		return nil, fmt.Errorf("KataConfig %s has no active owned RuntimeClasses", f.config.GetName())
	}
	return f, nil
}

func labelObject(oc *CLI, resource, name string) (*unstructured.Unstructured, error) {
	data, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(resource, name, "-o=json", "--request-timeout=20s").Output()
	if err != nil {
		return nil, err
	}
	object := &unstructured.Unstructured{}
	return object, json.Unmarshal([]byte(data), object)
}

func labelObjects(oc *CLI, resource string) ([]unstructured.Unstructured, error) {
	data, err := oc.AsAdmin().WithoutNamespace().Run("get").Args(resource, "-o=json", "--request-timeout=20s").Output()
	if err != nil {
		return nil, err
	}
	list := &unstructured.UnstructuredList{}
	err = json.Unmarshal([]byte(data), list)
	return list.Items, err
}

func (f *runtimeClassLabelsFixture) ownedClasses() ([]unstructured.Unstructured, error) {
	classes, err := labelObjects(f.oc, "runtimeclass")
	if err != nil {
		return nil, err
	}
	owned := []unstructured.Unstructured{}
	for _, rc := range classes {
		owner := metav1.GetControllerOf(&rc)
		if owner != nil && owner.UID == f.config.GetUID() && owner.Name == f.config.GetName() &&
			owner.Kind == "KataConfig" && strings.HasPrefix(owner.APIVersion, "kataconfiguration.openshift.io/") &&
			rc.GetDeletionTimestamp() == nil {
			owned = append(owned, rc)
		}
	}
	slices.SortFunc(owned, func(a, b unstructured.Unstructured) int { return strings.Compare(a.GetName(), b.GetName()) })
	return owned, nil
}

func labelMap(root map[string]interface{}, path ...string) map[string]interface{} {
	for _, key := range path {
		next, ok := root[key].(map[string]interface{})
		if !ok {
			next = map[string]interface{}{}
			root[key] = next
		}
		root = next
	}
	return root
}

func (f *runtimeClassLabelsFixture) configure(change func(map[string]interface{})) error {
	return pollLabelCheck(func() error {
		current, err := labelObject(f.oc, "kataconfig", f.config.GetName())
		if err != nil {
			return err
		}
		if current.GetUID() != f.config.GetUID() || current.GetDeletionTimestamp() != nil {
			return fmt.Errorf("KataConfig was replaced or is being deleted")
		}
		config, found, err := unstructured.NestedMap(current.Object, "spec", "runtimeClassLabels")
		if err != nil {
			return err
		}
		if config == nil {
			config = map[string]interface{}{}
		}
		change(config)
		operation := map[string]interface{}{"op": "add", "path": "/spec/runtimeClassLabels", "value": config}
		if len(config) == 0 && !f.hadConfig {
			if !found {
				return nil
			}
			operation = map[string]interface{}{"op": "remove", "path": "/spec/runtimeClassLabels"}
		}
		patch, err := json.Marshal([]map[string]interface{}{
			{"op": "test", "path": "/metadata/resourceVersion", "value": current.GetResourceVersion()}, operation,
		})
		if err != nil {
			return err
		}
		_, err = f.oc.AsAdmin().WithoutNamespace().Run("patch").Args(
			"kataconfig", current.GetName(), "--type=json", "-p", string(patch), "--request-timeout=20s",
		).Output()
		return err
	})
}

func pollLabelCheck(check func() error) error {
	var lastErr error
	err := wait.PollUntilContextTimeout(context.Background(), 2*time.Second, 3*time.Minute, true, func(context.Context) (bool, error) {
		lastErr = check()
		return lastErr == nil, nil
	})
	if err != nil {
		return fmt.Errorf("RuntimeClass label check timed out: %w", errors.Join(err, lastErr))
	}
	return nil
}

func (f *runtimeClassLabelsFixture) waitLabels(defaults map[string]string, overrides map[string]map[string]string) {
	ginkgo.GinkgoHelper()
	o.Expect(pollLabelCheck(func() error { return f.checkLabels(defaults, overrides) })).To(o.Succeed())
}

func (f *runtimeClassLabelsFixture) checkLabels(defaults map[string]string, overrides map[string]map[string]string) error {
	classes, err := f.ownedClasses()
	if err != nil {
		return err
	}
	seen := map[string]unstructured.Unstructured{}
	for _, rc := range classes {
		seen[rc.GetName()] = rc
		var managed []string
		if value := rc.GetAnnotations()["kataconfiguration.openshift.io/managed-runtimeclass-labels"]; value != "" {
			if err := json.Unmarshal([]byte(value), &managed); err != nil {
				return fmt.Errorf("RuntimeClass %s: invalid managed label annotation: %w", rc.GetName(), err)
			}
		}
		for _, key := range []string{f.sharedKey, f.extraKey} {
			value, expected := defaults[key]
			if override, ok := overrides[rc.GetName()][key]; ok {
				value, expected = override, true
			}
			actual, present := rc.GetLabels()[key]
			if expected != present || actual != value {
				return fmt.Errorf("RuntimeClass %s: %s=%q (present=%t), want %q (present=%t)", rc.GetName(), key, actual, present, value, expected)
			}
			if slices.Contains(managed, key) != expected {
				return fmt.Errorf("RuntimeClass %s: managed key tracking does not match %s", rc.GetName(), key)
			}
		}
	}
	for _, original := range f.classes {
		rc, found := seen[original.GetName()]
		if !found {
			return fmt.Errorf("RuntimeClass %s is missing", original.GetName())
		}
		for key, value := range original.GetLabels() {
			if actual, present := rc.GetLabels()[key]; !present || actual != value {
				return fmt.Errorf("RuntimeClass %s: unrelated label %s changed", rc.GetName(), key)
			}
		}
		for key, value := range original.GetAnnotations() {
			if key == "kataconfiguration.openshift.io/managed-runtimeclass-labels" {
				continue
			}
			if actual, present := rc.GetAnnotations()[key]; !present || actual != value {
				return fmt.Errorf("RuntimeClass %s: unrelated annotation %s changed", rc.GetName(), key)
			}
		}
		for _, field := range []string{"handler", "overhead", "scheduling"} {
			if !reflect.DeepEqual(rc.Object[field], original.Object[field]) {
				return fmt.Errorf("RuntimeClass %s: %s changed", rc.GetName(), field)
			}
		}
	}
	for _, name := range f.extraOwned {
		if _, found := seen[name]; !found {
			return fmt.Errorf("new owned RuntimeClass %s is missing", name)
		}
	}
	return nil
}

func (f *runtimeClassLabelsFixture) createClass(owned bool) (*unstructured.Unstructured, error) {
	rc := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "node.k8s.io/v1", "kind": "RuntimeClass", "handler": f.classes[0].Object["handler"],
	}}
	rc.SetName("e2e-labels-" + getRandomString())
	if owned {
		controller := true
		rc.SetOwnerReferences([]metav1.OwnerReference{{
			APIVersion: "kataconfiguration.openshift.io/v1", Kind: "KataConfig",
			Name: f.config.GetName(), UID: f.config.GetUID(), Controller: &controller,
		}})
	} else {
		rc.SetLabels(map[string]string{f.sharedKey: "foreign"})
	}
	data, err := json.Marshal(rc)
	if err != nil {
		return nil, err
	}
	out, err := f.oc.AsAdmin().WithoutNamespace().Run("create").Args("-f", "-", "-o=json", "--request-timeout=20s").Input(string(data)).Output()
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(out), rc); err != nil {
		return nil, err
	}
	f.created[rc.GetName()] = rc.GetUID()
	return rc, nil
}

func (f *runtimeClassLabelsFixture) deleteClass(name string, uid types.UID) error {
	data, err := json.Marshal(metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}})
	if err != nil {
		return err
	}
	_, err = f.oc.AsAdmin().WithoutNamespace().Run("delete").Args(
		"--raw", "/apis/node.k8s.io/v1/runtimeclasses/"+name, "-f", "-", "--request-timeout=20s",
	).Input(string(data)).Output()
	return err
}

func (f *runtimeClassLabelsFixture) unusedClass() (*unstructured.Unstructured, error) {
	out, err := f.oc.AsAdmin().WithoutNamespace().Run("get").Args(
		"pods", "-A", `-o=jsonpath={range .items[*]}{.spec.runtimeClassName}{"\t"}{.status.phase}{"\n"}{end}`, "--request-timeout=20s",
	).Output()
	if err != nil {
		return nil, err
	}
	used := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.SplitN(line, "\t", 2)
		if len(fields) == 2 && fields[1] != "Succeeded" && fields[1] != "Failed" {
			used[fields[0]] = true
		}
	}
	current, err := labelObject(f.oc, "kataconfig", f.config.GetName())
	if err != nil {
		return nil, err
	}
	managed, _, err := unstructured.NestedStringSlice(current.Object, "status", "runtimeClasses")
	if err != nil {
		return nil, err
	}
	classes, err := f.ownedClasses()
	if err != nil {
		return nil, err
	}
	for i := range classes {
		rc := &classes[i]
		if !slices.Contains(managed, rc.GetName()) || used[rc.GetName()] {
			continue
		}
		var keys []string
		if value := rc.GetAnnotations()["kataconfiguration.openshift.io/managed-runtimeclass-labels"]; value != "" {
			if err := json.Unmarshal([]byte(value), &keys); err != nil {
				return nil, fmt.Errorf("RuntimeClass %s: invalid managed label annotation: %w", rc.GetName(), err)
			}
		}
		// Recreating an object loses unmanaged metadata. Leave those classes alone.
		unmanaged := false
		for key := range rc.GetLabels() {
			unmanaged = unmanaged || !slices.Contains(keys, key)
		}
		for key := range rc.GetAnnotations() {
			unmanaged = unmanaged || key != "kataconfiguration.openshift.io/managed-runtimeclass-labels"
		}
		if !unmanaged {
			return rc, nil
		}
	}
	return nil, nil
}

func (f *runtimeClassLabelsFixture) cleanup() error {
	// Remove this run's keys without reverting other clients' changes.
	var errs []error
	if err := f.configure(func(config map[string]interface{}) {
		defaults := labelMap(config, "defaults")
		delete(defaults, f.sharedKey)
		if _, existed := f.original["defaults"]; !existed && len(defaults) == 0 {
			delete(config, "defaults")
		}
		overrides := labelMap(config, "overrides")
		originalOverrides, _ := f.original["overrides"].(map[string]interface{})
		for _, name := range f.overrideNames {
			entry := labelMap(overrides, name)
			delete(entry, f.sharedKey)
			delete(entry, f.extraKey)
			if _, existed := originalOverrides[name]; !existed && len(entry) == 0 {
				delete(overrides, name)
			}
		}
		if _, existed := f.original["overrides"]; !existed && len(overrides) == 0 {
			delete(config, "overrides")
		}
	}); err != nil {
		errs = append(errs, err)
	}
	for name, uid := range f.created {
		if err := f.deleteClass(name, uid); err != nil {
			errs = append(errs, err)
		}
	}
	f.extraOwned = nil
	classes, err := f.ownedClasses()
	if err != nil {
		errs = append(errs, err)
	}
	for _, rc := range classes {
		if _, exists := rc.GetLabels()[f.unmanagedKey]; !exists {
			continue
		}
		if _, err := f.oc.AsAdmin().WithoutNamespace().Run("label").Args(
			"runtimeclass", rc.GetName(), f.unmanagedKey+"-", "--request-timeout=20s",
		).Output(); err != nil {
			errs = append(errs, err)
		}
	}
	if err := pollLabelCheck(func() error {
		if err := f.checkLabels(nil, nil); err != nil {
			return err
		}
		for name := range f.created {
			out, err := f.oc.AsAdmin().WithoutNamespace().Run("get").Args(
				"runtimeclass", name, "--ignore-not-found", "-o=json", "--request-timeout=20s",
			).Output()
			if err != nil {
				return err
			}
			if out != "" {
				return fmt.Errorf("temporary RuntimeClass %s still exists", name)
			}
		}
		return nil
	}); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}
