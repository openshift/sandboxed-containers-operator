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

set_status_installed() {
	label_node "installed"
}

set_status_installing() {
	label_node "installing"
}

set_status_uninstalling() {
	label_node "uninstalling"
}

set_status_uninstalled() {
	label_node "uninstalled"
}

install_kata() {
	# Compares installed and available package versions. If any packages need
	# installing or upgrading, runs rpm-ostree install --apply-live so changes
	# take effect immediately without a node reboot. rpm-ostree cannot upgrade
	# local layered packages in-place, so outdated versions are uninstalled first.

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

		# Build install command with --apply-live to avoid a node reboot
		install_cmd="rpm-ostree install --apply-live"
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

		# rpm-ostree --apply-live updates /usr immediately but /etc changes from
		# RPMs only land after reboot (OSTree three-way merge). Copy the kata
		# CRI-O drop-ins from the RPM's factory-defaults location now so CRI-O
		# can see the runtime handlers without a reboot.
		for conf in /host/usr/etc/crio/crio.conf.d/50-kata*; do
			[ -f "$conf" ] && cp "$conf" /host/etc/crio/crio.conf.d/
		done

		# rpm-ostree --apply-live does not run RPM %post scripts on the live
		# system. The kata-containers %post script normally runs kata-osbuilder
		# to build a host-kernel-derived kata.kernel/kata.initrd at
		# /var/cache/kata-containers/osbuilder-images/. Run it manually here
		# to replicate what the RPM %post would have done.
		if [[ -x /host/usr/libexec/kata-containers/osbuilder/kata-osbuilder.sh ]]; then
			echo "Running kata-osbuilder.sh to generate kata VM images..."
			chroot /host /usr/libexec/kata-containers/osbuilder/kata-osbuilder.sh
			if [[ $? -eq 0 ]]; then
				echo "kata-osbuilder.sh completed successfully"
			else
				echo "WARNING: kata-osbuilder.sh failed, kata runtime may not work"
			fi
		else
			echo "WARNING: kata-osbuilder.sh not found, skipping VM image generation"
		fi

		# Run %posttrans steps from kata-containers.spec
		# Load SELinux modules for kata-monitor and container device access
		if command -v semodule >/dev/null 2>&1 && command -v setsebool >/dev/null 2>&1; then
			echo "Loading SELinux modules..."
			chroot /host semodule -i \
				/usr/share/udica/templates/base_container.cil \
				/usr/share/udica/templates/net_container.cil \
				/usr/share/kata-containers/defaults/osc_monitor.cil
			echo "Enabling container_use_devices SELinux boolean..."
			chroot /host setsebool -P container_use_devices 1
		else
			echo "WARNING: SELinux tools not available, skipping SELinux configuration"
		fi

		# Restart CRI-O to pick up the new runtime configuration. A reload
		# (SIGHUP) only re-reads the config struct; it does not rebuild the
		# internal OCI handler map, so kata would not appear as a valid runtime
		# until the next full CRI-O start. Existing pod processes survive the
		# restart — only new CRI operations are briefly unavailable.
		chroot /host systemctl restart crio

		set_status_installed
	fi
}

uninstall_kata() {
	# If kata-containers is already uninstalled, mark the node and sleep to
	# prevent DaemonSet pod restarts. Otherwise, stage removal via rpm-ostree
	# uninstall (takes effect at next reboot) and immediately clean up CRI-O
	# config so kata becomes unavailable without waiting for that reboot.
	installed_version=$(chroot /host rpm -q kata-containers 2>/dev/null || true)
	if [[ "$installed_version" == "package kata-containers is not installed" ]]; then
		echo "Package already uninstalled"
		set_status_uninstalled
		sleep infinity
	fi

	# Set installation status to uninstalling
	set_status_uninstalling

	# Run %preun steps from kata-containers.spec
	# Remove osc_monitor SELinux module and revoke container device access
	if command -v semodule >/dev/null 2>&1 && command -v setsebool >/dev/null 2>&1; then
		echo "Removing osc_monitor SELinux module..."
		chroot /host semodule -r osc_monitor || true
		echo "Disabling container_use_devices SELinux boolean..."
		chroot /host setsebool -P container_use_devices 0 || true
	else
		echo "WARNING: SELinux tools not available, skipping SELinux cleanup"
	fi

	# rpm-ostree uninstall does not support --apply-live (only install does).
	# Stage the removal for the next boot; clean up CRI-O config immediately
	# so kata becomes unavailable without waiting for a reboot.
	chroot /host /bin/bash -c "rpm-ostree uninstall $PACKAGES"

	# Remove the CRI-O drop-ins we copied during install
	rm -f /host/etc/crio/crio.conf.d/50-kata*

	# Remove the kata VM image symlinks created during --apply-live install
	rm -f /host/var/cache/kata-containers/osbuilder-images/kata.kernel \
	      /host/var/cache/kata-containers/osbuilder-images/kata.initrd

	# Restart CRI-O to drop the removed runtime configuration (same reason as
	# install: reload does not rebuild the internal OCI handler map).
	chroot /host systemctl restart crio

	set_status_uninstalled
	# Sleep to prevent pod restart while staged RPM removal awaits next reboot.
	sleep infinity
}

