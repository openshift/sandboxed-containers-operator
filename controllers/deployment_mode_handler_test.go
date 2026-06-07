package controllers

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// mcpProbeClient stubs Get for MachineConfigPool probes (fake client returns NotFound, not NoMatch).
type mcpProbeClient struct {
	client.Client
	mcpGetErr error
}

func (c *mcpProbeClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if u, ok := obj.(*unstructured.Unstructured); ok && u.GetKind() == machineConfigPoolKind {
		if c.mcpGetErr != nil {
			return c.mcpGetErr
		}
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func TestIsMachineConfigPoolAvailable(t *testing.T) {
	t.Parallel()

	t.Run("returns false when MCP API is not registered", func(t *testing.T) {
		t.Parallel()
		scheme := runtime.NewScheme()
		if err := corev1.AddToScheme(scheme); err != nil {
			t.Fatal(err)
		}

		base := fake.NewClientBuilder().WithScheme(scheme).Build()
		noMatch := &meta.NoKindMatchError{
			GroupKind: schema.GroupKind{
				Group: machineConfigGroup,
				Kind:  machineConfigPoolKind,
			},
			SearchedVersions: []string{machineConfigVersion},
		}
		r := &KataConfigOpenShiftReconciler{
			Client: &mcpProbeClient{Client: base, mcpGetErr: noMatch},
			Log:    logr.Discard(),
		}

		avail, err := r.isMachineConfigPoolAvailable()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if avail {
			t.Fatal("expected MachineConfigPool to be unavailable")
		}
	})

	t.Run("returns true when MCP API is registered", func(t *testing.T) {
		t.Parallel()
		scheme := runtime.NewScheme()
		if err := corev1.AddToScheme(scheme); err != nil {
			t.Fatal(err)
		}
		if err := mcfgv1.AddToScheme(scheme); err != nil {
			t.Fatal(err)
		}

		r := &KataConfigOpenShiftReconciler{
			Client: fake.NewClientBuilder().WithScheme(scheme).Build(),
			Log:    logr.Discard(),
		}

		avail, err := r.isMachineConfigPoolAvailable()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !avail {
			t.Fatal("expected MachineConfigPool to be available")
		}
	})

	t.Run("returns true when MCP CR exists", func(t *testing.T) {
		t.Parallel()
		scheme := runtime.NewScheme()
		if err := corev1.AddToScheme(scheme); err != nil {
			t.Fatal(err)
		}
		if err := mcfgv1.AddToScheme(scheme); err != nil {
			t.Fatal(err)
		}

		mcp := &mcfgv1.MachineConfigPool{
			ObjectMeta: metav1.ObjectMeta{Name: "worker"},
		}
		r := &KataConfigOpenShiftReconciler{
			Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(mcp).Build(),
			Log:    logr.Discard(),
		}

		avail, err := r.isMachineConfigPoolAvailable()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !avail {
			t.Fatal("expected MachineConfigPool to be available")
		}
	})
}

func TestCheckConvergedClusterWhenMCPUnavailable(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	base := fake.NewClientBuilder().WithScheme(scheme).Build()
	noMatch := &meta.NoKindMatchError{
		GroupKind: schema.GroupKind{
			Group: machineConfigGroup,
			Kind:  machineConfigPoolKind,
		},
		SearchedVersions: []string{machineConfigVersion},
	}
	r := &KataConfigOpenShiftReconciler{
		Client: &mcpProbeClient{Client: base, mcpGetErr: noMatch},
		Log:    logr.Discard(),
	}

	converged, err := r.checkConvergedCluster()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if converged {
		t.Fatal("expected cluster not to be converged when MCP API is absent")
	}
}
