package main

import (
	"context"
	"os"

	configv1 "github.com/openshift/api/config/v1"
	csvv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

func getCsv(ctx context.Context, mgr manager.Manager) (*csvv1alpha1.ClusterServiceVersion, error) {
	// Get the operator CSV
	// Namespace is the same as the operator namespace
	// RELEASE_VERSION is the operator version
	csvName := "sandboxed-containers-operator.v" + os.Getenv("RELEASE_VERSION")

	csv := &csvv1alpha1.ClusterServiceVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name:      csvName,
			Namespace: OperatorNamespace,
		},
	}

	err := mgr.GetAPIReader().Get(ctx, client.ObjectKeyFromObject(csv), csv)
	if err != nil {
		setupLog.Error(err, "Cannot get the operator CSV: "+csvName)
		return nil, err
	}
	return csv, nil
}

// fix the CSV environment variables
// The environment variables are under container manager
// the jsonpath is:
// .spec.install.spec.deployments[*].spec.template.spec.containers[?(@.name=="manager")].env]
func fixCsvEnv(ctx context.Context, mgr manager.Manager, csv *csvv1alpha1.ClusterServiceVersion) error {

	envMap := map[string]string{
		"RELATED_IMAGE_CAA":           "RELATED_IMAGE_CAA_PREV",
		"RELATED_IMAGE_PODVM_BUILDER": "RELATED_IMAGE_PODVM_BUILDER_PREV",
		"RELATED_IMAGE_PODVM_PAYLOAD": "RELATED_IMAGE_PODVM_PAYLOAD_PREV",
	}

	for _, deployment := range csv.Spec.InstallStrategy.StrategySpec.DeploymentSpecs {
		for i, container := range deployment.Spec.Template.Spec.Containers {
			if container.Name == "manager" {
				// Update the environment variables using the envMap
				for j, env := range container.Env {
					if newVal, ok := envMap[env.Name]; ok {
						deployment.Spec.Template.Spec.Containers[i].Env[j].Value = os.Getenv(newVal)
					}
				}
				break
			}
		}
	}

	// Update the CSV
	err := mgr.GetClient().Update(ctx, csv)
	if err != nil {
		setupLog.Error(err, "Cannot update the operator CSV: "+csv.Name)
		return err
	}

	return nil
}

// Patch CSV if ClusterVersion is < 4.15.0
func patchCsv(ctx context.Context, mgr manager.Manager) error {
	// Get the cluster version
	clusterVersion, err := getClusterVersion(ctx, mgr)
	if err != nil {
		// If clusterVersion is not available we'll assume it's less than 4.15
		setupLog.Error(err, "Cannot get the cluster version. Assuming it's less than 4.15")
		clusterVersion = "4.14.0"
	}

	// Patch CSV to use older images for peer-pods if cluster version is less than 4.15
	if clusterVersion < "4.15.0" {
		csv, err := getCsv(ctx, mgr)
		if err != nil {
			setupLog.Error(err, "Cannot get the operator CSV")
			return err
		}

		err = fixCsvEnv(ctx, mgr, csv)
		if err != nil {
			setupLog.Error(err, "Cannot patch the CSV environment variables")
			return err
		}
	}

	return nil
}

// Method to get cluster version from ClusterVersion object
func getClusterVersion(ctx context.Context, mgr manager.Manager) (string, error) {
	clusterVersion := &configv1.ClusterVersion{}

	err := mgr.GetAPIReader().Get(ctx, client.ObjectKey{Name: "version"}, clusterVersion)
	if err != nil {
		return "", err
	}

	// Return cluster version
	return clusterVersion.Spec.DesiredUpdate.Version, nil

}