get_cloud_provider() {
	local provider

	# Run kubectl: capture rc and stdout
	if ! provider="$(kubectl get configmap/peer-pods-cm \
		-n openshift-sandboxed-containers-operator \
		-o jsonpath='{.data.CLOUD_PROVIDER}')"; then
		return
	fi

	# Reject empty/whitespace-only results
	if [[ -z "${provider//[[:space:]]/}" ]]; then
		error "get_cloud_provider: CLOUD_PROVIDER not set in configmap/peer-pods-cm"
		return
	fi

	echo "${provider}"
}

# Reads worker version label and extracts the OpenShift version (prefix before '_').
get_worker_node_version_ibmcloud() {
	local version
	local raw_version

	if ! raw_version="$(kubectl get "nodes/${NODE_NAME}" \
		-o jsonpath='{.metadata.labels.ibm-cloud\.kubernetes\.io/worker-version}')"; then
		error "get_worker_node_version_ibmcloud: kubectl failed to read worker-version label on node ${NODE_NAME}"
		return
	fi

	if [[ -z "${raw_version//[[:space:]]/}" ]]; then
		error "get_worker_node_version_ibmcloud: worker-version label is empty on node ${NODE_NAME}"
		return
	fi

	# Extract the part before the first underscore
	version="${raw_version%%_*}"

	if [[ -z "${version//[[:space:]]/}" ]]; then
		error "get_worker_node_version_ibmcloud: cannot parse '${raw_version}' as an OpenShift version"
		return
	fi

	echo "${version}"
}

# get_extension_image VERSION
# Resolves the rhel-coreos-extensions image for a given release VERSION.
get_extension_image() {
	[[ -n ${1-} ]] || {
		error "get_extension_image: usage: get_extension_image VERSION"
		return
	}

	local version=$1
	local image

	if ! image="$(oc adm release info --image-for rhel-coreos-extensions "$version")"; then
		error "get_extension_image: 'oc adm release info' failed for version: $version"
		return
	fi

	if [[ -z "${image//[[:space:]]/}" ]]; then
		error "get_extension_image: empty image string for version: $version"
		return
	fi

	echo "${image}"
}

# FIXME : This logic should be moved into the Operator
rpm-extensions() {
	local provider
	local extension_image

	provider="$(get_cloud_provider)"

	case "${provider}" in
	ibmcloud)
		local version
		version="$(get_worker_node_version_ibmcloud)"
		extension_image="$(get_extension_image $version)"
		;;

	*)
		extension_image=$EXTENSION_IMAGE
		;;

	esac

	mkdir -p "/usr/share/rpm-ostree/extensions"
	extract_container_image "$extension_image" "/usr/share/rpm-ostree/extensions" "/usr/share/rpm-ostree" "/tmp/regauth/auth.json"
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

		install_kata

		# Install addon artifacts, if ADDON_IMAGE is present
		[ -n "${ADDON_IMAGE:-}" ] && /scripts/osc-kata-addons-install.sh install

		sleep infinity
		;;
	uninstall)
		client_tools

		#/osc-log-level.sh "$action"

		# Call addon uninstaller if configured
		[ -n "${ADDON_IMAGE:-}" ] && /scripts/osc-kata-addons-install.sh uninstall

		uninstall_kata
		;;
	*)
		echo "Usage: $0 {install|uninstall}"
		exit 1
		;;
	esac
}

# Call main with the argument passed to the script
main "$@"
