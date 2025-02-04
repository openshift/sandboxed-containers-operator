#!/bin/bash
# Contains common functions used by the scripts

[[ "$DEBUG" == "true" ]] && set -x

# Bootc Defaults
BIB_IMAGE=${BIB_IMAGE:-registry.redhat.io/rhel9/bootc-image-builder:9.5}

# Defaults for pause image
# This pause image is multi-arch
PAUSE_IMAGE_REPO_DEFAULT="quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256"
PAUSE_IMAGE_VERSION_DEFAULT="7f3cb6f9d265291b47a7491c2ba4f4dd0752a18b661eee40584f9a5dbcbe13bb"
CLUSTER_PULL_SECRET_AUTH_FILE="/tmp/regauth/auth.json"

# function to trap errors and exit
function error_exit() {
    echo "$1" 1>&2
    exit 1
}

# function to install required rpm packages.
# the packages are listed in the variable REQUIRED_RPM_PACKAGES

function install_rpm_packages() {
    # Install required rpm packages
    # If any error occurs, exit the script with an error message

    # List of packages to be installed
    REQUIRED_RPM_PACKAGES=(
        "curl"
        "git"
        "make"
        "unzip"
        "skopeo"
        "jq"
        "qemu-img" # for handling pre-built images. Note that this rpm requires subscription
        "podman" # TODO: can we drop podman/skopeo?
    )

    # Create a new array to store rpm packages that are not installed
    NEW_REQUIRED_RPM_PACKAGES=()

    # Check which rpm packages are already installed and remove them from the list
    for package in "${REQUIRED_RPM_PACKAGES[@]}"; do
        if rpm -q "${package}" &>/dev/null; then
            echo "Package ${package} is already installed. Skipping."
        else
            # Add the rpm package to the new array if it's not installed
            NEW_REQUIRED_RPM_PACKAGES+=("$package")
        fi
    done

    # Update the original array with the new list of rpm packages
    REQUIRED_RPM_PACKAGES=("${NEW_REQUIRED_RPM_PACKAGES[@]}")

    # Install the required rpm packages
    if [[ "${#REQUIRED_RPM_PACKAGES[@]}" -gt 0 ]]; then
        echo "Installing required packages..."
        # Using allowerasing flag to remove conflicting packages
        # eg curl and curl-minimal
        yum install -y --allowerasing "${REQUIRED_RPM_PACKAGES[@]}" ||
            error_exit "Failed to install required packages"
    else
        echo "All required packages are already installed."
    fi

}

# function to download and install binary packages.
# the packages, their respective download locations and compression
# are available in the variable REQUIRED_BINARY_PACKAGES
# the function will download the packages, extract them and install them in /usr/local/bin
# Following are the packages that are installed:
#"packer=https://releases.hashicorp.com/packer/1.9.4/packer_1.9.4_linux_amd64.zip"
#"kubectl=https://mirror.openshift.com/pub/openshift-v4/x86_64/clients/ocp/4.14.9/openshift-client-linux.tar.gz"
#"yq=https://github.com/mikefarah/yq/releases/download/v4.35.2/yq_linux_amd64.tar.gz"
#"umoci=https://github.com/opencontainers/umoci/releases/download/v0.4.7/umoci.amd64"

