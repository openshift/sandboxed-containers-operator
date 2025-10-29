#!/bin/bash

# TODO: Use Envars for path and code dependant variables in the script

# include common functions from lib.sh
# shellcheck source=/dev/null
source "$(dirname "$0")"/lib.sh

set -xeuo pipefail

PACKAGES="capstone daxctl-libs edk2-ovmf ipxe-roms-qemu kata-containers libfdt libpmem libpng librdmacm ndctl-libs pixman qemu-img qemu-kvm-common qemu-kvm-core seabios-bin seavgabios-bin virtiofsd"

# Format: "absolute_source_path:absolute_dest_path:octal_mode"
FILES=(
	"/files/50-kata-remote:/host/etc/crio/crio.conf.d/50-kata-remote:0644"
	"/files/configuration-remote.toml:/host/opt/kata/configuration-remote.toml:0420"
)

copy_file() {
	local src="$1" dest="$2" perm="$3"
	if [[ -f "$src" ]]; then
		if [[ -e "$dest" ]]; then
			echo "$dest already exists, skipping"
		else
			# GNU coreutils install: create parents (-D) and set mode (-m)
			install -D -m "$perm" "$src" "$dest"
			echo "Installed $(basename "$src") -> $dest (mode $perm)"
		fi
	else
		echo "Warning: $(basename "$src") not found"
	fi
}

remove_file() {
	local dest="$1"
	if [[ -e "$dest" ]]; then
		rm -f "$dest"
		echo "Removed $dest"
	else
		echo "Info: $dest not present; skipping"
	fi
}

copy_kata_remote_config_files() {
	echo "Starting configuration copy..."

	for entry in "${FILES[@]}"; do
		IFS=: read -r src dest perm <<<"$entry"
		copy_file "$src" "$dest" "$perm"
	done

	echo "Configuration copy completed"
}

remove_kata_remote_config_files() {
	echo "Starting configuration removal..."

	for entry in "${FILES[@]}"; do
		IFS=: read -r _src dest _perm <<<"$entry"
		remove_file "$dest"
	done

	echo "Configuration removal completed"
}

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

set_status_waiting_to_install() {
	label_node "waiting_to_install"
}

set_status_waiting_to_uninstall() {
	label_node "waiting_to_uninstall"
}

set_status_installed() {
	label_node "installed"
}

set_status_waiting_for_reboot() {
	label_node "waiting_for_reboot"
}

set_status_uninstalled() {
	label_node "uninstalled"
}

exec_on_host() {
	nsenter --target 1 --mount --pid -- bash -c "$@"
}

wait_till_node_is_ready() {
	local ready="False"

	while ! [[ "${ready}" == "True" ]]; do
		sleep 2s
		ready=$(kubectl get node $NODE_NAME -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}')
	done
}

wait_for_kubelet_cri_recovery() {
	while true; do
		# Check if crictl info is available
		if ! exec_on_host "crictl info --output json >/dev/null 2>&1"; then
			sleep 1
			continue
		fi

		# Get cri-o status
		local runtime_ready network_ready
		runtime_ready=$(exec_on_host "crictl info --output json | jq -r '.status.conditions[] | select(.type==\"RuntimeReady\") | .status'")
		network_ready=$(exec_on_host "crictl info --output json | jq -r '.status.conditions[] | select(.type==\"NetworkReady\") | .status'")

		# Confirm cri-o is ready
		if [[ "$runtime_ready" == "true" && "$network_ready" == "true" ]]; then
			# Confirm kubelet is healthy
			#if exec_on_host "curl -sf http://127.0.0.1:10248/healthz >/dev/null"; then
			return 0
			#fi
		fi

		sleep 1
	done
}

restart_crio() {
	# Restart crio
	exec_on_host "systemctl daemon-reload"
	exec_on_host "systemctl restart crio"

	wait_till_node_is_ready

	# Wait for crio and kubelet
	wait_for_kubelet_cri_recovery
}

