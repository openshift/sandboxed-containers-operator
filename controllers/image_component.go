package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"

	semver "github.com/Masterminds/semver/v3"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/oc/pkg/cli/admin/release"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/cli-runtime/pkg/genericiooptions"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/transport"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const defaultGraphURL = "https://api.openshift.com/api/upgrades_info/v1/graph"

// Cache for the loaded images
var (
	imageCache = make(map[string]map[string]string)
	cacheMu    sync.RWMutex
)

func getCachedImage(componentName, versionKey string) (string, bool) {
	cacheMu.RLock()
	defer cacheMu.RUnlock()
	if inner, ok := imageCache[componentName]; ok {
		if img, ok := inner[versionKey]; ok {
			return img, true
		}
	}
	return "", false
}

func setCachedImage(componentName, versionKey, image string) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	inner, ok := imageCache[componentName]
	if !ok {
		inner = make(map[string]string)
		imageCache[componentName] = inner
	}
	inner[versionKey] = image
}

type versionGraph struct {
	Nodes []versionNode `json:"nodes"`
}

type versionNode struct {
	Version string `json:"version"`
	Payload string `json:"payload"`
}

// GetImageForComponent returns the container image for componentName from a specified
// OpenShift release payload. If version is provided, the function attempts to resolve
// the release payload, then loads release info for that payload. If version is empty
// it falls back to the cluster's release image.
func GetImageForComponent(componentName string, version string, graphURL string, k8sClient client.Client) (string, error) {
	var versionKey string
	if version != "" {
		versionKey = version
	} else {
		// Use cluster's desired release image when version is not specified
		clusterVersion := &configv1.ClusterVersion{}
		if err := k8sClient.Get(context.TODO(), types.NamespacedName{Name: "version"}, clusterVersion); err != nil {
			return "", fmt.Errorf("failed to get cluster version: %w", err)
		}
		versionKey = clusterVersion.Status.Desired.Image
		if versionKey == "" {
			return "", fmt.Errorf("no release image found in cluster version")
		}
	}

	// Check cache
	if img, ok := getCachedImage(componentName, versionKey); ok {
		return img, nil
	}

	// Resolve release image
	var releaseImage string
	if version != "" {
		// Resolve release image from upgrade graph if a version is specified
		payload, err := getPayloadFromGraph(version, graphURL)
		if err != nil {
			return "", fmt.Errorf("failed to resolve version payload from upgrade graph: %w", err)
		}
		if payload == "" {
			return "", fmt.Errorf("couldn't find the provided version in the upgrade graph")
		}

		releaseImage = payload
	} else {
		// Use cluster's desired release image when version is not specified
		releaseImage = versionKey
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
		return "", fmt.Errorf("error loading release info: %w", err)
	}

	// Search for the componentName and return
	for _, tag := range releaseInfo.References.Spec.Tags {
		if tag.Name == componentName {
			// we found the short name in ImageStream
			if tag.From != nil && tag.From.Kind == "DockerImage" {
				setCachedImage(componentName, versionKey, tag.From.Name)
				return tag.From.Name, nil
			}
		}
	}

	// Didn't find it
	return "", fmt.Errorf("empty result for image name")
}

// getPayloadFromGraph tries to map a semantic version (e.g., "4.14.24") to the
// exact release payload image using the upgrade graph. It queries the "stable"
// channel for the given 4.x minor.
// Based on github.com/openshift/oc/pkg/cli/admin/release
func getPayloadFromGraph(version string, graphURL string) (string, error) {
	if len(graphURL) == 0 {
		graphURL = defaultGraphURL
	}
	u, err := url.Parse(graphURL)
	if err != nil {
		return "", err
	}

	// Parse the version and ensure it’s an OCP 4.x version for channel lookup
	semanticVersion, err := semver.NewVersion(version)
	if err != nil {
		return "", fmt.Errorf("invalid semantic version %s: %w", version, err)
	}
	if semanticVersion.Major() != uint64(4) {
		return "", fmt.Errorf("only OpenShift 4.x versions are supported")
	}

	// Build a client with a proper User-Agent
	transport, err := transport.HTTPWrappersForConfig(
		&transport.Config{
			UserAgent: rest.DefaultKubernetesUserAgent() + "(release-info)",
		},
		http.DefaultTransport,
	)
	if err != nil {
		return "", err
	}
	httpClient := &http.Client{Transport: transport}

	u.RawQuery = url.Values{
		"channel": []string{fmt.Sprintf("%s-%d.%d", "stable", semanticVersion.Major(), semanticVersion.Minor())},
	}.Encode()

	request, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/json")

	response, err := httpClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("could not get %s version graph", u.String())
	}

	data, err := io.ReadAll(response.Body)
	if err != nil {
		return "", err
	}

	var versions versionGraph
	if err := json.Unmarshal(data, &versions); err != nil {
		return "", err
	}

	for _, node := range versions.Nodes {
		if node.Version == version && len(node.Payload) > 0 {
			return node.Payload, nil
		}
	}

	// No payload was found
	return "", fmt.Errorf("version %s not present in stable channel", version)
}
