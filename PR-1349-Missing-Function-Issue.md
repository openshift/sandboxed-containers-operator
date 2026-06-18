# PR #1349 Investigation: Missing Function Issue

**Date**: 2026-06-17  
**Issue**: Undefined function `copy_kata_remote_config_files()` in PR #1349  
**Impact**: Critical - Installation will fail  

---

## Summary

The cherry-picked PR #1349 commit (47d28ac0) calls `copy_kata_remote_config_files()` but this function is **not defined** in the extracted code. This is because PR #1349 consists of multiple commits, and we only cherry-picked the final commit without its dependencies.

## Root Cause

PR #1349 consists of at least 4 commits in sequence:

1. `b4fc5c59` - daemonset deployment: prevent installation if NODE_LABEL envar is unset
2. `74746253` - daemonset deployment: migrate peer-pods config handling to osc-rpm DaemonSet
3. `47d28ac0` - daemonset deployment: enable Kata installation without node reboots ← **We cherry-picked this**
4. `9c19cf2c` - logic to backup etc before uninstall (WIP)

**The problem**: Commit `47d28ac0` depends on `74746253`, which defines:
- `copy_kata_remote_config_files()` function
- `remove_kata_remote_config_files()` function  
- `copy_file()` helper function
- `remove_file()` helper function
- `FILES` array with kata-remote configuration paths

## Missing Code from Commit 74746253

### FILES Array Definition

```bash
# Format: "absolute_source_path:absolute_dest_path:octal_mode"
FILES=(
    "/files/50-kata-remote:/host/etc/crio/crio.conf.d/50-kata-remote:0644"
    "/files/configuration-remote.toml:/host/opt/kata/configuration-remote.toml:0420"
)
```

### Helper Functions

```bash
copy_file() {
    local src="$1" dest="$2" perm="$3"
    if [[ -f "$src" ]]; then
        if [[ -e "$dest" ]]; then
            echo "$dest already exists, skipping"
        else
            # GNU coreutils install: create parents (-D) and set mode (-m)
            install -D -m "$perm" "$src" "$dest"
            echo "Installed $(basename "$src") -> $dest (mode $perm)"
        fi
    else
        echo "Warning: $(basename "$src") not found"
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
```

### Main Functions

```bash
copy_kata_remote_config_files() {
    echo "Starting configuration copy..."

    for entry in "${FILES[@]}"; do
        IFS=: read -r src dest perm <<<"$entry"
        copy_file "$src" "$dest" "$perm"
    done

    echo "Configuration copy completed"
}

remove_kata_remote_config_files() {
    echo "Starting configuration removal..."

    for entry in "${FILES[@]}"; do
        IFS=: read -r _src dest _perm <<<"$entry"
        remove_file "$dest"
    done

    echo "Configuration removal completed"
}
```

## What is "kata-remote"?

"kata-remote" is **NOT** "kata-oc" (which is used for MachineConfigPool-based deployments).

**kata-remote** refers to:
1. **CRI-O runtime configuration**: `/etc/crio/crio.conf.d/50-kata-remote`
2. **Kata configuration**: `/opt/kata/configuration-remote.toml`

These files configure the kata-remote runtime, which is used for **peer-pods** (remote hypervisor) scenarios, as opposed to the local kata runtime.

## How PR #2312 Handles This

PR #2312 **does NOT** need these helper functions because it uses a different approach:

### CRI-O Drop-In Handling

```bash
# rpm-ostree --apply-live updates /usr immediately but /etc changes from
# RPMs only land after reboot (OSTree three-way merge). Copy the kata
# CRI-O drop-ins from the RPM's factory-defaults location now so CRI-O
# can see the runtime handlers without a reboot.
for conf in /host/usr/etc/crio/crio.conf.d/50-kata*; do
    [ -f "$conf" ] && cp "$conf" /host/etc/crio/crio.conf.d/
done
```

**Benefits:**
- Generic `50-kata*` pattern catches **both** `50-kata` and `50-kata-remote`
- Copies from RPM's installed location (`/usr/etc/`) to active location (`/etc/`)
- No external file dependencies
- Documents the OSTree three-way merge behavior

### Cleanup During Uninstall

