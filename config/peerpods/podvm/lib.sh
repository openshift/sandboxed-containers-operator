#!/bin/bash
# Contains common functions used by the scripts

set -x

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

install_binary_packages() {
    # Define the required binary packages
    REQUIRED_BINARY_PACKAGES=(
        "packer=https://releases.hashicorp.com/packer/1.9.4/packer_1.9.4_linux_amd64.zip"
        "kubectl=https://mirror.openshift.com/pub/openshift-v4/x86_64/clients/ocp/4.14.9/openshift-client-linux.tar.gz"
        "yq=https://github.com/mikefarah/yq/releases/download/v4.35.2/yq_linux_amd64.tar.gz"
    )

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
                echo "Unsupported archive format for ${package_name}. Skipping."
                continue
            fi

            echo "Marking  ${install_dir}/${package_name} executable"
            # yq extracted file name is yq_linux_amd64. Rename it
            [[ "${package_name}" == "yq" ]] &&
                mv "${install_dir}/yq_linux_amd64" "${install_dir}/yq"

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

    # Delete the source code directory if it exists
    [[ -d "${CAA_SRC_DIR}" ]] &&
        rm -rf "${CAA_SRC_DIR}"

    # Create the root directory for source code
    mkdir -p "${CAA_SRC_DIR}"

    # Download the source code from GitHub
    git clone "${CAA_SRC}" "${CAA_SRC_DIR}" ||
        error_exit "Failed to download source code from GitHub"

    # Checkout the required commit
    cd "${CAA_SRC_DIR}" ||
        error_exit "Failed to change directory to ${CAA_SRC_DIR}"

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

    # Download the podvm binaries and copy it to the podvm/files directory
    tar xvf /payload/podvm-binaries.tar.gz -C "${podvm_dir}"/files ||
        error_exit "Failed to download podvm binaries"

    mkdir -p "${podvm_dir}"/files/pause_bundle # workaround to avoid pause image requirement

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
}

# Global variables

# Set global variable for the source code directory
export CAA_SRC_DIR="/src/cloud-api-adaptor"
