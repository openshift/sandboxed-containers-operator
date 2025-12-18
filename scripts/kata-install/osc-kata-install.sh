#!/bin/bash

# TODO: Use Envars for path and code dependant variables in the script

# include common functions from lib.sh
# shellcheck source=/dev/null
source "$(dirname "$0")"/lib.sh

set -xeuo pipefail

PACKAGES="capstone daxctl-libs edk2-ovmf ipxe-roms-qemu kata-containers libfdt libpmem libpng librdmacm ndctl-libs pixman qemu-img qemu-kvm-common qemu-kvm-core seabios-bin seavgabios-bin virtiofsd"

# find_affected_local_paths pkg1 pkg2 ...
#
# Runs on the host (via exec_on_host) and prints /etc paths (one per line) that are:
#   - "Added" in ostree admin config-diff (local-only, not in /usr/etc)
#   - under directories that:
#       * contain files from the given packages, AND
#       * contain no files owned by any other package
#     → those directories may disappear when these packages are removed.
find_affected_local_paths() {
	(($# > 0)) || {
		echo "find_affected_local_paths: need at least one package" >&2
		return 1
	}

	# All packages as a single space-separated string for exec_on_host
	local pkgs="$*"

	#
	# 1) Local-only /etc files (Added vs base) on host
	#
	local added_paths=()
	mapfile -t added_paths < <(
		exec_on_host "
      ostree admin config-diff \
        | awk '\$1==\"A\" && \$2!=\"\" {print \"/etc/\"\$2}' \
        | sort -u
    "
	)
	((${#added_paths[@]} > 0)) || return 0

	#
	# 2) /etc files from packages being removed (on host)
	#
	local pkg_paths=()
	mapfile -t pkg_paths < <(
		exec_on_host "
      rpm -ql $pkgs 2>/dev/null \
        | grep '^/etc/' \
        | sort -u
    "
	)
	((${#pkg_paths[@]} > 0)) || return 0

	#
	# 3) Directories that have both local-only files and removal-package files
	#
	local added_dirs=() pkg_dirs=()
	mapfile -t added_dirs < <(printf '%s\n' "${added_paths[@]}" | xargs -r dirname | sort -u)
	mapfile -t pkg_dirs < <(printf '%s\n' "${pkg_paths[@]}" | xargs -r dirname | sort -u)

	local dirs_both=()
	mapfile -t dirs_both < <(
		comm -12 \
			<(printf '%s\n' "${added_dirs[@]}") \
			<(printf '%s\n' "${pkg_dirs[@]}") |
			grep -vE '^/etc$'
	)
	((${#dirs_both[@]} > 0)) || return 0

	#
	# 4) Full NEVR names for packages being removed (on host)
	#
	local removing_pkgs=()
	mapfile -t removing_pkgs < <(
		exec_on_host "rpm -q $pkgs 2>/dev/null | sort -u"
	)
	((${#removing_pkgs[@]} > 0)) || return 0

	#
	# 5) Filter dirs_both → risky_dirs:
	#    A dir is risky if:
	#      - it has no owned files at all (only unowned stuff), OR
	#      - all owned files in its subtree are from packages being removed.
	#
	local risky_dirs=()
	local dir

	for dir in "${dirs_both[@]}"; do
		local is_risky=true

		# Collect owners of:
		#   - the directory itself
		#   - all files under it recursively
		local owners=()
		local tmp=()

		# Owner(s) of the directory itself (if any)
		mapfile -t tmp < <(
			exec_on_host "rpm -qf '$dir' 2>/dev/null || true"
		)
		owners+=("${tmp[@]}")

		# Owner(s) of all regular files under the directory (recursive)
		mapfile -t tmp < <(
			exec_on_host "
        find '$dir' -type f -print0 2>/dev/null \
          | xargs -0 -r rpm -qf 2>/dev/null || true
      "
		)
		owners+=("${tmp[@]}")

		# Deduplicate owners and drop empties
		if ((${#owners[@]} > 0)); then
			mapfile -t owners < <(
				printf '%s\n' "${owners[@]}" |
					grep -v '^$' |
					sort -u
			)
		fi

		# If there are no owners at all, no package keeps this dir, the directory is risky
		if ((${#owners[@]} == 0)); then
			risky_dirs+=("$dir")
			continue
		fi

		# If ANY owner is not in removing_pkgs, the directory is safe
		local owner rem
		for owner in "${owners[@]}"; do
			local owner_in_removal=false
			for rem in "${removing_pkgs[@]}"; do
				if [[ "$owner" == "$rem" ]]; then
					owner_in_removal=true
					break
				fi
			done

			if [[ "$owner_in_removal" == false ]]; then
				# some surviving package owns something under this dir, the directory is safe
				is_risky=false
				break
			fi
		done

		[[ "$is_risky" == true ]] && risky_dirs+=("$dir")
	done

	((${#risky_dirs[@]} > 0)) || return 0

	#
	# 6) Local-only files that live under risky directories
	#
	local affected=() path
	for path in "${added_paths[@]}"; do
		for dir in "${risky_dirs[@]}"; do
			[[ "$path" == "$dir"/* ]] && affected+=("$path")
		done
	done

	printf '%s\n' "${affected[@]}" | sort -u
}

# restore_backup_and_cleanup BACKUP_ROOT
# Restores and deletes backup on the HOST.
restore_backup_and_cleanup() {
	local backup_root="${1:-}"
	[[ -n "$backup_root" ]] || {
		echo "restore_backup_and_cleanup: backup_root is required" >&2
		return 1
	}

	exec_on_host "if [ -d '$backup_root' ]; then cp -a '$backup_root'/\. /; rm -rf '$backup_root'; fi"
}

# backup_paths BACKUP_ROOT PATH...
#
# Copies all existing PATHs into BACKUP_ROOT, preserving structure.
#
# Example:
#   mapfile -t affected < <(find_affected_local_paths cri-o kata-containers)
#   backup_paths /var/backups/foo "${affected[@]}"
# backup_paths BACKUP_ROOT PATH...
# Runs all filesystem operations on the HOST.
backup_paths() {
	local backup_root="${1:-}"
	shift || true

	[[ -n "$backup_root" ]] || {
		echo "backup_paths: backup_root is required" >&2
		return 1
	}

	exec_on_host "mkdir -p '$backup_root'"

	local p dir
	for p in "$@"; do
		[[ -z "$p" ]] && continue
		# Compute dirname in the container (same string on host)
		dir=$(dirname "$p")
		exec_on_host "if [ -e '$p' ]; then mkdir -p '$backup_root$dir'; cp -a '$p' '$backup_root$p'; fi"
	done
}

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

set_status_waiting_to_install() {
	label_node "waiting_to_install"
}

set_status_waiting_to_uninstall() {
	label_node "waiting_to_uninstall"
}

set_status_installed() {
	label_node "installed"
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

reload_crio() {
	exec_on_host "systemctl reload crio"

	wait_for_kubelet_cri_recovery
}

# check crio's runtime list using crictl
# check whether it contains kata-remote or not
is_kata_loaded() {
	# TODO: Check for normal kata shim
	local kata_remote_exists
	kata_remote_exists=$(chroot /host /bin/bash -c "crictl info | jq 'any(.runtimeHandlers[]; .name == \"kata-remote\")'")

	if [[ "$kata_remote_exists" == "true" ]]; then
		return 0
	fi

	return 1
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

	# If nothing to install, exit early
	if [[ ${#install_rpms[@]} -eq 0 ]]; then
		#if ! is_kata_loaded; then
		#	restart_crio
		#fi

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
	exec_on_host "semodule -i /usr/share/kata-containers/defaults/osc_monitor.cil"

	# Debug
	#sleep infinity

	sleep 5

	# Reload crio
	#reload_crio
	restart_crio

	set_status_installed
}

uninstall_kata() {
	# Check if kata-containers is installed
	# If kata-containers is not installed we are done
	# Sleep infinity to prevent pod restart (DaemonSets always restart exited pods)
	installed_version=$(chroot /host rpm -q kata-containers 2>/dev/null || true)
	if [[ "$installed_version" == "package kata-containers is not installed" ]]; then
		#if is_kata_loaded; then
		#	reload_crio
		#fi

		echo "Package already uninstalled"
		set_status_uninstalled
		return 0
	fi

	# Set installation status to waiting and wait for the operator's signal
	set_status_waiting_to_uninstall
	waiting_for_uninstall_schedule

	# Backup files that are not owned by any packages and would be removed by apply-live
	affected=()
	mapfile -t affected < <(find_affected_local_paths $PACKAGES)

	if ((${#affected[@]} > 0)); then
		backup_root="/var/backups/rpm-ostree-etc-$(date +%s)"
		backup_paths "$backup_root" "${affected[@]}"
	fi

	# Uninstall extensions from the node
	chroot /host /bin/bash -c "rpm-ostree uninstall $PACKAGES"
	chroot /host bash -c "rpm-ostree apply-live --allow-replacement"

	# Restore files that were backed up
	if ((${#affected[@]} > 0)); then
		restore_backup_and_cleanup "$backup_root"
	fi

	# Remove Config
	remove_kata_remote_config_files

	# Remove SELinux policy
	exec_on_host "semodule -r osc_monitor"

	# Debug
	#sleep infinity

	# Let crio and other services realize that the filesystem changed
	sleep 5

	# Reload crio
	reload_crio

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
