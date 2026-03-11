#!/usr/bin/env bash
set -euo pipefail

# This script automates generating rpms.lock.yaml (notes provided by @jrope during Q4 2025).
# It must be run from the directory containing rpms.in.yaml.
# It also updates the redhat.repo and ubi.repo files in the same directory.
# Note: if all "url" lines in rpms.lock.yaml reference "cdn.redhat.com" (and none
# reference "cdn-ubi.redhat.com"), then ubi.repo is not needed and can be removed.
#
# Requirements on host:
# - podman
# - activation key
# - organization id
# - registry username
#
# Note: An activation key can be created following this documentation:
# https://konflux.pages.redhat.com/docs/users/building/activation-keys-subscription.html#create-the-activation-key
#
# Inputs (flags or env vars):
# - -k ACTIVATION_KEY (or env ACTIVATION_KEY)
# - -o ORG_ID (or env ORG_ID)
# - -u REGISTRY_USERNAME (or env REGISTRY_USERNAME)
# - -p REGISTRY_PASSWORD (or env REGISTRY_PASSWORD)
# - -i IMAGE (defaults to registry.access.redhat.com/ubi9/ubi:latest)
#
# Example:
#   ./update-rpms-lock.sh \
#     -k "$ACTIVATION_KEY" -o "$ORG_ID" \
#     -u "$REGISTRY_USERNAME" -p "$REGISTRY_PASSWORD"
#
# Author: Julien Rope <jrope@redhat.com>
# Author: Daniel Kreling <dkreling@redhat.com>
# Made with the help of Cursor and Claude Opus 4.6


IMAGE_DEFAULT="registry.access.redhat.com/ubi9/ubi:latest"

usage() {
  cat <<EOF
Usage: $(basename "$0") [-k ACTIVATION_KEY] [-o ORG_ID] [-u REGISTRY_USERNAME] [-p REGISTRY_PASSWORD] [-i IMAGE]

Environment variables may be used instead of flags: ACTIVATION_KEY, ORG_ID, REGISTRY_USERNAME, REGISTRY_PASSWORD, IMAGE.
Defaults: IMAGE=${IMAGE_DEFAULT}

Note: run from the folder containing rpms.in.yaml.
EOF
}

ACTIVATION_KEY="${ACTIVATION_KEY:-}"
ORG_ID="${ORG_ID:-}"
REGISTRY_USERNAME="${REGISTRY_USERNAME:-}"
REGISTRY_PASSWORD="${REGISTRY_PASSWORD:-}"
IMAGE="${IMAGE:-${IMAGE_DEFAULT}}"

while getopts ":k:o:u:p:i:h" opt; do
  case "${opt}" in
    k) ACTIVATION_KEY="$OPTARG" ;;
    o) ORG_ID="$OPTARG" ;;
    u) REGISTRY_USERNAME="$OPTARG" ;;
    p) REGISTRY_PASSWORD="$OPTARG" ;;
    i) IMAGE="$OPTARG" ;;
    h) usage; exit 0 ;;
    :) echo "Missing argument for -$OPTARG" >&2; usage; exit 2 ;;
    \?) echo "Unknown option -$OPTARG" >&2; usage; exit 2 ;;
  esac
done
shift $((OPTIND-1)) || true

# Extract UBI version from the image name (e.g. "ubi10" -> "10", "ubi9" -> "9")
UBI_VERSION=$(echo "${IMAGE}" | grep -oP 'ubi\K[0-9]+')
if [[ -z "${UBI_VERSION}" ]]; then
  echo "Error: could not determine UBI version from IMAGE: ${IMAGE}" >&2
  exit 1
fi

# Basic validations
if [[ ! -f "rpms.in.yaml" ]]; then
  echo "Error: rpms.in.yaml not found in current directory: $(pwd)" >&2
  exit 1
fi

if [[ -z "${ACTIVATION_KEY}" || -z "${ORG_ID}" ]]; then
  echo "Error: ACTIVATION_KEY and ORG_ID are required." >&2
  usage
  exit 1
