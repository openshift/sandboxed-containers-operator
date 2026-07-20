#!/bin/bash

# TODO: Use Envars for path and code dependant variables in the script

# include common functions from lib.sh
# shellcheck source=/dev/null
source "$(dirname "$0")"/lib.sh

set -euo pipefail

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

extract_scriptlet() {
    # Extract the script from the given phase in the RPM
    # RPM scriptlet $1 meanings:
    #   1 = initial installation
    #   0 = package removal (preuninstall/postuninstall)
    local phase=$1
    local rpm_path="$2"
    local arg_value=${3:-1}  # Default to 1 (install) if not specified
    rpm -qp --scripts "$rpm_path" |
        awk '/^'$phase' scriptlet \(using .*\):/{found=1;next} /^[a-z]+ (scriptlet \(using .*\):|program:)/{found=0} found { gsub("\\$1","'"$arg_value"'"); print }'
}

install_kata() {
	# Compares installed and available package versions. If any packages need
	# installing or upgrading, runs rpm-ostree install --apply-live so changes
	# take effect immediately without a node reboot. rpm-ostree cannot upgrade
	# local layered packages in-place, so outdated versions are uninstalled first.

	# If a prior uninstall_kata staged the removal of kata-containers, rpm -q
	# below still sees kata as installed (the booted deployment still has it)
	# and would skip reinstalling — leaving the staged removal to silently drop
	# kata at the next reboot. Detect and cancel that staged removal now.
	if chroot /host bash -c "
		s=\$(rpm-ostree status --json)
		n_staged=\$(echo \"\$s\" | jq '[.deployments[] | select(.staged)] | length')
		[[ \$n_staged -eq 0 ]] && exit 1
		n_kata_staged=\$(echo \"\$s\" | jq '[.deployments[] | select(.staged) | ((.\"requested-packages\" // []) + (.\"requested-local-packages\" // []))[] | select(startswith(\"kata-containers\"))] | length')
		n_kata_booted=\$(echo \"\$s\" | jq '[.deployments[] | select(.booted) | ((.\"requested-packages\" // []) + (.\"requested-local-packages\" // []))[] | select(startswith(\"kata-containers\"))] | length')
		[[ \$n_kata_booted -gt 0 && \$n_kata_staged -eq 0 ]]
	"; then
		echo "Staged removal of kata-containers detected; cancelling pending deployment"
		chroot /host rpm-ostree cleanup -p
	fi

	# Mirror case: a prior install_kata staged packages but the live overlay
	# was lost (e.g., pod crashed, or prior image lacked --apply-live).
	# Packages sit in the staged deployment but rpm -q (which reads the
	# booted rpmdb) reports them as "not installed", so the version-check
	# loop below would try to rpm-ostree install them again — which fails
	# with "already layered". Apply the staged deployment live instead.
	if chroot /host bash -c "
		s=\$(rpm-ostree status --json)
		n_staged=\$(echo \"\$s\" | jq '[.deployments[] | select(.staged)] | length')
		[[ \$n_staged -eq 0 ]] && exit 1
		n_kata_staged=\$(echo \"\$s\" | jq '[.deployments[] | select(.staged) | ((.\"requested-packages\" // []) + (.\"requested-local-packages\" // []))[] | select(startswith(\"kata-containers\"))] | length')
		n_kata_booted=\$(echo \"\$s\" | jq '[.deployments[] | select(.booted) | ((.\"requested-packages\" // []) + (.\"requested-local-packages\" // []))[] | select(startswith(\"kata-containers\"))] | length')
		[[ \$n_kata_staged -gt 0 && \$n_kata_booted -eq 0 ]]
	"; then
		echo "Packages staged for installation but not live; applying staged deployment"
		chroot /host rpm-ostree apply-live
	fi

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

	# If all packages are live and up-to-date, check they're also staged in
	# rpm-ostree. After an uninstall cycle without a reboot, packages remain
	# live via --apply-live but are absent from the staged deployment. Without
	# re-staging, a subsequent uninstall would fail with "not currently
	# requested". Force a re-stage if kata is not tracked in any deployment.
	if [[ ${#install_rpms[@]} -eq 0 ]]; then
		local n_kata_tracked
		n_kata_tracked=$(chroot /host bash -c '
			rpm-ostree status --json | jq "[.deployments[] |
			  ((.\"requested-packages\" // []) + (.\"requested-local-packages\" // []))[]]
			| map(select(startswith(\"kata-containers\"))) | length" 2>/dev/null || echo 0
		')
		if [[ "${n_kata_tracked:-0}" -eq 0 ]]; then
			echo "Packages live but absent from rpm-ostree deployments; re-staging all packages"
			for package in $PACKAGES; do
				rpm_path=$(find /usr/share/rpm-ostree/extensions/ -maxdepth 1 -type f -name "${package}-*.rpm" 2>/dev/null | head -n1)
				[[ -n "$rpm_path" ]] && install_rpms+=("$rpm_path")
			done
		fi
	fi

	local fresh_install=false

	if [[ ${#install_rpms[@]} -gt 0 ]]; then
		fresh_install=true
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

		# rpm-ostree install --apply-live updates /usr immediately but
		# /etc changes from RPMs only land after reboot
		# (OSTree three-way merge).
		# Extract the /etc files we find in the rpms.
		# This is notably necessary for CRI-O drop-ins so that the CRI-O
		# restart below is effective
		for rpm_path in "${install_rpms[@]}"; do
			mapfile -t etc_files < <(rpm -qpl "$rpm_path" | sed -n 's|^/etc/|etc/|p')
			local real_etc_files=()
			for f in "${etc_files[@]}"; do
				[[ -f "/host/usr/$f" ]] && real_etc_files+=("$f")
			done
			if [[ ${#real_etc_files[@]} -gt 0 ]]; then
				(cd /host/usr; tar cf - "${real_etc_files[@]}") | chroot /host tar xf -
			fi
		done

		# rpm-ostree --apply-live does not run RPM scriptlets
		# Run the scriptlets manually in host context
		# Normally this includes:
		# 1. postinstall: Building host-kernel derived kata VM image
		# 2. posttrans:	  Running the SELinux steps
		for phase in postinstall posttrans; do
			for rpm_path in "${install_rpms[@]}"; do
				scriptlet="$(extract_scriptlet "$phase" "$rpm_path")"
				[[ -n "$scriptlet" ]] && chroot /host /bin/sh -c "$scriptlet"
			done
		done

		# The posttrans scriptlet installs SELinux modules (e.g. osc_monitor)
		# into the module store and compiles the policy binary on disk, but
		# --apply-live does not reload the kernel's in-memory SELinux policy.
		# Without this, new SELinux types are invisible until the next reboot.
		chroot /host /sbin/load_policy

		# Clean up temp dir
		rm -rf /host/tmp/extensions/
	fi

	# Post-install verification and repair. If a prior run completed
	# rpm-ostree install but crashed before finishing post-install steps
	# (/etc extraction, scriptlets, CRI-O restart), the packages look
	# installed but the node is half-configured. Each check is idempotent.
	local needs_crio_restart="$fresh_install"

	# Ensure /etc files from RPMs are in place (CRI-O drop-in configs).
	# On fresh install these were just extracted above; this catches the
	# case where a prior run crashed before /etc extraction completed.
	for package in $PACKAGES; do
		rpm_path=$(find /usr/share/rpm-ostree/extensions/ -maxdepth 1 -type f -name "${package}-*.rpm" 2>/dev/null | head -n1)
		[[ -z "$rpm_path" ]] && continue
		mapfile -t etc_files < <(rpm -qpl "$rpm_path" | sed -n 's|^/etc/|etc/|p')
		local repair_files=()
		for f in "${etc_files[@]}"; do
			[[ -f "/host/usr/$f" ]] && ! chroot /host test -e "/$f" && repair_files+=("$f")
		done
		if [[ ${#repair_files[@]} -gt 0 ]]; then
			echo "Post-install configs missing, extracting from RPM: ${repair_files[*]}"
			(cd /host/usr; tar cf - "${repair_files[@]}") | chroot /host tar xf -
			needs_crio_restart=true
		fi
	done

	# Ensure scriptlets have run (SELinux modules). The posttrans
	# scriptlet installs the osc_monitor SELinux module; if it is absent,
	# re-run all scriptlets and reload the policy.
	if ! chroot /host semodule -l 2>/dev/null | grep -q osc_monitor; then
		echo "SELinux module osc_monitor missing, running scriptlets"
		for package in $PACKAGES; do
			rpm_path=$(find /usr/share/rpm-ostree/extensions/ -maxdepth 1 -type f -name "${package}-*.rpm" 2>/dev/null | head -n1)
			[[ -z "$rpm_path" ]] && continue
			for phase in postinstall posttrans; do
				scriptlet="$(extract_scriptlet "$phase" "$rpm_path")"
				[[ -n "$scriptlet" ]] && chroot /host /bin/sh -c "$scriptlet"
			done
		done
		chroot /host /sbin/load_policy
	fi

	# The module store (semodule -l) may list osc_monitor even though the
	# kernel's in-memory policy does not contain the type. This happens
	# when load_policy ran before a CRI-O restart that killed this pod:
	# the on-disk store survives but the kernel policy reverts. Verify the
	# type is actually usable and reload if not.
	if chroot /host semodule -l 2>/dev/null | grep -q osc_monitor &&
	   ! chroot /host runcon -t osc_monitor.process /bin/true 2>/dev/null; then
		echo "SELinux type osc_monitor.process not in kernel policy, reloading"
		chroot /host /sbin/load_policy
	fi

	# Ensure the kata VM kernel/initrd exist. The kata-osbuilder.sh script
	# builds a host-kernel-derived initrd and symlinks the kernel. When
	# packages were already present or the RPM postinstall scriptlet failed
	# silently inside the DaemonSet chroot, these files may be missing.
	if ! chroot /host test -e /var/cache/kata-containers/osbuilder-images/kata.kernel; then
		echo "kata VM kernel/initrd missing, running kata-osbuilder.sh"
		chroot /host mkdir -p /var/cache/kata-containers/osbuilder-images
		chroot /host /usr/libexec/kata-containers/osbuilder/kata-osbuilder.sh
	fi

	# Set label before CRI-O restart (restart kills this pod).
	set_status_installed

	if [[ "$needs_crio_restart" == "true" ]]; then
		# Restart CRI-O to pick up the new runtime configuration. A reload
		# (SIGHUP) only re-reads the config struct; it does not rebuild the
		# internal OCI handler map, so kata would not appear as a valid runtime
		# until the next full CRI-O start.
		chroot /host systemctl restart crio
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

	# rpm-ostree uninstall does not support --apply-live (only install does).
	# Stage the removal for the next boot; clean up CRI-O config immediately
	# so kata becomes unavailable without waiting for a reboot.
	#
	# Only uninstall packages that rpm-ostree is currently tracking. After a
	# reinstall where packages were live but not re-staged, they won't appear
	# in requested-packages and the uninstall command would fail with "not
	# currently requested". Packages absent from all deployments are already
	# absent from the staged deployment and need no further action.
	local ostree_pkg_list
	if ! ostree_pkg_list=$(chroot /host bash -c '
		set -o pipefail
		rpm-ostree status --json | jq -r "[.deployments[] |
		  ((.\"requested-packages\" // []) + (.\"requested-local-packages\" // []))[]]
		| .[]" 2>/dev/null
	'); then
		echo "Failed to inspect rpm-ostree deployments" >&2
		exit 1
	fi
	local uninstall_args=()
	for pkg in $PACKAGES; do
		echo "$ostree_pkg_list" | grep -xqF "$pkg" && uninstall_args+=("$pkg")
	done
	if [[ ${#uninstall_args[@]} -gt 0 ]]; then

		# Manually run preuninstall phase
		# For Kata this is primarily SELinux module removal for osc_monitor
		for pkg in "${uninstall_args[@]}"; do
			# Get the list of /etc files to remove before uninstalling
			chroot /host rpm -ql "$pkg" 2>/dev/null | grep "^/etc/crio/crio.conf.d" | while read -r file; do
				rm -f "/host$file"
			done

			# Extract and run the preuninstall scriptlet with $1=0 (package removal)
			rpm_path=$(find /usr/share/rpm-ostree/extensions/ -maxdepth 1 -type f -name "${pkg}-*.rpm" 2>/dev/null | head -n1)
			if [[ -n "$rpm_path" ]]; then
				scriptlet="$(extract_scriptlet preuninstall "$rpm_path" 0)"
				[[ -n "$scriptlet" ]] && chroot /host /bin/sh -c "$scriptlet"
			fi
		done

		# Run actual uninstall phase
		chroot /host /bin/bash -c "rpm-ostree uninstall ${uninstall_args[*]}"
	else
		# Packages are no longer tracked by rpm-ostree — a prior run already
		# staged the removal and cleaned up CRI-O config. This happens when
		# CRI-O restart killed this pod before the status label was set.
		echo "No kata packages tracked in rpm-ostree deployments; uninstall already staged"
		set_status_uninstalled
		sleep infinity
	fi

	# Mark the node before restarting CRI-O: the restart terminates all
	# containers on the node (including this pod), so anything after it
	# is best-effort. On the next pod restart the rpm-ostree check above
	# will detect the staged removal and exit cleanly.
	set_status_uninstalled
	echo "Uninstallation complete on node ${NODE_NAME}, restarting CRI-O"

	# Restart CRI-O to drop the removed runtime configuration (same reason as
	# install: reload does not rebuild the internal OCI handler map).
	chroot /host systemctl restart crio
	# Sleep to prevent pod restart while staged RPM removal awaits next reboot.
	sleep infinity
}

get_cloud_provider() {
	echo "${CLOUD_PROVIDER:-}"
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

		kata_version=$(chroot /host rpm -q kata-containers 2>/dev/null || echo "unknown")
		echo "Installation complete on node ${NODE_NAME} (kata-containers: ${kata_version})"
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
