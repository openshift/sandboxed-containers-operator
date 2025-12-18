#!/bin/bash
# Contains common functions used by the scripts

function extract_container_image() {
	# Set the values required for the container image extraction.
	local image="${1}"

	local copy_src="${2}"
	local copy_des="${3}"

	local auth_json_file="${4}"

	local dest_image="/tmp/image"
	local tag="tag"
	local destination_path="/tmp/unpacked"

	# If arguments are not provided, exit the script with an error message
	[[ $# -lt 3 ]] &&
		error_exit "Usage: extract_container_image <image> <dest_image> <destination_path> <copy_src> <copy_des> [registry_secret]"

	# Form the skopeo CLI. Add authfile if provided
	# copy signatures for OCI images is not supported, hence the signatures if any are removed while copying
	if [[ -n "${4}" ]]; then
		SKOPEO_CLI="skopeo copy --remove-signatures --authfile ${auth_json_file}"
	else
		SKOPEO_CLI="skopeo copy --remove-signatures"
	fi

	# Download the container image
	$SKOPEO_CLI "docker://${image}" "oci:${dest_image}:${tag}" ||
		error_exit "Failed to download the container image"

	# Extract the container image using umoci into provided directory
	umoci unpack --rootless --image "${dest_image}:${tag}" "${destination_path}" ||
		error_exit "Failed to extract the container image"

	# Copy requested files or directories
	for src in $copy_src; do
		full_src="${destination_path}/rootfs${src}"
		if [[ -e "$full_src" ]]; then
			cp -r "$full_src" "$copy_des/"
		else
			echo "Warning: Not found in image: $src"
			sleep infinity
		fi
	done

	# Clean up
	rm -rf "${image}" "${destination_path}"
}

client_tools() {
	mkdir -p "/usr/bin"
	local upper_node_name="${NODE_NAME^^}"
	upper_node_name="${upper_node_name//-/_}"
	local envarName="CLI_IMAGE_$upper_node_name"
	extract_container_image "$(printenv -- "$envarName")" "/usr/bin/oc /usr/bin/kubectl" "/usr/bin" "/tmp/regauth/auth.json"
	chmod +x /usr/bin/oc
	chmod +x /usr/bin/kubectl
}

error() {
	local msg="$*"
	printf 'ERROR: %s\n' "$msg" >&2
	return 1
}