```bash
# Remove the CRI-O drop-ins we copied during install
rm -f /host/etc/crio/crio.conf.d/50-kata*
```

**Benefits:**
- Removes both kata and kata-remote configs
- Symmetric with install operation
- No need for separate tracking

## Impact on Our Builds

### hpc-deployment-1349 Image (BROKEN)

**Image**: `quay.io/c3d/openshift-sandboxed-containers-operator:hpc-deployment-live-apply-1349`

**Status**: ❌ **BROKEN** - Will fail at runtime

**Error When Installing:**
```bash
./osc-kata-install.sh: line 261: copy_kata_remote_config_files: command not found
```

**Affects:**
- Any kata installation attempt
- Both regular kata and kata-remote configurations

### hpc-deployment-2312 Image (WORKING)

**Image**: `quay.io/c3d/openshift-sandboxed-containers-operator:hpc-deployment-live-apply-2312`

**Status**: ✅ **WORKING** - Properly handles all configurations

**Works Because:**
- Uses wildcard pattern to copy all `50-kata*` files
- No dependency on external helper functions
- Self-contained implementation

## Correct Label: "kata-remote" NOT "kata-oc"

### kata-oc
- **Purpose**: MachineConfigPool name for MCO-based installations
- **Scope**: OpenShift cluster with Machine Config Operator
- **Files**: Managed by MachineConfig CRs
- **Location**: `controllers/openshift_controller.go`

### kata-remote  
- **Purpose**: Runtime configuration for peer-pods/remote hypervisor
- **Scope**: Both MCO and DaemonSet deployments
- **Files**: 
  - `/etc/crio/crio.conf.d/50-kata-remote`
  - `/opt/kata/configuration-remote.toml`
- **Location**: `scripts/kata-install/osc-kata-install.sh`

## Recommendations

### Option 1: Cherry-Pick Missing Commit (Complex)

Cherry-pick commit `74746253` into `hpc-deployment-1349`:

```bash
git checkout hpc-deployment-1349
git cherry-pick 74746253
```

**Pros:**
- Fixes the immediate issue
- Keeps PR #1349 approach intact

**Cons:**
- Adds more code (+64 lines)
- Still has the other issues identified in the comparison review
- Requires rebuilding and retesting

### Option 2: Use PR #2312 (Recommended)

Continue using `hpc-deployment-2312` as the production image.

**Pros:**
- Already working correctly
- Simpler implementation
- Better documented
- Production-tested approach

**Cons:**
- None identified

### Option 3: Fix PR #1349 with PR #2312's Approach (Best for Upstream)

If submitting PR #1349 upstream, replace the `copy_kata_remote_config_files()` call with PR #2312's approach:

```bash
# Instead of:
copy_kata_remote_config_files

# Use:
for conf in /host/usr/etc/crio/crio.conf.d/50-kata*; do
    [ -f "$conf" ] && cp "$conf" /host/etc/crio/crio.conf.d/
done
```

## Testing Recommendations

### For hpc-deployment-1349 (If Fixed)

1. **Basic Installation**:
   ```bash
   # Install kata and verify kata-remote configs are present
   ls -la /etc/crio/crio.conf.d/50-kata*
   ls -la /opt/kata/configuration-remote.toml
   ```

2. **Verify CRI-O Sees Runtimes**:
   ```bash
   crictl info | grep -A5 runtimeHandlers
   # Should show both "kata" and "kata-remote"
   ```

3. **Test Uninstall**:
   ```bash
   # After uninstall, configs should be gone
   ls /etc/crio/crio.conf.d/50-kata* 2>&1 | grep "No such file"
   ```

### For hpc-deployment-2312 (Current)

Same tests as above - should all pass.

## Conclusion

The `copy_kata_remote_config_files()` function issue in our cherry-picked PR #1349 is a **critical bug** that makes the image non-functional. This is caused by incomplete cherry-picking of dependent commits.

**Recommendation**: Continue using `hpc-deployment-live-apply-2312` as it:
1. ✅ Works correctly without this dependency
2. ✅ Handles both kata and kata-remote configurations
3. ✅ Uses simpler, more maintainable code
4. ✅ Is better documented

The PR #1349 image (`hpc-deployment-live-apply-1349`) should be considered **broken** and should not be used for testing or production until fixed.