install_binary_packages() {
    ARCH=$(uname -m)
    if [ "${ARCH}" == "s390x" ]; then
        # Define the required binary packages
        REQUIRED_BINARY_PACKAGES=(
            "kubectl=https://mirror.openshift.com/pub/openshift-v4/s390x/clients/ocp/4.14.9/openshift-client-linux.tar.gz"
            "yq=https://github.com/mikefarah/yq/releases/download/v4.35.1/yq_linux_s390x.tar.gz"
        )
    else
        # Define the required binary packages
        REQUIRED_BINARY_PACKAGES=(
            "packer=https://releases.hashicorp.com/packer/1.9.4/packer_1.9.4_linux_amd64.zip"
            "kubectl=https://mirror.openshift.com/pub/openshift-v4/x86_64/clients/ocp/4.14.9/openshift-client-linux.tar.gz"
            "yq=https://github.com/mikefarah/yq/releases/download/v4.35.2/yq_linux_amd64.tar.gz"
            "umoci=https://github.com/opencontainers/umoci/releases/download/v0.4.7/umoci.amd64"
        )
    fi

    # Specify the installation directory
    local install_dir="/usr/local/bin"

    # Install the required binary packages
    for package_info in "${REQUIRED_BINARY_PACKAGES[@]}"; do
        IFS='=' read -r package_name package_url <<<"${package_info}"
        download_path="/tmp/${package_name}"

        if [[ -x "${install_dir}/${package_name}" ]]; then
            echo "Package ${package_name} is already installed. Skipping."
            continue
        else
            echo "Downloading ${package_name}..."
            curl -sSL "${package_url}" -o "${download_path}" ||
                error_exit "Failed to download ${package_name}"

            echo "Extracting ${package_name}..."
            if [[ "${package_url}" == *.zip ]]; then
                unzip -q "${download_path}" -d "${install_dir}" ||
                    error_exit "Failed to extract ${package_name}"
            elif [[ "${package_url}" == *.tar.gz ]]; then
                tar -xf "${download_path}" -C "${install_dir}" ||
                    error_exit "Failed to extract ${package_name}"
            else
                echo "Copying ${download_path} to ${install_dir}/${package_name}"
                cp "${download_path}" "${install_dir}/${package_name}" ||
                    error_exit "Failed to move ${package_name} to ${install_dir}"
            fi

            echo "Marking  ${install_dir}/${package_name} executable"
            # yq extracted file name is yq_linux_${ARCH}. Rename it
            [[ "${package_name}" == "yq" ]] &&
                mv "${install_dir}"/yq_linux_"${ARCH/x86_64/amd64}" "${install_dir}"/yq

            chmod +x "${install_dir}/${package_name}" ||
                error_exit "Failed to mark ${package_name} executable"

            echo "Cleaning up..."
            rm -f "${download_path}"
        fi
    done

    echo "All binary packages installed successfully."

}

# Function to download source code from GitHub

function download_source_code() {

    # Download source code from GitHub
    # If any error occurs, exit the script with an error message

    # CAA_SRC_DIR is set to CAA_SRC_DOWNLOAD_DIR/src/cloud-api-adaptor
    # The default value of CAA_SRC_DOWNLOAD_DIR is /src/cloud-api-adaptor

    # Delete the source code download directory if it exists
    [[ -d "${CAA_SRC_DOWNLOAD_DIR}" ]] &&
        rm -rf "${CAA_SRC_DOWNLOAD_DIR}"

    # Create the root directory for source code
    mkdir -p "${CAA_SRC_DOWNLOAD_DIR}"

    # Download the source code from GitHub
    git clone "${CAA_SRC}" "${CAA_SRC_DOWNLOAD_DIR}" ||
        error_exit "Failed to download source code from GitHub"

    # Checkout the required commit
    cd "${CAA_SRC_DOWNLOAD_DIR}" ||
        error_exit "Failed to change directory to ${CAA_SRC_DOWNLOAD_DIR}"

    git checkout "${CAA_REF}" ||
        error_exit "Failed to checkout the required commit"

}

# Function to prepare the source code for building the image
# Patch any files that need to be patched
# Copy any files that need to be copied

