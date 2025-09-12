#!/bin/bash
# FILEPATH: ibmcloud-podvm-image-handler.sh

# This script is used to import PodVM images to IBM Cloud
# The basic assumption is that the required variables are set as environment variables in the pod
# Typically the variables are read from configmaps and set as environment variables in the pod
# The script will be called with one of the following options:
# Create image (-c)
# Delete image (-C)

# include common functions from lib.sh
# shellcheck source=/dev/null
# The directory is where ibmcloud-podvm-image-handler.sh is located
source "$(dirname "$0")"/lib.sh

# function to download and install ibmcloud cli

function install_ibmcloud_cli() {
    # Install ibmcloud cli
    # If any error occurs, exit the script with an error message

    # Check if ibmcloud cli is already installed
    if command -v ibmcloud &>/dev/null; then
        echo "ibmcloud cli is already installed"
        return
    fi

    # Download ibmcloud cli
    curl -fsSL https://clis.cloud.ibm.com/install/linux -o /tmp/ibmcloud_install.sh ||
        error_exit "Failed to download ibmcloud cli"

    # Install ibmcloud cli
    sh /tmp/ibmcloud_install.sh ||
        error_exit "Failed to execute ibmcloud cli installer"

    # Install ibmcloud cli plugins
    ibmcloud plugin install -a ||
        error_exit "Failed to install ibmcloud cli plugins"

    # Clean up temporary files
    rm -f "/tmp/ibmcloud_install.sh"
}

function create_image() {
    error_exit "currently not supported"
}

function delete_image() {
    error_exit "currently not supported"
}

# display help message

function display_help() {
    echo "This script is used to import PodVM images to IBM Cloud"
    echo "Usage: $0 [-c|-C] [-- install_binaries|install_rpms|install_cli]"
    echo "Options:"
    echo "-c  Create image"
    echo "-C  Delete image"
}

# main function

if [ "$#" -eq 0 ]; then
    display_help
    exit 1
fi

if [ "$1" = "--" ]; then
    shift
    # Handle positional parameters
    case "$1" in

    install_binaries)
        install_binary_packages
        ;;
    install_rpms)
        install_rpm_packages
        ;;
    install_cli)
        install_ibmcloud_cli
        ;;
    *)
        echo "Unknown argument: $1"
        exit 1
        ;;
    esac
else
    while getopts "cCh" opt; do
        verify_vars
        case ${opt} in
        c)
            create_image
            ;;
        C)
            delete_image
            ;;
        h)
            # Display help
            display_help
            exit 0
            ;;
        *)
            # Invalid option
            display_help
            exit 1
            ;;
        esac
    done
fi
