#!/bin/bash

set -xeuo pipefail

LOG_LEVELS=("fatal" "panic" "error" "warn" "info" "debug" "trace")
LOG_FILE_PATH=/host/etc/crio/crio.conf.d/01-ctrcfg-logLevel

# save a crio config file that contains the log level configuration
create_or_override_log_config() {
	local level="$1"
	local file_content

	file_content=$(
		cat <<EOF
[crio.runtime]
log_level = "$level"
EOF
	)

	# Ensure the destination directory exists
	local dest_dir
	dest_dir="$(dirname "$LOG_FILE_PATH")"
	if [[ ! -d "$dest_dir" ]]; then
		mkdir -p "$dest_dir"
	fi

	# Write content to the file
	echo "$file_content" >"$LOG_FILE_PATH"
	# Set permissions
	chmod 644 "$LOG_FILE_PATH"
}

remove_log_config() {
	if [[ -f "$LOG_FILE_PATH" ]]; then
		rm -f "$LOG_FILE_PATH"
	fi
}

reload_crio() {
	chroot /host /bin/bash -c "systemctl reload crio"
}

set_loglevel() {
	local level="$1"
	local valid=false

	for valid_level in "${LOG_LEVELS[@]}"; do
		if [[ "$level" == "$valid_level" ]]; then
			valid=true
			break
		fi
	done

	if ! $valid; then
		echo "Usage: $0 install {${LOG_LEVELS[*]}}" >&2
		exit 1
	fi

	create_or_override_log_config "$level"
	reload_crio
}

unset_loglevel() {
	remove_log_config
	reload_crio
}

main() {
	local action="${1:-}"
	local level="${2:-}"

	case "$action" in
	install)
		set_loglevel "$level"
		;;
	uninstall)
		unset_loglevel
		;;
	*)
		echo "Usage: $0 {install|uninstall} [log_level]" >&2
		exit 1
		;;
	esac

	sleep infinity
}

main "$@"
