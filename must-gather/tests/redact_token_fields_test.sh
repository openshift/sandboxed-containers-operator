#!/bin/bash

# Tests for redact_token_fields in must-gather/collection-scripts/gather_common.
#
# Covers the KATA-6494 fix: the "Token" field of
# nodes/<node>/run/vc/sbs/*/*/persist.json used to reach the must-gather
# archive as-is, which makes leaktk redact the whole osc-must-gather.tar.gz
# when must-gather output is attached to prow jobs.
#
# Run with: bash tests/redact_token_fields_test.sh   (or: make test)

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
temp_dir=$(mktemp -d)
trap 'rm -rf "$temp_dir"' EXIT

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
cat > "$vc/sbs/77aa31bc/other-container/persist.json" <<'EOF'
{"state":{"Type":1},"Process":{"token":"lowercase-key-token-value","Pid":-1}}
EOF
cat > "$vc/hypervisor/no-tokens.json" <<'EOF'
{"vcpus": 2, "memory": 2048}
EOF
printf 'not json, ignore me\n' > "$vc/sbs/9c1f5b2e/some-container/trace.log"

redact_token_fields "$temp_dir/nodes/worker-0/run/vc"

persist=$(cat "$vc/sbs/9c1f5b2e/some-container/persist.json")
compact=$(cat "$vc/sbs/77aa31bc/other-container/persist.json")
untouched=$(cat "$vc/hypervisor/no-tokens.json")
log=$(cat "$vc/sbs/9c1f5b2e/some-container/trace.log")

assert_not_contains "pretty persist.json: token value is gone" "$persist" "50c2fffdeadbeefcec9"
assert_contains "pretty persist.json: Token marked redacted" "$persist" '"Token": "<redacted>"'
assert_contains "compact persist.json: lowercase token marked redacted" "$compact" '"token":"<redacted>"'
assert_contains "pretty persist.json: other fields preserved" "$persist" '"StartTime": "2026-11-17T05:40:02.747023792Z"'
assert_contains "pretty persist.json: container state preserved" "$persist" '"Pid": 12347'
assert_contains "json without tokens is untouched" "$untouched" '"memory": 2048'
assert_contains "non-json files are untouched" "$log" "not json, ignore me"

# no temporary files left behind
if find "$vc" -name '*.redacting' | grep -q .; then
    bad "no .redacting leftovers"
else
    ok "no .redacting leftovers"
fi

# a missing directory is not an error (nothing was collected)
if redact_token_fields "$temp_dir/nodes/does-not-exist"; then
    ok "missing directory is tolerated"
else
    bad "missing directory is tolerated"
fi

echo
if (( failures == 0 )); then
    echo "redact_token_fields tests passed"
    exit 0
fi
echo "redact_token_fields tests FAILED ($failures failure(s))"
exit 1