check_node_label() {
	local expected_value="$1"
	local escaped_label="${NODE_LABEL//./\\.}" # escape dots

	# Get the label value
	local label_value=$(kubectl get node "$NODE_NAME" -o jsonpath="{.metadata.labels.$escaped_label}")

	# Compare with a string
	if [[ "$label_value" == "$expected_value" ]]; then
		return 0
	else
		return 1
	fi
}

wait_for_label() {
	local expected_value="$1"
	echo "Waiting for label '$NODE_LABEL' on node '$NODE_NAME' to become '$expected_value'..."
	while ! check_node_label "$expected_value"; do
		echo "Label not matched yet. Retrying in 5s..."
		sleep 5
	done
	echo "Label matched!"
}

waiting_for_schedule() {
	local expected_label_value="$1"
	echo "Waiting for operator's schedule"
	while ! check_node_label $expected_label_value; do
		sleep 5
	done
}

waiting_for_install_schedule() {
	waiting_for_schedule "installing"
}

waiting_for_uninstall_schedule() {
	waiting_for_schedule "uninstalling"
}

install_kata() {
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

	# TODO: Check whether crio's loaded the kata config or not
	# If nothing to install, exit early
	if [[ ${#install_rpms[@]} -eq 0 ]]; then
		set_status_installed

		return 0
	fi

	# Set installation status to waiting and wait for the operator's signal
	set_status_waiting_to_install
	waiting_for_install_schedule

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
	chroot /host bash -c "rpm-ostree apply-live --allow-replacement"

	# Clean up temp dir
	rm -rf /host/tmp/extensions/

	# Copy configs
	copy_kata_remote_config_files

	# Install SELinux policy
	semodule -i /usr/share/kata-containers/defaults/osc_monitor.cil

	# Restart crio
	restart_crio

	set_status_installed
}

uninstall_kata() {
	# Initial wait: avoid doing anything if a previous staged update is pending
	wait_for_reboot_clear

	# Check if kata-containers is installed
	# If kata-containers is not installed we are done
	# Sleep infinity to prevent pod restart (DaemonSets always restart exited pods)
	installed_version=$(chroot /host rpm -q kata-containers 2>/dev/null || true)
	if [[ "$installed_version" == "package kata-containers is not installed" ]]; then
		echo "Package already uninstalled"
		set_status_uninstalled
		return 0
	fi

	# Set installation status to uninstalling
	set_status_uninstalling

	# Uninstall extensions from the node
	chroot /host /bin/bash -c "rpm-ostree uninstall $PACKAGES"

	# Remove Config
	remove_kata_remote_config_files

	# Wait again: rpm-ostree uninstall stages changes, requiring a reboot
	wait_for_reboot_clear

	### Uninstall without reboot
	### Does not work currently
	### This won’t execute because the wait_for_reboot_clear function blocks it

	# Check if kata-containers is installed
	# If kata-containers is not installed we are done
	# Sleep infinity to prevent pod restart (DaemonSets always restart exited pods)
	installed_version=$(chroot /host rpm -q kata-containers 2>/dev/null || true)
	if [[ "$installed_version" == "package kata-containers is not installed" ]]; then
		echo "Package already uninstalled"
		set_status_uninstalled
		return 0
	fi

	# Set installation status to waiting and wait for the operator's signal
	set_status_waiting_to_uninstall
	waiting_for_uninstall_schedule

	# Uninstall extensions from the node
	chroot /host /bin/bash -c "rpm-ostree uninstall $PACKAGES"
	chroot /host bash -c "rpm-ostree apply-live --allow-replacement"

	# Remove Config
	remove_kata_remote_config_files

	# Remove SELinux policy
	semodule -r osc_monitor

	# Restart crio
	# TODO: Maybe reload is enough after removal
	restart_crio

	set_status_uninstalled
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

	[[ -z "${NODE_LABEL:-}" ]] && {
		echo "ERROR: NODE_LABEL env var must be set."
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

		install_kata
		;;
	uninstall)
		client_tools

		#/osc-log-level.sh "$action"

		uninstall_kata
		;;
	*)
		echo "Usage: $0 {install|uninstall}"
		exit 1
		;;
	esac

	sleep infinity
}

# Call main with the argument passed to the script
main "$@"