function prepare_source_code() {

    # Prepare the source code for building the image
    # If any error occurs, exit the script with an error message

    # Ensure CAA_SRC_DIR is set
    [[ -z "${CAA_SRC_DIR}" ]] && error_exit "CAA_SRC_DIR is not set"

    local podvm_dir="${CAA_SRC_DIR}/podvm"

    mkdir -p "${podvm_dir}"/files

    # Download the podvm binaries and copy it to the podvm/files directory
    tar xvf /payload/podvm-binaries.tar.gz -C "${podvm_dir}"/files ||
        error_exit "Failed to download podvm binaries"

    # Set the NVIDIA_DRIVER_VERSION if variable is set
    if [[ "${NVIDIA_DRIVER_VERSION}" ]]; then
        echo "NVIDIA_DRIVER_VERSION is set to ${NVIDIA_DRIVER_VERSION}"
        sed -i "s/535/${NVIDIA_DRIVER_VERSION}/g" "${podvm_dir}"/addons/nvidia_gpu/setup.sh ||
            error_exit "Failed to set NVIDIA_DRIVER_VERSION"
    fi

    # Set the NVIDIA_USERSPACE_VERSION if variable is set
    if [[ "${NVIDIA_USERSPACE_VERSION}" ]]; then
        echo "NVIDIA_USERSPACE_VERSION is set to ${NVIDIA_USERSPACE_VERSION}"
        sed -i "s/1.13.5-1/${NVIDIA_USERSPACE_VERSION}/g" "${podvm_dir}"/addons/nvidia_gpu/setup.sh ||
            error_exit "Failed to set NVIDIA_USERSPACE_VERSION"
    fi

    if [[ "$BOOT_FIPS" == "yes" ]]; then
        echo "FIPS mode is enabled"
        sed -i '/exit 0/ifips-mode-setup --enable' "${podvm_dir}"/qcow2/misc-settings.sh ||
            error_exit "Failed to enable fips mode"
    fi

    # links must be relative
    if [[ "${AGENT_POLICY}" ]]; then
        echo "Custom agent policy is being set through the AGENT_POLICY value (this can be overridden if initData policy is set)"
        echo "${AGENT_POLICY}" | base64 -d >"${podvm_dir}"/files/etc/kata-opa/custom.rego
        return_code=$?
        if [[ "$return_code" == 0 ]] && grep -q "agent_policy" "${podvm_dir}"/files/etc/kata-opa/custom.rego; then # checks policy validity
            sed -i 's/allow-all.rego/custom.rego/g' "${podvm_dir}"/files/etc/tmpfiles.d/policy.conf || error_exit "Failed 
to edit policy.conf"
        else
            error_exit "Invalid AGENT_POLICY value set, expected base64 encoded valid agent policy, got: \"${AGENT_POLICY}\""
        fi
	echo "~~~ Fallback default policy is set to: ~~~" && cat "${podvm_dir}"/files/etc/kata-opa/custom.rego
    elif [[ "$CONFIDENTIAL_COMPUTE_ENABLED" == "yes" ]]; then
        echo "Setting custom agent policy to CoCo's recommended policy"
        sed 's/default ReadStreamRequest := true/default ReadStreamRequest := false/;
            s/default ExecProcessRequest := true/default ExecProcessRequest := false/' \
            "${podvm_dir}"/files/etc/kata-opa/default-policy.rego >"${podvm_dir}"/files/etc/kata-opa/coco-default-policy.rego
        sed -i 's/allow-all.rego/coco-default-policy.rego/g' "${podvm_dir}"/files/etc/tmpfiles.d/policy.conf || error_exit "Failed to edit policy.conf"
	echo "~~~ Fallback default policy is set to: ~~~" && cat "${podvm_dir}"/files/etc/kata-opa/coco-default-policy.rego
    fi

    # Fix disk mounts for CoCo
    if [[ "$CONFIDENTIAL_COMPUTE_ENABLED" == "yes" ]]; then
        create_overlay_mount_unit
    fi

    # disable ssh and unsafe cloud-init modules
    if [[ "$CONFIDENTIAL_COMPUTE_ENABLED" == "yes" ]] || [[ -n "$CUSTOM_CLOUD_INIT_MODULES" ]]; then
        [[ "$CUSTOM_CLOUD_INIT_MODULES" != "no" ]] && [[ "$CLOUD_PROVIDER" != "libvirt" ]] && set_custom_cloud_init_modules
    fi

    # Validate and copy HKD for IBM Z Secure Enablement
    if [[ "$SE_BOOT" == "true" ]]; then
        if [[ -z "$HOST_KEY_CERTS" ]]; then
            error_exit "Error: HKD is not present."
        else
            echo "$HOST_KEY_CERTS" >>"${podvm_dir}/files/HKD.crt"
        fi
    fi

    # Enable image signature check
    if [[ "$CONFIDENTIAL_COMPUTE_ENABLED" == "yes" ]]; then
        local agent_config_file="/etc/agent-config.toml"

        cat<<EOF>"${podvm_dir}/files${agent_config_file}"
server_addr = "unix:///run/kata-containers/agent.sock"
guest_components_procs = "none"
image_registry_auth = "file:///run/peerpod/auth.json"
enable_signature_verification = true
image_policy_file = "kbs:///default/security-policy/osc"
EOF
        sed -i "s,/run/peerpod/agent-config.toml,${agent_config_file},g" \
            "${podvm_dir}"/files/etc/systemd/system/kata-agent.service
        echo "sudo cp -a /tmp/files${agent_config_file} ${agent_config_file}" >>"${podvm_dir}"/qcow2/copy-files.sh
    fi
}

