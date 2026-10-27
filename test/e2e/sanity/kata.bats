#!/usr/bin/env bats
#
# Sanity check for the kata containers workload.

load "${BATS_TEST_DIRNAME}/lib.sh"

setup() {
    sanity_setup
}

@test "kata: pod runs and is deleted" {
    oc apply -f "$POD_YAML"
    oc wait --for=condition=Ready "pod/${POD_NAME}" \
        -n "$SANITY_NAMESPACE" --timeout="${SANITY_TIMEOUT}s"

    oc delete pod "$POD_NAME" -n "$SANITY_NAMESPACE" \
        --wait --timeout="${SANITY_TIMEOUT}s"

    run oc get pod "$POD_NAME" -n "$SANITY_NAMESPACE" --ignore-not-found
    [ -z "$output" ]
}

teardown() {
    if [[ -z "${BATS_TEST_COMPLETED:-}" ]]; then
        collect_failure_logs
    fi

    oc delete pod "$POD_NAME" -n "$SANITY_NAMESPACE" \
        --ignore-not-found --wait=false
}
