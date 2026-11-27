#!/bin/bash

# Tests for collect_vc_runtime_data in must-gather/collection-scripts/
# gather_common: the /run/vc runtime state must only be published under the
# collection tree when the copy succeeded and the tokens were redacted
# (KATA-6494). A failed copy publishes nothing, not even redacted partial
# data; a failed redaction or publish keeps the data out as well.
#
# Run with: bash tests/collect_vc_runtime_data_test.sh   (or: make test)

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
temp_dir=$(mktemp -d)
trap 'chmod -R u+rX "$temp_dir" 2>/dev/null; rm -rf "$temp_dir"' EXIT

export BASE_COLLECTION_PATH="$temp_dir/must-gather"
mkdir -p "$BASE_COLLECTION_PATH"

# Fast retries (see gather_common).
export EXEC_RETRY_DELAY=0

mkdir -p "$temp_dir/bin"
cat > "$temp_dir/bin/oc" <<'EOF'
#!/bin/bash
# Fake oc, driven by environment variables:
#   OC_CP_FAIL=1  the copy writes partial data and then fails
#   OC_CP_LOCK=1  the copy also makes persist.json unreadable
case "$1" in
    cp)
        dest=$3
        mkdir -p "$dest/sbs/9c1f5b2e/some-container"
        printf '{"Process":{"Token":"staged-token-value","Pid":-1}}\n' \
            > "$dest/sbs/9c1f5b2e/some-container/persist.json"
        if [[ ${OC_CP_LOCK:-0} == 1 ]]; then
            chmod 000 "$dest/sbs/9c1f5b2e/some-container/persist.json"
        fi
        if [[ ${OC_CP_FAIL:-0} == 1 ]]; then
            echo "fake oc cp failure" >&2
            exit 1
        fi
        exit 0
        ;;
    *)
        echo "fake-oc: unexpected command: $*" >&2
        exit 1
        ;;
esac
EOF
chmod +x "$temp_dir/bin/oc"
export PATH="$temp_dir/bin:$PATH"

source "$script_dir/collection-scripts/gather_common"

failures=0
ok()  { echo "  ok   - $1"; }
bad() { echo "  FAIL - $1"; failures=$((failures + 1)); }
assert_contains() {  # description haystack needle
    if [[ "$2" == *"$3"* ]]; then ok "$1"; else bad "$1 ('$3' not found)"; fi
}
assert_not_contains() {  # description haystack needle
    if [[ "$2" != *"$3"* ]]; then ok "$1"; else bad "$1 ('$3' found)"; fi
}
assert_file_exists() {
    if [[ -e "$2" ]]; then ok "$1"; else bad "$1 ($2 does not exist)"; fi
}
assert_file_absent() {
    if [[ ! -e "$2" ]]; then ok "$1"; else bad "$1 ($2 exists)"; fi
}
errors() { cat "${BASE_COLLECTION_PATH}/must-gather-errors.log"; }

# Staging directories must not be left behind (compare relative counts so
# leftovers from unrelated runs do not false-fail).
staging_before=$(ls -d /tmp/osc-must-gather-vc-* 2>/dev/null | wc -l | tr -d ' ')

# --- successful copy: published, redacted -------------------------------
vc="$temp_dir/success/nodes/worker-0/run/vc"
rc=0
collect_vc_runtime_data test-pod worker-0 "$vc" || rc=$?
if [[ $rc -eq 0 ]]; then ok "successful copy: exit status 0"; else bad "successful copy: exit status 0 (rc=$rc)"; fi
assert_file_exists "successful copy: persist.json published" "$vc/sbs/9c1f5b2e/some-container/persist.json"
persist=$(cat "$vc/sbs/9c1f5b2e/some-container/persist.json" 2>/dev/null)
assert_contains "successful copy: token redacted" "$persist" '"Token":"<redacted>"'
assert_not_contains "successful copy: token value gone" "$persist" "staged-token-value"

# --- failed copy: nothing published, not even redacted partial data ------
vc="$temp_dir/failed-copy/nodes/worker-0/run/vc"
rc=0
OC_CP_FAIL=1 collect_vc_runtime_data test-pod worker-0 "$vc" >/dev/null 2>&1 || rc=$?
if [[ $rc -ne 0 ]]; then ok "failed copy: non-zero exit status"; else bad "failed copy: non-zero exit status (rc=0)"; fi
assert_file_absent "failed copy: nothing published" "$vc"
assert_file_absent "failed copy: no run directory created" "$temp_dir/failed-copy/nodes/worker-0/run"
assert_contains "failed copy: failure logged" "$(errors)" "Copying sandboxed containers runtime data for node <redacted>"

# --- failed redaction: nothing published ---------------------------------
if [[ $(id -u) -ne 0 ]]; then
    vc="$temp_dir/failed-redact/nodes/worker-0/run/vc"
    rc=0
    OC_CP_LOCK=1 collect_vc_runtime_data test-pod worker-0 "$vc" >/dev/null 2>&1 || rc=$?
    if [[ $rc -ne 0 ]]; then ok "failed redaction: non-zero exit status"; else bad "failed redaction: non-zero exit status (rc=0)"; fi
    assert_file_absent "failed redaction: nothing published" "$vc"
    assert_contains "failed redaction: failure logged" "$(errors)" "Redacting tokens in sandboxed containers runtime data for node <redacted>"
else
    echo "  skip - failed redaction: permission tests do not apply to root"
fi

# --- no staging leftovers -------------------------------------------------
staging_after=$(ls -d /tmp/osc-must-gather-vc-* 2>/dev/null | wc -l | tr -d ' ')
if [[ "$staging_before" == "$staging_after" ]]; then
    ok "no staging directories left behind"
else
    bad "no staging directories left behind (before=$staging_before after=$staging_after)"
fi

echo
if (( failures == 0 )); then
    echo "collect_vc_runtime_data tests passed"
    exit 0
fi
echo "collect_vc_runtime_data tests FAILED ($failures failure(s))"
exit 1