# Download and extract the pause container image
# Accepts three arguments:
# 1. pause_image_repo_url: The registry URL of the OCP pause image.
# 2. pause_image_tag: The tag of the OCP pause image.
# 3. auth_json_file (optional): Path to the registry secret file to use for downloading the image
function download_and_extract_pause_image() {

    # Set default values for the OCP pause image
    pause_image_repo_url="${1:-${PAUSE_IMAGE_REPO_DEFAULT}}"
    pause_image_tag="${2:-${PAUSE_IMAGE_VERSION_DEFAULT}}"
    auth_json_file="${3:-${CLUSTER_PULL_SECRET_AUTH_FILE}}"

    # If arguments are not provided, exit the script with an error message
    [[ $# -lt 2 ]] &&
        error_exit "Usage: download_and_extract_pause_image <pause_image_repo_url> <pause_image_tag> [registry_secret]"

    # Ensure CAA_SRC_DIR is set
    [[ -z "${CAA_SRC_DIR}" ]] && error_exit "CAA_SRC_DIR is not set"

    local podvm_dir="${CAA_SRC_DIR}/podvm"
    local pause_src="/tmp/pause"
    local pause_bundle="${podvm_dir}/files/pause_bundle"

    mkdir -p "${pause_bundle}" ||
        error_exit "Failed to create the pause_bundle directory"

    extract_container_image "${pause_image_repo_url}" "${pause_image_tag}" "${pause_src}" "${pause_bundle}" "${auth_json_file}"
}

# Function to download and extract a container image.
# Accepts six arguments:
# 1. container_image_repo_url: The registry URL of the source container image.
# 2. image_tag: The tag of the source container image.
# 3. dest_image: The destination image name.
# 4. destination_path: The destination path where the image is to be extracted.
# 5. auth_json_file (optional): Path to the registry secret file to use for downloading the image.
function extract_container_image() {

    # Set the values required for the container image extraction.
    container_image_repo_url="${1}"
    image_tag="${2}"
    dest_image="${3}"
    destination_path="${4}"
    auth_json_file="${5}"

    # If arguments are not provided, exit the script with an error message
    [[ $# -lt 4 ]] &&
        error_exit "Usage: extract_container_image <container_image_repo_url> <image_tag> <dest_image> <destination_path> [registry_secret]"

    # Form the skopeo CLI. Add authfile if provided
    if [[ -n "${5}" ]]; then
        SKOPEO_CLI="skopeo copy --authfile ${auth_json_file}"
    else
        SKOPEO_CLI="skopeo copy"
    fi

    # Download the container image
    $SKOPEO_CLI "docker://${container_image_repo_url}:${image_tag}" "oci:${dest_image}:${image_tag}" ||
        error_exit "Failed to download the container image"

    # Extract the container image using umoci into provided directory
    umoci unpack --rootless --image "${dest_image}:${image_tag}" "${destination_path}" ||
        error_exit "Failed to extract the container image"

    # Display the content of the destination_path
    echo "Extracted container image content:"
    ls -l "${destination_path}"

}

# These are cloud-init modules we allow for the CoCo case, it's mostly used to disable ssh
# and other unsafe modules
function set_custom_cloud_init_modules() {
    local cfg_file="${podvm_dir}/files/etc/cloud/cloud.cfg.d/99_coco_only_allow.cfg"
    mkdir -p $(dirname "${cfg_file}")
    cat <<EOF >"${cfg_file}"
cloud_init_modules:
  - migrator
  - set_hostname
  - update_hostname

cloud_config_modules:
  - locale
  - rh_subscription
  - ntp
  - timezone
  - disable_ec2_metadata

cloud_final_modules:
  #- reset_rmc # needed for ibm power?
  #- install_hotplug ?
  - phone_home
  - final_message
  - power_state_change
EOF
    echo "sudo cp -a /tmp/files/etc/cloud/cloud.cfg.d/* /etc/cloud/cloud.cfg.d/" >>"${podvm_dir}"/qcow2/copy-files.sh
    echo "Inject cloud-init configuration file:" && cat "${cfg_file}"
}

# Function to create overlay mount unit in the podvm files
# this ensures rw (overlay) layer for the container images are in memory (encrypted)
function create_overlay_mount_unit() {
    # The actual mount point is /run/kata-containers/image/overlay
    local unit_name="run-kata\\x2dcontainers-image-overlay.mount"
    local unit_path="${podvm_dir}/files/etc/systemd/system/${unit_name}"

    cat <<EOF >"${unit_path}"
[Unit]
Description=Mount unit for /run/kata-containers/image/overlay
Before=kata-agent.service

[Mount]
What=tmpfs
Where=/run/kata-containers/image/overlay
Type=tmpfs

[Install]
WantedBy=multi-user.target
EOF

    echo "Mount unit created at ${unit_name}"

    # Enable the mount unit by creating a symlink
    # This syntax works to create the symlink to the unit file in ${podvm_dir}/files/etc/systemd/system
    ln -sf ../"${unit_name}" "${podvm_dir}/files/etc/systemd/system/multi-user.target.wants/${unit_name}" ||
        error_exit "Failed to enable the overlay mount unit"

}

# Function to split image type, url and path from PODVM_IMAGE_URI for pre-built image scenario.
function get_image_type_url_and_path() {

    # Use pattern matching to split on '::' and then on ':', and capture output
    # The PODVM_IMAGE_URI is evaluated in the podvm-builder.sh
    # It must be set in the {provider}-podvm-image-cm configmap if needed
    # shellcheck disable=SC2153
    if [[ $PODVM_IMAGE_URI =~ ^([^:]+)::([^:]+)(:([^:]+))?(::(.+))?$ ]]; then
        PODVM_IMAGE_TYPE="${BASH_REMATCH[1]}"
        PODVM_IMAGE_URL="${BASH_REMATCH[2]}"
        PODVM_IMAGE_TAG="${BASH_REMATCH[4]}"      # This will be empty if not present
        PODVM_IMAGE_SRC_PATH="${BASH_REMATCH[6]}" # This will be empty if not present
    fi

    if [[ -z "${PODVM_IMAGE_TAG}" ]]; then
        PODVM_IMAGE_TAG="latest"
    fi

    if [[ -z "${PODVM_IMAGE_SRC_PATH}" ]]; then
        PODVM_IMAGE_SRC_PATH="/image/podvm.qcow2"
    fi

    export PODVM_IMAGE_TYPE PODVM_IMAGE_URL PODVM_IMAGE_TAG PODVM_IMAGE_SRC_PATH
}

# Function to get format of the podvm image
# Input: podvm image path
# Use qemu-img info to get the image info
# export the image format as PODVM_IMAGE_FORMAT
function get_podvm_image_format() {
    image_path="${1}"
    echo "Getting format of the PodVM image: ${image_path}"

    # jq -r when you want to output plain strings without quotes. Otherwise the string will be quoted
    PODVM_IMAGE_FORMAT=$(qemu-img info --output json "${image_path}" | jq -r '.format') ||
        error_exit "Failed to get podvm image info"

    # vhd images are also raw format. So check the file extension. It's crude but for
    # now it's good enough hopefully
    if [[ "${image_path}" == *.vhd ]] && [[ "${PODVM_IMAGE_FORMAT}" == "raw" ]]; then
        PODVM_IMAGE_FORMAT="vhd"
    fi

    echo "PodVM image format for ${image_path}: ${PODVM_IMAGE_FORMAT}"
    export PODVM_IMAGE_FORMAT
}

# Function to validate the podvm image type.
# Input: podvm image path
function validate_podvm_image() {
    image_path="${1}"

    echo "Validating PodVM image: ${image_path}"

    # Get the podvm image format. This sets the PODVM_IMAGE_FORMAT global variable
    get_podvm_image_format "${image_path}"

    # Check if the format is qcow2, raw or vhd
    if [[ "${PODVM_IMAGE_FORMAT}" != "qcow2" &&
        "${PODVM_IMAGE_FORMAT}" != "raw" &&
        "${PODVM_IMAGE_FORMAT}" != "vhd" ]]; then
        error_exit "PodVM image is neither a valid qcow2, raw or vhd, exiting."
    fi

    echo "Checksum of the PodVM image: $(sha256sum "$image_path")"
}

# Function to convert local bootc container image to cloud image
# Input:
# 1. container_image_repo_url: The registry URL of the source container image.
# 2. image_tag: The tag of the source container image.
# 3. auth_json_file (can be empty): Path to the registry secret file to use for downloading the image.
# 4. run_args: arguments for the podman run command that runs bootc image builder
# 5. bib_args: arguments for bootc image builder itself
# Output: based on input, either, qcow2 image at output/qcow2/disk.qcow2 or ami
# see: https://github.com/osbuild/bootc-image-builder
function bootc_image_builder_conversion() {
    container_image_repo_url="${1}"
    image_tag="${2}"
    auth_json_file="${3}"
    run_args="${4}"
    bib_args="${5}"

    # some VM customizations
    if [[ -n "${BOOTC_BUILD_CONFIG}" ]]; then
        echo "Using Custom Bootc Build Configuration"
        echo "${BOOTC_BUILD_CONFIG}" > ./custom-config.toml
        BOOTC_BUILD_CONFIG_PATH=$(pwd)/custom-config.toml
    else
        echo "Using Default Bootc Build Configuration"
        BOOTC_BUILD_CONFIG_PATH=/scripts/bootc/config.toml
    fi
    echo "config.toml:"
    cat ${BOOTC_BUILD_CONFIG_PATH}

    # login for local registry pulling # TODO: can we use token instead?
    if [[ "${container_image_repo_url}" == *"image-registry.openshift-image-registry.svc"* ]]; then
        echo "login to local registry"
        mkdir  /etc/containers/certs.d/image-registry.openshift-image-registry.svc:5000
        ln -s /var/run/secrets/kubernetes.io/serviceaccount/service-ca.crt  /etc/containers/certs.d/image-registry.openshift-image-registry.svc:5000/service-ca.crt
        podman login -u kubeadmin -p $(cat /var/run/secrets/kubernetes.io/serviceaccount/token) image-registry.openshift-image-registry.svc:5000
        podman pull "${container_image_repo_url}:${image_tag}" || error_exit "Failed to pull local bootc image"
    else
        # pull first to authenticate properly, if REGISTRY_AUTH_FILE is empty, it's ignored
        REGISTRY_AUTH_FILE=${auth_json_file} podman pull "${container_image_repo_url}:${image_tag}" || error_exit "Failed to pull bootc image"
    fi


    # execute bootc-image-builder
    # TODO: check if we can avoid this to drop the /store volumeMount
    # REGISTRY_AUTH_FILE is needed to access bib image
    REGISTRY_AUTH_FILE=${CLUSTER_PULL_SECRET_AUTH_FILE} podman run \
        -it \
        --privileged \
        --security-opt label=type:unconfined_t \
        -v ${BOOTC_BUILD_CONFIG_PATH}:/config.toml:ro \
        -v /store:/store \
        -v /var/lib/containers/storage:/var/lib/containers/storage \
        ${run_args} \
        ${BIB_IMAGE} \
        ${bib_args} \
        --rootfs xfs \
        --local \
        ${container_image_repo_url}:${image_tag} || error_exit "Failed to convert bootc image"
}

# Function to convert bootc container image to qcow2
# Input:
# 1. container_image_repo_url: The registry URL of the source container image.
# 2. image_tag: The tag of the source container image.
# 3. auth_json_file (can be empty): Path to the registry secret file to use for downloading the image.
# Output: qcow2 image at output/qcow2/disk.qcow2
function bootc_to_qcow2() {
    container_image_repo_url="${1}"
    image_tag="${2}"
    auth_json_file="${3}"

    mkdir output
    run_args="-v $(pwd)/output:/output"
    bib_args="--type qcow2"
    bootc_image_builder_conversion "${container_image_repo_url}" "${image_tag}" "${auth_json_file}" "${run_args}" "${bib_args}"
}

# Function to convert qcow2 image to raw image
# Input: qcow2 image
# Output: raw image
function convert_qcow2_to_raw() {
    qcow2disk=${1}
    rawdisk="$(basename -s qcow2 "${1}")raw"
    echo "Qcow2 disk name: ${qcow2disk}"
    echo "Raw disk name: ${rawdisk}"

    # Convert qcow2 to raw
    qemu-img convert -f qcow2 -O raw "${qcow2disk}" "${rawdisk}" ||
        error_exit "Failed to convert qcow2 to raw"

    echo "Successfully converted qcow2 to raw image name: ${rawdisk}"
    export RAW_IMAGE_PATH="${rawdisk}"
}

# Function to convert qcow2 image to vhd image
# Input: qcow2 image
# Output: vhddisk image
function convert_qcow2_to_vhd() {
    qcow2disk=${1}
    rawdisk="$(basename -s qcow2 "${1}")raw"
    vhddisk="$(basename -s qcow2 "${1}")vhd"
    echo "Qcow2 disk name: ${qcow2disk}"
    echo "Raw disk name: ${rawdisk}"
    echo "VHD disk name: ${vhddisk}"

    # Convert qcow2 to raw
    qemu-img convert -f qcow2 -O raw "${qcow2disk}" "${rawdisk}" ||
        error_exit "Failed to convert qcow2 to raw"

    # Convert raw to vhd
    resize_and_convert_raw_to_vhd_image "${rawdisk}"

    # Clean up the raw disk
    rm -f "${rawdisk}"

    echo "Successfully converted qcow2 to vhd image name: ${vhddisk}"
    export VHD_IMAGE_PATH="${vhddisk}"
}

# Function to resize and convert raw image to 1MB aligned vhd image for Azure
# Input: raw disk image
# Output: vhddisk image
function resize_and_convert_raw_to_vhd_image() {
    rawdisk=${1}
    vhddisk="$(basename -s raw "${1}")vhd"

    echo "Raw disk name: ${rawdisk}"
    echo "VHD disk name: ${vhddisk}"

    MB=$((1024 * 1024))
    size=$(qemu-img info -f raw --output json "$rawdisk" | jq '."virtual-size"') ||
        error_exit "Failed to get raw disk size"

    echo "Raw disk size: ${size}"

    rounded_size=$(((size + MB - 1) / MB * MB))

    echo "Rounded Size = ${rounded_size}"

    echo "Rounding up raw disk to 1MB"
    qemu-img resize -f raw "$rawdisk" "$rounded_size" ||
        error_exit "Failed to resize raw disk"

    echo "Converting raw to vhd"
    qemu-img convert -f raw -o subformat=fixed,force_size -O vpc "$rawdisk" "$vhddisk" ||
        error_exit "Failed to convert raw to vhd"

    echo "Successfully converted raw to vhd image name: ${vhddisk}"
    export VHD_IMAGE_PATH="${vhddisk}"
}

# Function to check image and convert to vhd if needed
# Input: image
# Output: vhddisk image
function convert_podvm_image_to_vhd() {
    image_path=${1}

    # Get the podvm image type. This sets the PODVM_IMAGE_FORMAT global variable
    get_podvm_image_format "${image_path}"

    case "${PODVM_IMAGE_FORMAT}" in
    "qcow2")
        # Convert the qcow2 image to vhd
        convert_qcow2_to_vhd "${image_path}"
        ;;
    "raw")
        # Convert the raw image to vhd
        resize_and_convert_raw_to_vhd_image "${image_path}"
        ;;
    "vhd")
        echo "PodVM image is already a vhd image"
        export VHD_IMAGE_PATH="${image_path}"
        ;;
    *)
        error_exit "Invalid podvm image format: ${PODVM_IMAGE_FORMAT}"
        ;;
    esac

    echo "Successfully converted podvm image to vhd image name: ${VHD_IMAGE_PATH}"

}

