#!/bin/bash

# include common functions from lib.sh
# shellcheck source=/dev/null
source "$(dirname "$0")"/lib.sh

set -xeuo pipefail

# Format: "absolute_source_path:absolute_dest_path:octal_mode"
FILES=(
	"/files/50-kata-remote:/host/etc/crio/crio.conf.d/50-kata-remote:0644"
	"/files/configuration-remote.toml:/host/opt/kata/configuration-remote.toml:0420"
)

copy_file() {
	local src="$1" dest="$2" perm="$3"
	if [[ -f "$src" ]]; then
		# GNU coreutils install: create parents (-D) and set mode (-m)
		install -D -m "$perm" "$src" "$dest"
		echo "Installed $(basename "$src") -> $dest (mode $perm)"
	else
		echo "Warning: $(basename "$src") not found in configmap"
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

# check crio's runtime list using crictl
# check whether it contains kata-remote or not
is_kata_remote_loaded() {
	local kata_remote_exists
	kata_remote_exists=$(chroot /host /bin/bash -c "crictl info | jq 'any(.runtimeHandlers[]; .name == \"kata-remote\")'")

	if [[ "$kata_remote_exists" == "true" ]]; then
		return 0
	fi

	return 1
}

# return false (1) when kata-remote is loaded by crio
# in that case there is no need to reload crio
is_reload_required_after_copy() {
	if is_kata_remote_loaded; then
		return 1
	fi

	return 0
}

# return false (1) when kata-remote is not loaded by crio
# in that case there is no need to reload crio
is_reload_required_after_remove() {
	if is_kata_remote_loaded; then
		return 0
	fi

	return 1
}

reload_crio() {
	chroot /host /bin/bash -c "systemctl reload crio"
}

# check whether crio requires reload after copying the config files and reload it
# wait for a little and check again in case crio doesn't reloaded for some reason
wait_for_reload_clear_copy() {
	while is_reload_required_after_copy; do
		echo "CRI-O needs to be reloaded because the Kata configuration hasn't been applied yet."
		reload_crio
		sleep 60
	done
}

label_node() {
	local state="$1"
	kubectl label node "${NODE_NAME}" "kataconfiguration.openshift.io/osc-config-sync=${state}" --overwrite
}

# check whether crio requires reload after removing the config files and reload it
# wait for a little and check again in case crio doesn't reloaded for some reason
wait_for_reload_clear_remove() {
	while is_reload_required_after_remove; do
		echo "CRI-O needs to be reloaded because the removal of the Kata configuration hasn't been applied yet."
		reload_crio
		sleep 60
	done
}

copy_files() {
	echo "Starting configuration copy..."
	for entry in "${FILES[@]}"; do
		IFS=: read -r src dest perm <<<"$entry"
		copy_file "$src" "$dest" "$perm"
	done
	reload_crio
	echo "Configuration copy completed at $(date)"

	wait_for_reload_clear_copy
}

remove_files() {
	echo "Starting configuration removal..."

	label_node "removing"

	for entry in "${FILES[@]}"; do
		IFS=: read -r _src dest _perm <<<"$entry"
		remove_file "$dest"
	done
	reload_crio
	echo "Configuration removal completed at $(date)"

	wait_for_reload_clear_remove

	label_node "removed"
}

main() {
	[[ -z "${NODE_NAME:-}" ]] && {
		echo "ERROR: NODE_NAME env var must be set."
		exit 1
	}

	[[ -z "${CLI_IMAGE:-}" ]] && {
		echo "ERROR: CLI_IMAGE env var must be set."
		exit 1
	}

	local action="${1:-}"
	case "$action" in
	install)
		client_tools
		copy_files
		;;
	uninstall)
		client_tools
		remove_files
		;;
	*)
		echo "Usage: $0 {install|uninstall}" >&2
		exit 1
		;;
	esac

	# Keep container alive
	sleep infinity
}

main "$@"