fi

if [[ -z "${REGISTRY_USERNAME}" || -z "${REGISTRY_PASSWORD}" ]]; then
  echo "Error: REGISTRY_USERNAME and REGISTRY_PASSWORD are required." >&2
  usage
  exit 1
fi

# Run the workflow inside a UBI9 container, mounting the current directory at /source

podman run --rm -i \
  -v "$(pwd):/source:Z" \
  -e ACTIVATION_KEY="${ACTIVATION_KEY}" \
  -e ORG_ID="${ORG_ID}" \
  -e REGISTRY_USERNAME="${REGISTRY_USERNAME}" \
  -e REGISTRY_PASSWORD="${REGISTRY_PASSWORD}" \
  -e UBI_VERSION="${UBI_VERSION}" \
  "${IMAGE}" bash -lc '
set -euo pipefail

# Ensure required tools (subscription-manager, skopeo, python3-pip) are available
# Notes used "pip"; on UBI9 the package is typically "python3-pip".
# Use dnf if available (UBI standard), otherwise fall back to microdnf (UBI minimal).
echo "Installing required packages: subscription-manager, skopeo, python3-pip..."
if command -v dnf &>/dev/null; then
  dnf install -y subscription-manager skopeo python3-pip >/dev/null
elif command -v microdnf &>/dev/null; then
  microdnf install -y subscription-manager skopeo python3-pip >/dev/null
else
  echo "Error: neither dnf nor microdnf found in the container image." >&2
  exit 1
fi

# Register using activation key and org
echo "Registering system with Red Hat subscription-manager..."
subscription-manager register \
  --activationkey="${ACTIVATION_KEY}" \
  --org="${ORG_ID}"

# Show enabled repositories
echo "Listing enabled repositories..."
if command -v dnf &>/dev/null; then
  dnf repolist --enabled
elif command -v microdnf &>/dev/null; then
  microdnf repolist --enabled
fi

# Install rpm-lockfile-prototype via pip
echo "Installing rpm-lockfile-prototype from GitHub..."
python3 -m pip install --user \
  https://github.com/konflux-ci/rpm-lockfile-prototype/archive/refs/heads/main.zip

# Make sure ~/.local/bin is on PATH for the installed tool
echo "Adding ~/.local/bin to PATH..."
export PATH="${PATH}:/root/.local/bin"

# Copy repo files to /source to be committed and adjusted for multi-arch
echo "Copying yum repo files (redhat.repo, ubi.repo) to /source..."
cp -f /etc/yum.repos.d/redhat.repo /source/redhat.repo
cp -f /etc/yum.repos.d/ubi.repo /source/ubi.repo

cd /source

# Adjust architecture placeholders for multi-arch
echo "Replacing architecture-specific values with \$basearch placeholders..."
sed -i "s/$(uname -m)/\$basearch/g" redhat.repo
sed -i "s/ubi-${UBI_VERSION}-codeready-builder/codeready-builder-for-ubi-${UBI_VERSION}-\$basearch/" ubi.repo
sed -i "s/\[ubi-${UBI_VERSION}/[ubi-${UBI_VERSION}-for-\$basearch/" ubi.repo

# Enable source repositories in the ubi.repo files
# This finds lines containing "-source]" and the first "enabled = 0" following it
echo "Enabling source RPM repositories in ubi.repo..."
sed -i "/-source-rpms]/,/enabled = 0/ s/enabled = 0/enabled = 1/" ubi.repo

# Non-interactive skopeo login using provided credentials
echo "Logging in to registry.redhat.io with skopeo..."
printf "%s" "${REGISTRY_PASSWORD}" | skopeo login \
  --username "${REGISTRY_USERNAME}" \
  --password-stdin \
  registry.redhat.io

# Run the lockfile generator
echo "Running rpm-lockfile-prototype to generate the lockfile..."
rpm-lockfile-prototype rpms.in.yaml

echo "rpms.lock.yaml generation complete. Files in /source have been updated."
'
