# Peer-Pods Encrypted Scratch Storage

This document describes how writable container data is protected inside a
peer-pod VM, which deployments get that protection, and where the feature is
implemented.

> **Important:** this feature is implemented downstream-only at the time this
> document was created.

## TL;DR

The default pod VM image that OSC ships contains an **encrypted scratch
partition**: a LUKS2 volume created inside the guest at every boot, keyed by a
random key that never leaves the VM.

It is a property of the **image**, not of the deployment. It is always on in
that image, for every provider, and it is *not* conditional on confidential
containers. There is no API to enable or disable it.

## Why it exists

A confidential peer pod runs inside a cloud confidential VM (CVM). The
hardware encrypts the VM's **memory**, so the cloud provider cannot read it.
The VM's **virtual disk** is not covered by that guarantee — it is ordinary
cloud block storage.

This matters because a confidential pod pulls its container images *inside the
guest* ("guest pull"), precisely so the host never sees them decrypted. The
container then needs writable space to run in. If that space lived on the
plain virtual disk, the image contents and everything the container writes
would be readable by the cloud provider — defeating the point.

So three layers cover a confidential pod VM:

| What | Protected by |
| --- | --- |
| Memory | CPU / TEE (AMD SEV-SNP, Intel TDX) |
| Root filesystem (read-only) | dm-verity — integrity, tamper-evident |
| Writable container data | **LUKS2 encrypted scratch partition** |

The third one is the subject of this document.

## How it works, briefly

The pod VM image is built with unallocated free space left at the end of its
disk. At every boot, inside the guest:

1. A systemd unit (`luks-scratch.service`) runs before `kata-agent` starts.
2. It generates a 64-byte random key from the guest's `/dev/urandom` into
   `/run/lukspw.bin` (tmpfs).
3. `systemd-repart` creates a partition in the free space, formats it as LUKS2
   with that key, and puts an ext4 filesystem inside.
4. `cryptsetup luksOpen` exposes it as `/dev/mapper/scratch`.
5. A `kata-agent.service` drop-in mounts it before the agent starts.

The key is generated inside the guest, is never written to persistent storage,
and is never sent anywhere. It is discarded when the VM stops, so the data is
unrecoverable afterwards. Each boot gets a fresh key and a fresh partition,
and because pod VMs are single-use, that means effectively per-pod.

## Which deployments get it

This is the part that is easy to get wrong. OSC supports peer pods with and
without confidential containers, on several providers — but that distinction
does **not** select a different pod VM image. There is one default image.

When `PODVM_IMAGE_URI` is empty in the provider's `*-podvm-image-cm`, the
operator fills it in with the prebuilt dm-verity image (shipped in the CSV),
for every provider except `s390x`.

| Scenario | Pod VM image used | Encrypted scratch? |
| --- | --- | --- |
| Any x86_64 provider (Azure, AWS, GCP), **with or without** CoCo, defaults untouched | prebuilt `osc-dm-verity-image` | **Yes** |
| IBM Z / `s390x` | excluded from the default; operator-built or user-supplied | No |
| User sets `PODVM_IMAGE_URI` to their own image | that image | Depends on that image |
| User sets `AZURE_IMAGE_ID` / `AWS_AMI_ID` directly | image generation is skipped entirely | No |
| Operator-built (packer) image with `CONFIDENTIAL_COMPUTE_ENABLED=yes` | packer image from `config/peerpods/podvm/` | No — tmpfs overlay instead, see below |
| Operator-built (packer) image, non-CoCo | packer image | No |

### Important consequence for non-confidential peer pods

With `DISABLECVM: "true"` the pod VM is **not** a confidential VM. Its memory
is readable by the hypervisor, and the LUKS key lives in that memory
(`/run/lukspw.bin` on tmpfs, plus the kernel keyring).

So in a non-CoCo deployment the encrypted scratch still runs, but it does not
protect you from the cloud provider or anyone with live access to the host —
they can simply read the key. What it still gives you is encryption at rest on
the backing volume: once the VM is gone the key is gone, so the disk contents
are unrecoverable from storage forensics or a leftover snapshot.

Treat it as defence in depth there, not as a confidentiality guarantee. The
guarantee only holds when the VM is a CVM.

### Why the operator-built CoCo image is different

The packer-based image built by `config/peerpods/podvm/` takes a different
approach for the same problem: `create_overlay_mount_unit` in
[`config/peerpods/podvm/lib.sh`](../config/peerpods/podvm/lib.sh) mounts a
tmpfs at `/run/kata-containers/image/overlay`, so the writable layer lives in
RAM and is therefore covered by TEE memory encryption rather than by LUKS.
That unit is only created when `CONFIDENTIAL_COMPUTE_ENABLED=yes`.

## What this means operationally

**There is nothing to configure.** This is not exposed through `KataConfig`,
`peer-pods-cm`, or any other API. It is compiled into the pod VM image. The
only way to opt out is to point `PODVM_IMAGE_URI` or the provider image ID at
a different image.

**It fails closed.** If the encrypted scratch cannot be set up, `kata-agent`
does not start and the pod fails. There is no silent fallback to unencrypted
storage.

**It provides confidentiality, not integrity.** The volume is plain
dm-crypt/LUKS2. An attacker with write access to the backing disk cannot read
the data but can corrupt it without the guest detecting it cryptographically.
The read-only root filesystem *is* integrity protected, via dm-verity.

**Size** is whatever is unallocated on the pod VM disk at boot; the image
build reserves 2500 MiB for it.

## Where it is implemented

The operator does not implement this. It lives entirely in the pod VM image
build:

- **Repository:** [confidential-devhub/coco-podvm-scripts](https://github.com/confidential-devhub/coco-podvm-scripts)
- **Reference documentation:** [`scripts/coco/podvm/luks-scratch/README.md`](https://github.com/confidential-devhub/coco-podvm-scripts/blob/main/scripts/coco/podvm/luks-scratch/README.md)
  — unit ordering, the `systemd-repart` definition, build-time space
  reservation, and the security properties in detail.

The operator's only involvement is choosing that image by default.
