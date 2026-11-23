#!/bin/bash

# Tests for redact_token_fields in must-gather/collection-scripts/gather_common.
#
# Covers the KATA-6494 fix: the "Token" field of
# nodes/<node>/run/vc/sbs/*/*/persist.json used to reach the must-gather
# archive as-is, which makes leaktk redact the whole osc-must-gather.tar.gz
# when must-gather output is attached to prow jobs.
#
# The redaction is JSON-aware, so the fixtures also cover values containing
# escaped quotes, uppercase field names, and values placed on a separate
# line from the key, plus the failure paths (unreadable file, unreadable
# directory during traversal).
#
# Run with: bash tests/redact_token_fields_test.sh   (or: make test)

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
temp_dir=$(mktemp -d)
trap 'chmod -R u+rX "$temp_dir" 2>/dev/null; rm -rf "$temp_dir"' EXIT

export BASE_COLLECTION_PATH="$temp_dir/must-gather"
mkdir -p "$BASE_COLLECTION_PATH"

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
assert_identical() {  # description file1 file2
    if cmp -s "$2" "$3"; then ok "$1"; else bad "$1 ($2 and $3 differ)"; fi
}

# Fake node state, mirroring the layout and content observed in KATA-6494:
# nodes/<worker>/run/vc/sbs/<sandbox-id>/<container-id>/persist.json
vc="$temp_dir/nodes/worker-0/run/vc"
mkdir -p "$vc/sbs/9c1f5b2e/some-container" "$vc/sbs/77aa31bc/other-container" "$vc/hypervisor"

cat > "$vc/sbs/9c1f5b2e/some-container/persist.json" <<'EOF'
{
  "state": {
    "Type": 1,
    "Pid": 12345,
    "StartTime": "2026-11-17T05:40:02.747023792Z",
    "Token": "50c2fffdeadbeefcec9"
  },
  "Process": {
    "StartTime": "2026-11-17T05:40:02.747023792Z",
    "Token": "50c2fffdeadbeefcec9",
    "Pid": -1
  },
  "containers": [
    {
      "id": "some-container",
      "state": {
        "Type": 1,
        "StartTime": "2026-11-17T05:41:11.101",
        "Pid": 12347
      }
    }
  ]
}
EOF

# Compact JSON with a lowercase key
printf '{"state":{"Type":1},"Process":{"token":"lowercase-key-token-value","Pid":-1}}\n' \
    > "$vc/sbs/77aa31bc/other-container/persist.json"

# Value containing an escaped quote: the whole value must go, and the JSON
# must stay valid
printf '{"Token":"escaped-\\"quote-token-value","Pid":1}\n' \
    > "$vc/sbs/77aa31bc/other-container/escaped.json"

# Uppercase field name
printf '{"TOKEN":"uppercase-token-value","Pid":1}\n' \
    > "$vc/sbs/77aa31bc/other-container/uppercase.json"

# Value placed on a separate line from the key
cat > "$vc/sbs/77aa31bc/other-container/multiline.json" <<'EOF'
{
  "Process": {
    "Token":
      "multiline-token-value",
    "Pid": -1
  }
}
EOF

# The string "Token" as a value is not a field name and must be preserved
printf '{"desc":"Token","Pid":1}\n' > "$vc/hypervisor/value-is-token.json"

# No tokens at all: must stay byte-identical
printf '{"vcpus": 2, "memory": 2048}\n' > "$vc/hypervisor/no-tokens.json"
cp "$vc/hypervisor/no-tokens.json" "$temp_dir/no-tokens.orig"
cp "$vc/hypervisor/value-is-token.json" "$temp_dir/value-is-token.orig"

# Non-JSON files must not be touched
printf 'not json, ignore me\n' > "$vc/sbs/9c1f5b2e/some-container/trace.log"

# Unreadable JSON file: processing fails, other files are still redacted
# (skipped when running as root, where permissions do not block reads)
run_permission_tests=0
if [[ $(id -u) -ne 0 ]]; then
    run_permission_tests=1
    printf '{"Token":"locked-token-value"}\n' > "$vc/sbs/9c1f5b2e/some-container/locked.json"
    chmod 000 "$vc/sbs/9c1f5b2e/some-container/locked.json"
fi

rc=0
redact_token_fields "$vc" >/dev/null 2>&1 || rc=$?

