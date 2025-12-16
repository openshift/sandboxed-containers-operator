package controllers

import (
	"context"
	"fmt"
	"os"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/oc/pkg/cli/admin/release"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/cli-runtime/pkg/genericiooptions"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GetImageForComponent retrieves the Docker image for a specified component.
// It uses the OpenShift client to fetch the cluster version and then loads the release info.
// It searches for the componentName in the release info and returns the corresponding Docker image.
func GetImageForComponent(componentName string, client client.Client) (string, error) {
	// Fetch the cluster version
	clusterVersion := &configv1.ClusterVersion{}
	err := client.Get(context.TODO(), types.NamespacedName{Name: "version"}, clusterVersion)
	if err != nil {
		return "", fmt.Errorf("failed to get cluster version: %w", err)
	}

	releaseImage := clusterVersion.Status.Desired.Image
	if releaseImage == "" {
		return "", fmt.Errorf("no release image found in cluster version")
	}

	// Create IOStreams
	streams := genericiooptions.IOStreams{
		Out:    os.Stdout,
		ErrOut: os.Stderr,
	}

	// Create InfoOptions
	infoOptions := release.NewInfoOptions(streams)

	// Load release info (set retrieveImages to false for faster loading)
	releaseInfo, err := infoOptions.LoadReleaseInfo(releaseImage, false)
	if err != nil {
		fmt.Printf("Error loading release info: %v\n", err)
		return "", err
	}

	// Search for the componentName and return
	for _, tag := range releaseInfo.References.Spec.Tags {
		if tag.Name == componentName {
			// we found the short name in ImageStream
			if tag.From != nil && tag.From.Kind == "DockerImage" {
				return tag.From.Name, nil
			}
		}
	}

	// Didn't find it
	return "", fmt.Errorf("empty result for image name")
}