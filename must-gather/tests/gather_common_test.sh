#!/bin/bash

# Tests for must-gather/collection-scripts/gather_common and the scripts
# that use it (gather_hcp, gather_sandboxed_containers_logs).
#
# Covers the KATA-6493 regressions:
#   - exec_with_retry must be able to run shell functions (run_oc) under
#     timeout(1); before the fix, every run_oc-based collection failed with
#     "timeout: failed to run command 'run_oc'".
#   - detect_topology must not report an HCP topology when the query fails;
#     the old IS_HCP=true default caused MachineConfigPool collection to be
#     skipped on standalone clusters.
#
# Run with: bash tests/gather_common_test.sh   (or: make test)
#
# Note: `set -e` is intentionally not used here — the code under test relies
# on arithmetic commands whose non-zero status must not abort the test run.
# Failures are counted explicitly instead.

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
temp_dir=$(mktemp -d)
trap 'rm -rf "$temp_dir"' EXIT

export BASE_COLLECTION_PATH="$temp_dir/must-gather"
mkdir -p "$BASE_COLLECTION_PATH"

# Fast retry knobs (see gather_common).
export EXEC_TIMEOUT_SECS=1
export EXEC_MAX_RETRIES=3
export EXEC_RETRY_DELAY=0

# Fake oc controls.
export OC_COUNT_FILE="$temp_dir/oc-invocations"
export OC_TOPOLOGY="External"
export OC_MCP_LIST="master worker"
unset OC_TOPOLOGY_EXIT OC_EXIT OC_FAIL_FIRST

mkdir -p "$temp_dir/bin"
cat > "$temp_dir/bin/oc" <<'EOF'
#!/bin/bash
# Fake oc, driven by environment variables:
#   OC_COUNT_FILE    a line is appended on every invocation
#   OC_TOPOLOGY      value returned for the controlPlaneTopology query
#   OC_TOPOLOGY_EXIT when non-zero, the topology query fails with this code
#   OC_EXIT          when non-zero, every invocation fails with this code
#   OC_FAIL_FIRST    the first N invocations fail, later ones succeed
#   OC_MCP_LIST      MachineConfigPools that "exist"
[[ -n ${OC_COUNT_FILE:-} ]] && echo x >> "$OC_COUNT_FILE"

if [[ ${OC_FAIL_FIRST:-0} != 0 ]]; then
    count=$(grep -c x "${OC_COUNT_FILE:-/dev/null}" 2>/dev/null)
    if (( count <= OC_FAIL_FIRST )); then
        exit 1
    fi
fi
if [[ ${OC_EXIT:-0} != 0 ]]; then
    echo "fake oc error" >&2
    exit "$OC_EXIT"
fi

case "$*" in
    "get infrastructure cluster -o jsonpath={.status.controlPlaneTopology}")
        if [[ ${OC_TOPOLOGY_EXIT:-0} != 0 ]]; then
            echo "the infrastructure query failed" >&2
            exit "$OC_TOPOLOGY_EXIT"
        fi
        echo "$OC_TOPOLOGY"
        ;;
    "get mcp "*)
        name=$3
        if [[ " ${OC_MCP_LIST:-} " == *" $name "* ]]; then
            echo "Name:   $name"
        else
            echo "machineconfigpools.machineconfiguration.openshift.io \"$name\" not found" >&2
            exit 1
        fi
        ;;
    "describe mcp "*)
        echo "description of MachineConfigPool $3"
        ;;
    *)
        echo "fake-oc: $*"
        ;;
esac
EOF
chmod +x "$temp_dir/bin/oc"
export PATH="$temp_dir/bin:$PATH"

source "$script_dir/collection-scripts/gather_common"

failures=0
section() { echo; echo "== $*"; }
ok()  { echo "  ok   - $1"; }
bad() { echo "  FAIL - $1"; failures=$((failures + 1)); }
assert_eq() {  # description expected actual
    if [[ "$2" == "$3" ]]; then ok "$1"; else bad "$1 (expected '$2', got '$3')"; fi
}
assert_contains() {  # description haystack needle
    if [[ "$2" == *"$3"* ]]; then ok "$1"; else bad "$1 ('$3' not found)"; fi
}
assert_file_exists() {
    if [[ -e "$2" ]]; then ok "$1"; else bad "$1 ($2 does not exist)"; fi
}
assert_file_absent() {
    if [[ ! -e "$2" ]]; then ok "$1"; else bad "$1 ($2 exists)"; fi
}
oc_invocations() {
    local n
    n=$(grep -c x "$OC_COUNT_FILE" 2>/dev/null)
    echo "${n:-0}"
}

# ---------------------------------------------------------------------------
# exec_with_retry runs the run_oc shell function (KATA-6493 core regression)
# ---------------------------------------------------------------------------
section "exec_with_retry runs the run_oc shell function"

out=$(exec_with_retry run_oc get infrastructure cluster -o jsonpath='{.status.controlPlaneTopology}')
assert_eq "run_oc jsonpath query returns topology" "External" "$out"

out=$(exec_with_retry run_oc get nodes)
assert_eq "run_oc generic query" "fake-oc: get nodes" "$out"