persist=$(cat "$vc/sbs/9c1f5b2e/some-container/persist.json")
compact=$(cat "$vc/sbs/77aa31bc/other-container/persist.json")
escaped=$(cat "$vc/sbs/77aa31bc/other-container/escaped.json")
uppercase=$(cat "$vc/sbs/77aa31bc/other-container/uppercase.json")
multiline=$(cat "$vc/sbs/77aa31bc/other-container/multiline.json")
log=$(cat "$vc/sbs/9c1f5b2e/some-container/trace.log")

assert_not_contains "pretty persist.json: token value is gone" "$persist" "50c2fffdeadbeefcec9"
assert_contains "pretty persist.json: Token marked redacted" "$persist" '"Token": "<redacted>"'
assert_contains "pretty persist.json: other fields preserved" "$persist" '"StartTime": "2026-11-17T05:40:02.747023792Z"'
assert_contains "pretty persist.json: container state preserved" "$persist" '"Pid": 12347'
assert_contains "compact persist.json: lowercase token marked redacted" "$compact" '"token":"<redacted>"'
assert_contains "escaped quote: value fully redacted" "$escaped" '"Token":"<redacted>"'
assert_not_contains "escaped quote: no partial value left" "$escaped" "quote-token-value"
assert_contains "escaped quote: JSON stays valid" "$escaped" '"Pid":1}'
assert_contains "uppercase: TOKEN marked redacted" "$uppercase" '"TOKEN":"<redacted>"'
assert_not_contains "uppercase: value gone" "$uppercase" "uppercase-token-value"
assert_contains "multiline: value marked redacted" "$multiline" '"<redacted>"'
assert_not_contains "multiline: value gone" "$multiline" "multiline-token-value"
assert_contains "multiline: JSON stays valid" "$multiline" '"Pid": -1'
assert_identical "value string Token is preserved" "$temp_dir/value-is-token.orig" "$vc/hypervisor/value-is-token.json"
assert_identical "token-free json is byte-identical" "$temp_dir/no-tokens.orig" "$vc/hypervisor/no-tokens.json"
assert_contains "non-json files are untouched" "$log" "not json, ignore me"

# no temporary files left behind
if find "$vc" -name '*.redacting' | grep -q .; then
    bad "no .redacting leftovers"
else
    ok "no .redacting leftovers"
fi

if [[ $run_permission_tests -eq 1 ]]; then
    if [[ $rc -ne 0 ]]; then
        ok "unreadable file makes redaction fail"
    else
        bad "unreadable file makes redaction fail (rc=$rc)"
    fi
    if [[ ! -e "$vc/sbs/9c1f5b2e/some-container/locked.json.redacting" ]]; then
        ok "failed file leaves no .redacting file"
    else
        bad "failed file leaves no .redacting file"
    fi
    chmod 644 "$vc/sbs/9c1f5b2e/some-container/locked.json"
    assert_contains "unreadable file content unchanged" \
        "$(cat "$vc/sbs/9c1f5b2e/some-container/locked.json")" "locked-token-value"
else
    if [[ $rc -ne 0 ]]; then
        bad "redaction should succeed when all files are readable (rc=$rc)"
    else
        ok "redaction succeeds when all files are readable"
    fi
fi

# A missing directory is not an error (nothing was collected)
if redact_token_fields "$temp_dir/nodes/does-not-exist"; then
    ok "missing directory is tolerated"
else
    bad "missing directory is tolerated"
fi

# An unreadable subdirectory makes the traversal fail: the whole call must
# report failure so callers drop the data (skipped when running as root)
if [[ $run_permission_tests -eq 1 ]]; then
    broken="$temp_dir/nodes/worker-1/run/vc"
    mkdir -p "$broken/sbs/locked-dir"
    printf '{"Token":"hidden-token-value"}\n' > "$broken/sbs/locked-dir/hidden.json"
    chmod 000 "$broken/sbs/locked-dir"
    rc=0
    redact_token_fields "$broken" >/dev/null 2>&1 || rc=$?
    chmod 755 "$broken/sbs/locked-dir"
    if [[ $rc -ne 0 ]]; then
        ok "traversal failure is reported"
    else
        bad "traversal failure is reported (rc=$rc)"
    fi
fi

echo
if (( failures == 0 )); then
    echo "redact_token_fields tests passed"
    exit 0
fi
echo "redact_token_fields tests FAILED ($failures failure(s))"
exit 1
