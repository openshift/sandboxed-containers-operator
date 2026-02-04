#!/bin/bash
# This script will enable subscription and edit the RPM fetcher input to inclue
# the subscription's keys to the SSL parameters.
# Then it will run the following commands:
# $ hermeto fetch-deps --source . --output hermeto-output ${INPUT}
# $ hermeto generate-env ./hermeto-output -o ./hermeto-output/hermeto.env --for-output-dir /hermeto-output
# $ hermeto inject-files ./hermeto-output --for-output-dir /hermeto-output
#
# For this reason, this script is supposed to be called with only one argument:
# the fetcher input JSON
#
# The code comes from the konflux prefetch-dependencies task:
# https://github.com/konflux-ci/build-definitions/blob/main/task/prefetch-dependencies/0.2/prefetch-dependencies.yaml
# use it for reference when troubleshooting.

INPUT="$1"

function is_json {
    jq . 2>/dev/null 1>&2 <<< "$1"
}

# The input JSON can be in one of these forms:
# 1) '[{"type": "gomod"}, {"type": "bundler"}]'
# 2) '{"packages": [{"type": "gomod"}, {"type": "bundler"}]}'
# 3) '{"type": "gomod"}'
function input_json_has_rpm {
    jq '
        if (type == "array" or type == "object") | not then
        false
        elif type == "array" then
        any(.[]; .type == "rpm")
        elif has("packages") | not then
        .type == "rpm"
        elif (.packages | type == "array") then
        any(.packages[]; .type == "rpm")
        else
        false
        end' <<< "$1"
}

function inject_ssl_opts {
    input="$1"
    ssl_options="$2"

    # Check if input is plain string or JSON and if the request specifies RPMs
    if [ "$input" == "rpm" ]; then
        input="$(jq -n --argjson ssl "$ssl_options" '
                {
                    type: "rpm",
                    options: {
                    ssl: $ssl
                    }
                }'
                )"
    elif is_json "$input" && [[ $(input_json_has_rpm "$input") == true ]]; then
        # The output JSON may need the SSL options updated for the RPM backend
        input="$(jq \
                --argjson ssl "$ssl_options" '
                    if type == "array" then
                    map(if .type == "rpm" then .options.ssl += $ssl else . end)
                    elif has("packages") then
                    .packages |= map(if .type == "rpm" then .options.ssl += $ssl else . end)
                    else
                    .options.ssl += $ssl
                    end' \
                    <<< "$input"
                )"
    fi
    echo "$input"
}


# run the subscription-manager registration
if [ -e /activation-key/org ]; then
    RHSM_ORG=$(cat /activation-key/org)
    RHSM_ACT_KEY=$(cat /activation-key/activation-key)

    echo "Registering with Red Hat subscription manager."
    subscription-manager register --force --org "${RHSM_ORG}" --activationkey "${RHSM_ACT_KEY}"

    trap 'subscription-manager unregister || true' EXIT

    entitlement_files="$(ls -1 /etc/pki/entitlement/*.pem)"
    ENTITLEMENT_CERT_KEY_PATH="$(grep -e '-key.pem$' <<< "$entitlement_files")"
    ENTITLEMENT_CERT_PATH="$(grep -v -e '-key.pem$' <<< "$entitlement_files")"
    CA_BUNDLE_PATH="/etc/rhsm/ca/redhat-uep.pem"

    PREFETCH_SSL_OPTS="$(jq -n \
                            --arg key "$ENTITLEMENT_CERT_KEY_PATH" \
                            --arg cert "$ENTITLEMENT_CERT_PATH" \
                            --arg ca_bundle "$CA_BUNDLE_PATH" \
                            '{client_key: $key, client_cert: $cert, ca_bundle: $ca_bundle}'
                        )"

    # We need to modify the CLI params in place if we're processing RPMs
    INPUT=$(inject_ssl_opts "$INPUT" "$PREFETCH_SSL_OPTS")
fi

hermeto fetch-deps --source . --output ./cachi2/output "${INPUT}"

hermeto generate-env ./cachi2/output --for-output-dir=/cachi2/output --output ./cachi2/cachi2.env

hermeto inject-files ./cachi2/output --for-output-dir=/cachi2/output

# NOTE: Compatibility hack, hermeto will create a hermeto.repo when processing RPMs (1 for
# each architecture) which may break users expecting cachi2.repo
find "./cachi2/output" \
    -type f \
    -name hermeto.repo \
    -execdir mv {} cachi2.repo \;