# run_oc logging must go to stderr so collected files stay clean
stderr_capture=$(mktemp)
out=$(exec_with_retry run_oc get nodes 2>"$stderr_capture")
assert_eq "run_oc stdout has no timestamp" "fake-oc: get nodes" "$out"
if grep -q "oc get nodes" "$stderr_capture"; then
    ok "run_oc logs the command to stderr"
else
    bad "run_oc logs the command to stderr"
fi
rm -f "$stderr_capture"

# ---------------------------------------------------------------------------
# exec_with_retry still runs plain executables (no behavior change)
# ---------------------------------------------------------------------------
section "exec_with_retry still runs plain executables"

out=$(exec_with_retry oc get nodes)
assert_eq "oc as command" "fake-oc: get nodes" "$out"

out=$(exec_with_retry /bin/echo hello)
assert_eq "non-oc executable" "hello" "$out"

# ---------------------------------------------------------------------------
# retry behaviour
# ---------------------------------------------------------------------------
section "exec_with_retry retries failures"

: > "$OC_COUNT_FILE"
export OC_FAIL_FIRST=2
out=$(exec_with_retry run_oc get pods)
assert_eq "succeeds after transient failures" "fake-oc: get pods" "$out"
assert_eq "three attempts were made" "3" "$(oc_invocations)"
unset OC_FAIL_FIRST

export OC_EXIT=42
rc=0
exec_with_retry run_oc get pods >/dev/null 2>&1 || rc=$?
assert_eq "persistent failure exit code is propagated" "42" "$rc"
unset OC_EXIT

# ---------------------------------------------------------------------------
# timeouts apply to shell functions too
# ---------------------------------------------------------------------------
section "exec_with_retry times out shell functions"

slow_function() { sleep 30; }
start=$SECONDS
rc=0
exec_with_retry slow_function >/dev/null 2>&1 || rc=$?
elapsed=$((SECONDS - start))
assert_eq "timed-out function returns 124" "124" "$rc"
if (( elapsed >= 2 && elapsed < 15 )); then
    ok "each attempt was killed at the timeout (took ${elapsed}s)"
else
    bad "each attempt was killed at the timeout (took ${elapsed}s)"
fi

# ---------------------------------------------------------------------------
# collect_resource error handling
# ---------------------------------------------------------------------------
section "collect_resource logs errors"

export OC_EXIT=42
rc=0
collect_resource "test context" "${BASE_COLLECTION_PATH}/should-not-exist.yaml" run_oc get pods >/dev/null 2>&1 || rc=$?
assert_eq "collect_resource propagates failure" "42" "$rc"
assert_file_absent "no output file on failure" "${BASE_COLLECTION_PATH}/should-not-exist.yaml"
assert_contains "error log records context" "$(cat "${BASE_COLLECTION_PATH}/must-gather-errors.log")" "test context"
assert_contains "error log records exit code" "$(cat "${BASE_COLLECTION_PATH}/must-gather-errors.log")" "Exit Code: 42"
unset OC_EXIT

collect_resource "test context ok" "${BASE_COLLECTION_PATH}/out.yaml" run_oc get nodes >/dev/null 2>&1
assert_file_exists "collect_resource writes output file on success" "${BASE_COLLECTION_PATH}/out.yaml"
assert_contains "output file has the command output" "$(cat "${BASE_COLLECTION_PATH}/out.yaml")" "fake-oc: get nodes"

# ---------------------------------------------------------------------------
# detect_topology
# ---------------------------------------------------------------------------
section "detect_topology"

unset CONTROL_PLANE_TOPOLOGY IS_HCP
export OC_TOPOLOGY=External
detect_topology
assert_eq "External topology means HCP" "true" "${IS_HCP:-}"
assert_eq "controlPlaneTopology recorded" "External" "${CONTROL_PLANE_TOPOLOGY:-}"

invocations_before=$(oc_invocations)
detect_topology
assert_eq "detection result is cached" "$invocations_before" "$(oc_invocations)"

export OC_TOPOLOGY=HighlyAvailable
unset CONTROL_PLANE_TOPOLOGY IS_HCP
detect_topology
assert_eq "standalone topology means not HCP" "false" "${IS_HCP:-}"

export OC_TOPOLOGY_EXIT=1
unset CONTROL_PLANE_TOPOLOGY IS_HCP
detect_topology
assert_eq "failed detection leaves topology unknown, not HCP" "" "${IS_HCP:-}"
assert_eq "failed detection leaves controlPlaneTopology empty" "" "${CONTROL_PLANE_TOPOLOGY:-}"
assert_contains "failed detection is logged" "$(cat "${BASE_COLLECTION_PATH}/must-gather-errors.log")" "Detecting cluster topology"
assert_contains "failed detection logs the real exit code" "$(cat "${BASE_COLLECTION_PATH}/must-gather-errors.log")" "Exit Code: 1"
unset OC_TOPOLOGY_EXIT
export OC_TOPOLOGY=External

# ---------------------------------------------------------------------------
# gather_hcp honors the detected topology
# ---------------------------------------------------------------------------
section "gather_hcp honors topology"