update_cm_annotation() {
    local key="$1"
    local value="$2"
    local cm="${3:-peer-pods-cm}"
    local ns="${4:-openshift-sandboxed-containers-operator}"

    echo "Adding annotation ${key}=${value} to configmap ${cm} in namespace ${ns}"

    if ! kubectl get configmap "${cm}" -n "${ns}" >/dev/null 2>&1; then
        echo "ConfigMap ${cm} does not exist in namespace ${ns}. Skipping."
        return
    fi

    kubectl annotate --overwrite configmap "${cm}" -n "${ns}" "${key}=${value}" ||
        error_exit "Failed to add annotation ${key} to ${cm}"

    echo "Annotation ${key}=${value} added successfully to ${cm}"
}

delete_cm_annotation() {
    local key="$1"
    local cm="${2:-peer-pods-cm}"
    local ns="${3:-openshift-sandboxed-containers-operator}"

    echo "Deleting annotation ${key} from configmap ${cm} in namespace ${ns}"

    if ! kubectl get configmap "${cm}" -n "${ns}" >/dev/null 2>&1; then
        echo "ConfigMap ${cm} does not exist in namespace ${ns}. Skipping."
        return
    fi

    kubectl annotate configmap "${cm}" -n "${ns}" "${key}-" ||
        error_exit "Failed to delete annotation ${key} from ${cm}"

    echo "Annotation ${key} deleted successfully from ${cm}"
}

# Global variables

# Set global variable for the source code directory
# The project layout has changed for the cloud-api-adaptor project
# https://github.com/confidential-containers/cloud-api-adaptor
export CAA_SRC_DOWNLOAD_DIR="/src/cloud-api-adaptor"
export CAA_SRC_DIR="/src/cloud-api-adaptor/src/cloud-api-adaptor"
