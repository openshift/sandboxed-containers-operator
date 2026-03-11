# update-rpms-lock.sh

Automates the generation of `rpms.lock.yaml` for OpenShift Sandboxed Containers using [rpm-lockfile-prototype](https://github.com/konflux-ci/rpm-lockfile-prototype).

The script runs inside a disposable UBI container (via Podman), registers with Red Hat subscription-manager, configures repositories for multi-arch support, and produces the lockfile from `rpms.in.yaml`. The UBI version (9, 10, …) is automatically detected from the `-i IMAGE` flag.

## Prerequisites

- **Podman** installed on the host
- A Red Hat **activation key** ([how to create one](https://konflux.pages.redhat.com/docs/users/building/activation-keys-subscription.html#create-the-activation-key))
- A Red Hat **organization ID**
- **Registry credentials** for `registry.redhat.io`

## Usage

Run the script from the directory containing `rpms.in.yaml`:

```bash
./update-rpms-lock.sh \
  -k "$ACTIVATION_KEY" \
  -o "$ORG_ID" \
  -u "$REGISTRY_USERNAME" \
  -p "$REGISTRY_PASSWORD"
```

### Options

| Flag | Environment Variable | Description | Default |
|------|---------------------|-------------|---------|
| `-k` | `ACTIVATION_KEY` | Red Hat activation key | *(required)* |
| `-o` | `ORG_ID` | Red Hat organization ID | `11009103` |
| `-u` | `REGISTRY_USERNAME` | Registry username | *(required)* |
| `-p` | `REGISTRY_PASSWORD` | Registry password | *(required)* |
| `-i` | `IMAGE` | Container base image | `registry.access.redhat.com/ubi9/ubi:latest` |
| `-h` | | Show usage help | |

Flags take precedence over environment variables.

## What it does

1. Extracts the UBI version from the image name (e.g. `ubi9` → `9`, `ubi10` → `10`)
2. Launches a UBI container with the current directory mounted at `/source`
3. Installs `subscription-manager`, `skopeo`, and `python3-pip`
4. Registers the container with Red Hat using the provided activation key
5. Installs `rpm-lockfile-prototype` from GitHub
6. Copies and adjusts repo files (`redhat.repo`, `ubi.repo`) for multi-arch (`$basearch`) support, parameterized by UBI version
7. Enables source RPM repositories
8. Sets `skip_if_unavailable=1` on all enabled repos (avoids failures from non-existent EUS or source repos)
9. Logs into `registry.redhat.io` via `skopeo`
10. Runs `rpm-lockfile-prototype rpms.in.yaml` to generate `rpms.lock.yaml`

## Output

- `rpms.lock.yaml` — generated lockfile
- `redhat.repo` / `ubi.repo` — repo configuration files adjusted for multi-arch

> **Note:** If all `url` lines in `rpms.lock.yaml` reference `cdn.redhat.com` (and none reference `cdn-ubi.redhat.com`), then `ubi.repo` is not needed and can be removed.

## Authors

- Julien Rope <jrope@redhat.com>
- Daniel Kreling <dkreling@redhat.com>
