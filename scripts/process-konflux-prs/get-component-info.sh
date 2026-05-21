#!/bin/bash
# Get information about a component (build time, nudged components)
# Usage: ./get-component-info.sh [--component COMPONENT_NAME]
#
# Outputs JSON with component metadata or all components if no name specified

set -euo pipefail

COMPONENT=""

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --component)
            COMPONENT="$2"
            shift 2
            ;;
        *)
            echo "Usage: $0 [--component COMPONENT_NAME]" >&2
            exit 1
            ;;
    esac
done

# Component metadata (from the skill documentation)
components_json='[
  {
    "name": "osc-monitor",
    "repository": "kata-containers",
    "build_time_minutes": 10,
    "nudges": ["osc-operator-bundle"]
  },
  {
    "name": "osc-caa",
    "repository": "cloud-api-adaptor",
    "build_time_minutes": 15,
    "nudges": ["osc-operator-bundle"]
  },
  {
    "name": "osc-caa-webhook",
    "repository": "cloud-api-adaptor",
    "build_time_minutes": 10,
    "nudges": ["osc-operator-bundle"]
  },
  {
    "name": "osc-podvm-payload",
    "repository": "cloud-api-adaptor",
    "build_time_minutes": 45,
    "nudges": ["osc-operator-bundle", "osc-dm-verity-image", "osc-initrds"]
  },
  {
    "name": "osc-pccs",
    "repository": "compute-artifacts",
    "build_time_minutes": 8,
    "nudges": ["osc-operator-bundle"]
  },
  {
    "name": "osc-tdx-qgs",
    "repository": "compute-artifacts",
    "build_time_minutes": 8,
    "nudges": ["osc-operator-bundle"]
  },
  {
    "name": "osc-storage-helper",
    "repository": "compute-artifacts",
    "build_time_minutes": 8,
    "nudges": ["osc-operator-bundle"]
  },
  {
    "name": "osc-operator",
    "repository": "osc",
    "build_time_minutes": 12,
    "nudges": ["osc-operator-bundle"]
  },
  {
    "name": "osc-operator-bundle",
    "repository": "osc",
    "build_time_minutes": 3,
    "nudges": ["osc-test-fbc"]
  },
  {
    "name": "osc-podvm-builder",
    "repository": "osc",
    "build_time_minutes": 10,
    "nudges": ["osc-operator-bundle"]
  },
  {
    "name": "osc-must-gather",
    "repository": "osc",
    "build_time_minutes": 10,
    "nudges": ["osc-operator-bundle"]
  },
  {
    "name": "osc-dm-verity-image",
    "repository": "podvm-scripts",
    "build_time_minutes": 50,
    "nudges": ["osc-operator-bundle"]
  },
  {
    "name": "build-dm-verity-image",
    "repository": "podvm-scripts",
    "build_time_minutes": 2,
    "nudges": ["osc-dm-verity-image"]
  },
  {
    "name": "osc-initrds",
    "repository": "compute-artifacts",
    "build_time_minutes": 15,
    "nudges": []
  },
  {
    "name": "osc-test-fbc",
    "repository": "osc",
    "build_time_minutes": 15,
    "nudges": []
  }
]'

if [[ -z "$COMPONENT" ]]; then
    # Return all components
    echo "$components_json" | jq '.'
else
    # Return specific component
    echo "$components_json" | jq --arg name "$COMPONENT" '.[] | select(.name == $name)'
fi
