#!/bin/bash

# TODO: Use Envars for path and code dependant variables in the script

# include common functions from lib.sh
# shellcheck source=/dev/null
source "$(dirname "$0")"/lib.sh

set -xeuo pipefail

ARCH=$(chroot /host uname -m)

case "$ARCH" in
    x86_64)
        PACKAGES="capstone daxctl-libs edk2-ovmf ipxe-roms-qemu kata-containers libfdt libpmem libpng librdmacm ndctl-libs pixman qemu-img qemu-kvm-common qemu-kvm-core seabios-bin seavgabios-bin virtiofsd"
        ;;

    s390x)
        PACKAGES="capstone kata-containers libfdt libpng pixman qemu-img qemu-kvm-common qemu-kvm-core virtiofsd"
        ;;

    *)
        echo "ERROR: unsupported architecture: $ARCH" >&2
        exit 1
        ;;
esac


# label the node with the passed state
label_node() {
	local state="$1"
	kubectl label node "${NODE_NAME}" "${NODE_LABEL}=${state}" --overwrite
}

# Check whether a package has staged updates
check_package_updated() {
	local pkg="$1"
	local booted staged

	booted=$(chroot /host /bin/bash -c "rpm-ostree status --json | jq -r '.deployments[] | select(.booted==true) | .checksum'")
	staged=$(chroot /host /bin/bash -c "rpm-ostree status --json | jq -r '.deployments[] | select(.staged==true) | .checksum'")

	if [ -n "$staged" ]; then
		if chroot /host rpm-ostree db diff "$booted" "$staged" | grep -q "$pkg"; then
			return 0 # Package was updated
		fi
	fi

	return 1 # Package not updated or no staged deployment
}

# Function to check if a reboot is required by looking for "Staged: yes"
is_reboot_required() {
	check_package_updated "kata-containers"
}

# Loop until reboot is no longer required
wait_for_reboot_clear() {
	while is_reboot_required; do
		echo "Reboot required"
		set_status_waiting_for_reboot
		sleep 60
	done
}

set_status_installed() {
	label_node "installed"
}

set_status_installing() {
	label_node "installing"
}

set_status_waiting_for_reboot() {
	label_node "waiting_for_reboot"
}

set_status_uninstalling() {
	label_node "uninstalling"
}

set_status_uninstalled() {
	label_node "uninstalled"
}

install() {
	# Initial wait: avoid doing anything if a previous staged update is pending
	wait_for_reboot_clear

	# This compares installed and available versions of packages.
	# If updates or installations are needed, it prepares and runs an rpm-ostree install command with uninstall flags.
	# rpm-ostree can't update local packages directly, so old versions must be explicitly removed.
	# If no action is needed, it sleeps indefinitely to prevent pod restarts in a DaemonSet.

	local uninstall_list=()
	local install_rpms=()

	# Process each package
	for package in $PACKAGES; do
		# Find the RPM file
		rpm_path=$(find /usr/share/rpm-ostree/extensions/ -maxdepth 1 -type f -name "${package}-*.rpm" 2>/dev/null | head -n1)
		if [[ -z "$rpm_path" ]]; then
			echo "No RPM found for $package"
			continue
		fi

		# Get available version
		available_version=$(rpm -qp "$rpm_path")

		# Check installed version
		installed_version=$(chroot /host rpm -q "$package" 2>/dev/null || true)

		# If the package is not installed rpm -q gives back an error message instead of the package name
		if [[ "$installed_version" == "$package-"* ]]; then
			if [[ "$installed_version" != "$available_version" ]]; then
				echo "$package is outdated: $installed_version -> $available_version"
				uninstall_list+=("$package")
				install_rpms+=("$rpm_path")
			else
				echo "$package is already up-to-date: $installed_version"
			fi
		else
			echo "$package is not installed"
			install_rpms+=("$rpm_path")
		fi
	done

	# If nothing to install, exit early
	if [[ ${#install_rpms[@]} -eq 0 ]]; then
		set_status_installed
	else
		# Set installation status to installing
		set_status_installing

		# Prepare working directory
		mkdir -p /host/tmp/extensions/

		# Copy only needed RPMs
		for rpm_path in "${install_rpms[@]}"; do
			cp "$rpm_path" /host/tmp/extensions/
		done

		# Build install command
		install_cmd="rpm-ostree install"
		for pkg in "${uninstall_list[@]}"; do
			install_cmd+=" --uninstall=$pkg"
		done
		for rpm_path in "${install_rpms[@]}"; do
			rpm_filename=$(basename "$rpm_path")
			install_cmd+=" /tmp/extensions/$rpm_filename"
		done

		# Run install inside chroot
		echo "Running in chroot: $install_cmd"
		chroot /host bash -c "$install_cmd"

		# Clean up temp dir
		rm -rf /host/tmp/extensions/

		# Wait again: rpm-ostree install stages changes, requiring a reboot
		wait_for_reboot_clear
	fi
}

uninstall() {
	# Initial wait: avoid doing anything if a previous staged update is pending
	wait_for_reboot_clear

	# Check if kata-containers is installed
	# If kata-containers is not installed we are done
	# Create uninstalled file to signal readiness
	# Sleep infinity to prevent pod restart (DaemonSets always restart exited pods)
	installed_version=$(chroot /host rpm -q kata-containers 2>/dev/null || true)
	if [[ "$installed_version" == "package kata-containers is not installed" ]]; then
		echo "Package already uninstalled"
		set_status_uninstalled
		sleep infinity
	fi

	# Set installation status to uninstalling
	set_status_uninstalling

	# Uninstall extensions from the node
	chroot /host /bin/bash -c "rpm-ostree uninstall $PACKAGES"

	# Wait again: rpm-ostree uninstall stages changes, requiring a reboot
	wait_for_reboot_clear
}

rpm-extensions() {
	mkdir -p "/usr/share/rpm-ostree/extensions"
	extract_container_image "$EXTENSION_IMAGE" "/usr/share/rpm-ostree/extensions" "/usr/share/rpm-ostree" "/tmp/regauth/auth.json"
}

main() {
	[[ -z "${NODE_NAME:-}" ]] && {
		echo "ERROR: NODE_NAME env var must be set."
		exit 1
	}

	#[[ -z "${LOG_LEVEL}" ]] || {
	#        echo "ERROR: LOG_LEVEL env var must be set."
	#        exit 1
	#}

	[[ -z "${CLI_IMAGE:-}" ]] && {
		echo "ERROR: CLI_IMAGE env var must be set."
		exit 1
	}

	[[ -z "${EXTENSION_IMAGE:-}" ]] && {
		echo "ERROR: EXTENSION_IMAGE env var must be set."
		exit 1
	}

	local action="${1:-}"

	case "$action" in
	install)
		client_tools

		rpm-extensions

		#/osc-log-level.sh "$action" "$LOG_LEVEL"

		#/osc-configs-script.sh "$action"

		install

		sleep infinity
		;;
	uninstall)
		client_tools

		#/osc-log-level.sh "$action"

		#/osc-configs-script.sh "$action"

		uninstall
		;;
	*)
		echo "Usage: $0 {install|uninstall}"
		exit 1
		;;
	esac
}

# Call main with the argument passed to the script
main "$@"
