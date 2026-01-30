#!/bin/bash
#
# osc-kata-addons-install.sh
# Install addon artifacts (kernel) from container images
# Reuses functions from lib.sh
#

set -e

# Source the shared library functions
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

#######################################
# Update the configuration file
# Arguments:
#   $1: kernel path (empty if not installed)
# Returns:
#   0 on success
#######################################
update_config() {
    local kernel_path="$1"
    
    local config_file="/etc/kata-containers/kata-se/configuration.toml"
    
    if [ ! -f  chroot /host "$config_file" ]; then
        echo "Warning: Configuration file not found: $config_file"
        return 1
    fi
    
    echo "Updating configuration: $config_file"
    
    # Backup config
    chroot /host cp "$config_file" "${config_file}.backup-$(date +%s)"
    
    # Update kernel if provided
	chroot /host sed -i "s|^\(kernel[[:space:]]*=[[:space:]]*\)\".*\"|\1\"$kernel_path\"|g" "$config_file"
	echo "  Updated kernel: $kernel_path"

    return 0
}

#######################################
# Install addon artifacts
# Reads from environment variables
# Returns:
#   0 on success
#######################################
install_addons() {
    local kernel_src="${ADDON_KERNEL_PATH:?}"
	local kernel_file
    kernel_file="$(basename "$kernel_src")"

    local staged_dir="/host/var/lib/kata/addons"
    local staged_kernel="${staged_dir}/${kernel_src}"

    if [[ ! -f "$staged_kernel" ]]; then
        echo "ERROR: Staged kernel not found: $staged_kernel" >&2
        exit 1
    fi
    
    local install_dir="/host/etc/kata-containers"
	local kernel_installed="${install_dir}/${kernel_file}"
    local kernel_path="/etc/kata-containers/${kernel_file}"

	cp "$staged_kernel" "$kernel_installed"

	rm -rf $staged_dir

    echo "Kernel installed: $kernel_installed"
    echo "Kernel image $kernel_file checksum (sha256):"
    sha256sum "$kernel_installed"
    
	# Update kata configuration
    update_config "$kernel_path"

    echo "Addon installation completed"
    return 0
}

#######################################
# Uninstall addon artifacts
# Returns:
#   0 on success
#######################################
uninstall_addons() {
    local install_dir="/etc/kata-containers"
    echo "Uninstalling addon artifacts"
    
    local kernel_src="${ADDON_KERNEL_PATH:?}"
    # Remove installed artifacts
    chroot /host rm -f "$install_dir/$(basename "$kernel_src")"
     
    # Restore config backup
    local config_file="/etc/kata-containers/kata-se/configuration.toml"
    local backup=$(ls -t "${config_file}.backup-"* 2>/dev/null | head -1)
    [ -n "$backup" ] && [ -f "$backup" ] && chroot /host cp "$backup" "$config_file"
    
    echo "Addon artifacts uninstalled"
    return 0
}

# Main execution
action=${1:-install}

case "$action" in
    install)
        install_addons
        ;;
    uninstall)
        uninstall_addons
        ;;
    *)
        echo "Usage: $0 {install|uninstall}"
        exit 1
        ;;
esac
