#!/bin/bash

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
temp_dir=$(mktemp -d)
trap 'rm -rf "$temp_dir"' EXIT

export FAKE_HOST="$temp_dir/host"
export OC_ARCHIVE="$temp_dir/sent.tar"
mkdir -p "$temp_dir/bin" "$FAKE_HOST/etc/kubernetes/cni/net.d/whereabouts.d" \
    "$FAKE_HOST/etc/cni/net.d" "$temp_dir/output with spaces"

# Run the node-side tar locally and capture its output as the data sent by oc.
cat > "$temp_dir/bin/oc" <<'EOF'
#!/bin/bash
set -o pipefail
if [[ ${OC_FAIL:-} == 1 ]]; then
    exit 42
fi
[[ $1 == exec && $3 == -n && $4 == node-gather && $5 == -- && $6 == tar && $7 == -C ]] || exit 1
shift 7
host_dir=$1
shift
[[ $host_dir == /host/* ]] || exit 1
tar -C "$FAKE_HOST/${host_dir#/host/}" "$@" | tee "$OC_ARCHIVE"
EOF
chmod +x "$temp_dir/bin/oc"
export PATH="$temp_dir/bin:$PATH"

printf 'multus configuration\n' > "$FAKE_HOST/etc/kubernetes/cni/net.d/00-multus.conf"
printf 'kubeconfig: /etc/kubernetes/cni/net.d/whereabouts.d/whereabouts.kubeconfig\n' > "$FAKE_HOST/etc/kubernetes/cni/net.d/whereabouts.d/whereabouts.conf"
printf 'node name\n' > "$FAKE_HOST/etc/kubernetes/cni/net.d/whereabouts.d/nodename"
printf 'sensitive-test-token\n' > "$FAKE_HOST/etc/kubernetes/cni/net.d/whereabouts.d/whereabouts.kubeconfig"
printf 'sensitive-test-token\n' > "$FAKE_HOST/etc/kubernetes/cni/net.d/other.kubeconfig"
printf 'alternate CNI configuration\n' > "$FAKE_HOST/etc/cni/net.d/10-cni.conf"
printf 'sensitive-test-token\n' > "$FAKE_HOST/etc/cni/net.d/kubeconfig"
printf 'sensitive-test-token\n' > "$FAKE_HOST/etc/cni/net.d/other.KubeConfig"

for conf_dir in etc/kubernetes/cni/net.d etc/cni/net.d; do
    destination="$temp_dir/output with spaces/$conf_dir"
    mkdir -p "$destination"
    bash "$script_dir/collection-scripts/gather_cni_config" test-pod "$conf_dir" "$destination"

    if tar -tf "$OC_ARCHIVE" | grep -qi kubeconfig || grep -aq 'sensitive-test-token' "$OC_ARCHIVE"; then
        echo "kubeconfig was sent from $conf_dir" >&2
        exit 1
    fi
    if find "$destination" -iname '*kubeconfig*' -print -quit | grep -q .; then
        echo "kubeconfig was gathered from $conf_dir" >&2
        exit 1
    fi
done

cmp "$FAKE_HOST/etc/kubernetes/cni/net.d/00-multus.conf" "$temp_dir/output with spaces/etc/kubernetes/cni/net.d/00-multus.conf"
cmp "$FAKE_HOST/etc/kubernetes/cni/net.d/whereabouts.d/whereabouts.conf" "$temp_dir/output with spaces/etc/kubernetes/cni/net.d/whereabouts.d/whereabouts.conf"
cmp "$FAKE_HOST/etc/kubernetes/cni/net.d/whereabouts.d/nodename" "$temp_dir/output with spaces/etc/kubernetes/cni/net.d/whereabouts.d/nodename"
cmp "$FAKE_HOST/etc/cni/net.d/10-cni.conf" "$temp_dir/output with spaces/etc/cni/net.d/10-cni.conf"

if OC_FAIL=1 bash "$script_dir/collection-scripts/gather_cni_config" test-pod etc/cni/net.d "$temp_dir/output with spaces" 2>/dev/null; then
    echo "node-side failure was not reported" >&2
    exit 1
fi
if bash "$script_dir/collection-scripts/gather_cni_config" test-pod etc/cni/net.d "$temp_dir/missing" 2>/dev/null; then
    echo "local extraction failure was not reported" >&2
    exit 1
fi

echo "CNI collection tests passed"
