//go:build tools

package main

// Add tool packages here so that cachi2 prefetchs them for hermetic builds.
import (
	_ "sigs.k8s.io/kustomize/kustomize/v5"
)