# HCP cluster: HCP data collected
case_base="$temp_dir/hcp-external"
mkdir -p "$case_base"
BASE_COLLECTION_PATH="$case_base" bash "$script_dir/collection-scripts/gather_hcp" >"$temp_dir/hcp-external.log" 2>&1
assert_eq "HCP cluster: exit status" "0" "$?"
assert_file_exists "HCP cluster: hcp/topology" "$case_base/hcp/topology"
assert_contains "HCP cluster: topology value" "$(cat "$case_base/hcp/topology")" "External"
assert_file_exists "HCP cluster: infrastructure.yaml" "$case_base/hcp/infrastructure.yaml"

# Standalone cluster: HCP section skipped
case_base="$temp_dir/hcp-standalone"
mkdir -p "$case_base"
BASE_COLLECTION_PATH="$case_base" OC_TOPOLOGY=HighlyAvailable bash "$script_dir/collection-scripts/gather_hcp" >"$temp_dir/hcp-standalone.log" 2>&1
assert_eq "standalone cluster: exit status" "0" "$?"
assert_file_absent "standalone cluster: no hcp/ directory" "$case_base/hcp"
assert_contains "standalone cluster: skip is reported" "$(cat "$temp_dir/hcp-standalone.log")" "skipping HCP-specific collection"

# Unknown topology: HCP data still collected (KATA-6493)
case_base="$temp_dir/hcp-unknown"
mkdir -p "$case_base"
BASE_COLLECTION_PATH="$case_base" OC_TOPOLOGY_EXIT=1 bash "$script_dir/collection-scripts/gather_hcp" >"$temp_dir/hcp-unknown.log" 2>&1
assert_eq "unknown topology: exit status" "0" "$?"
assert_file_exists "unknown topology: hcp/topology" "$case_base/hcp/topology"
assert_contains "unknown topology: topology notes the failure" "$(cat "$case_base/hcp/topology")" "unknown"
assert_file_exists "unknown topology: infrastructure.yaml" "$case_base/hcp/infrastructure.yaml"

# ---------------------------------------------------------------------------
# gather_sandboxed_containers_logs MachineConfigPool gating (KATA-6493)
# ---------------------------------------------------------------------------
section "gather_sandboxed_containers_logs MachineConfigPool gating"

osc_case() {  # name [VAR=value...] — runs the script, sets OSC_CASE_RC/_BASE
    local name="$1"; shift
    local base="$temp_dir/osc-$name"
    mkdir -p "$base"
    env "$@" BASE_COLLECTION_PATH="$base" bash "$script_dir/collection-scripts/gather_sandboxed_containers_logs" >"$temp_dir/osc-$name.log" 2>&1
    OSC_CASE_RC=$?
    OSC_CASE_BASE="$base"
}

# Standalone cluster: MCPs collected
osc_case standalone OC_TOPOLOGY=HighlyAvailable
assert_eq "standalone: exit status" "0" "$OSC_CASE_RC"
assert_file_exists "standalone: master MCP description" "$OSC_CASE_BASE/sandboxed-containers/mcps/master_description"
assert_file_exists "standalone: worker MCP description" "$OSC_CASE_BASE/sandboxed-containers/mcps/worker_description"
assert_file_absent "standalone: missing MCP not collected" "$OSC_CASE_BASE/sandboxed-containers/mcps/kata-oc_description"
assert_file_exists "standalone: peer-pods ConfigMap collected" "$OSC_CASE_BASE/sandboxed-containers/peer-pods/peer-pods-cm.yaml"
assert_file_exists "standalone: OLM CatalogSources collected" "$OSC_CASE_BASE/sandboxed-containers/olm/catalogsources.yaml"

# HCP cluster: MCPs skipped
osc_case hcp OC_TOPOLOGY=External
assert_eq "HCP: exit status" "0" "$OSC_CASE_RC"
assert_file_absent "HCP: no mcps directory" "$OSC_CASE_BASE/sandboxed-containers/mcps"
assert_file_exists "HCP: peer-pods ConfigMap collected" "$OSC_CASE_BASE/sandboxed-containers/peer-pods/peer-pods-cm.yaml"

# Unknown topology: MCPs attempted (KATA-6493: used to be skipped)
osc_case unknown OC_TOPOLOGY_EXIT=1
assert_eq "unknown: exit status" "0" "$OSC_CASE_RC"
assert_file_exists "unknown: master MCP description" "$OSC_CASE_BASE/sandboxed-containers/mcps/master_description"
assert_file_exists "unknown: peer-pods ConfigMap collected" "$OSC_CASE_BASE/sandboxed-containers/peer-pods/peer-pods-cm.yaml"
assert_file_exists "unknown: OLM CatalogSources collected" "$OSC_CASE_BASE/sandboxed-containers/olm/catalogsources.yaml"
assert_contains "unknown: topology failure logged" "$(cat "$OSC_CASE_BASE/must-gather-errors.log")" "Detecting cluster topology"

# ---------------------------------------------------------------------------

echo
if (( failures == 0 )); then
    echo "gather_common tests passed"
    exit 0
fi
echo "gather_common tests FAILED ($failures failure(s))"
exit 1
