# Sanity tests

A sanity check per workload type, meant to gate the heavier test suites: if a
plain pod does not run, there is no point in reading the output of hundreds of
other tests. Each check creates a pod, waits for it to run, deletes it and
checks it is gone. When it fails, it prints the logs that explain the failure
and writes them to the results directory as well.

The tests are written in [bats](https://bats-core.readthedocs.io) and driven by
`../run_sanity_tests.sh`.

## Running

```bash
cd test/e2e
export KUBECONFIG=~/path/to/kubeconfig

# kata containers
./run_sanity_tests.sh --workload kata --runtime_class kata

# peer pods
./run_sanity_tests.sh --workload peer-pods --runtime_class kata-remote

# confidential containers on peer pods
./run_sanity_tests.sh --workload coco --runtime_class kata-remote

# confidential containers on bare metal
./run_sanity_tests.sh --workload coco --runtime_class kata-cc \
    --initdata initdata.txt
```

The cluster is not inspected to find out how it was installed: pass the
workload and the RuntimeClass it was installed with.

Options and environment variables are documented in
`./run_sanity_tests.sh --help`.

A single `.bats` file can also be run directly, for instance to iterate on a
test, as every variable it reads has a default:

```bash
bats sanity/kata.bats
```

## Workloads

| `--workload` | RuntimeClass | Default timeout | Checks |
|---|---|---|---|
| `kata` | `kata` | 180s | pod runs, pod is deleted |
| `peer-pods` | `kata-remote` | 420s | pod runs, pod is deleted |
| `coco` | `kata-remote`, `kata-cc`, ... | 600s | pod runs, pod reads a resource from Trustee, pod is deleted |

The `coco` workload covers both confidential containers flavours. An init
container reads `SANITY_KBS_RESOURCE` from Trustee, which only succeeds once
the pod has attested, so the pod reaching Ready proves it reached Trustee.

The read is an init container rather than a command the test runs in the pod
because neither of the two ways of observing such a command works for peer
pods: the Kata Agent policy carried in initdata denies `exec`, and
`oc logs` returns nothing for a peer pod. An init container needs
neither: the pod is only Ready if the container exited 0, and a failure is
visible in `oc describe pod` and in the Trustee logs.

Three prerequisites come with it:

- The workload image is one the cluster's image security policy admits. That
  policy travels in the initdata, and the guest refuses to pull anything else:
  our CI admits a single repository, so those jobs set `SANITY_IMAGE` to an
  image from it. The probe uses `curl` or `wget`, whichever the image has.
- Trustee serves the resource. `default/kbsres1/key1` is served by a Trustee
  installed with the [confidential-devhub
  charts](https://github.com/confidential-devhub/charts); set
  `SANITY_KBS_RESOURCE` for any other setup.
- The pod knows the Trustee address, which it reads from initdata. Pass the
  initdata of the cluster with `--initdata` and it is set on the pod as the
  `io.katacontainers.config.hypervisor.cc_init_data` annotation. Our CI
  clusters work this way: the step that installs Trustee writes the initdata
  it generated to `${SHARED_DIR}/INITDATA`, for the tests to attach to their
  pods.

  It has to be the initdata the cluster was set up with: initdata is measured
  into PCR 8, so a different one fails attestation wherever reference values
  are enforced.

  `--initdata` can be left out only on a peer pods cluster whose
  `peer-pods-cm` config map carries a global `INITDATA`, which every pod
  inherits. A bare-metal cluster has no such setting.

## Logs collected on failure

Everything is printed to stdout and appended to
`results/<timestamp>/<workload>-failure.log`.

| Workload | Logs |
|---|---|
| all | `oc get pod`, `oc describe pod`, `oc logs` |
| `kata` | kata and CRI-O journal entries of the pod's node |
| `peer-pods` | logs of the cloud-api-adaptor pod running on the pod's node |
| `coco` | Trustee logs, plus the cloud-api-adaptor logs on peer pods or the node journal on bare metal |

`oc logs` is collected for every workload but is empty for peer pods,
which do not surface container output.

Trustee logs are only collected when Trustee runs in the cluster under test;
when it serves the cluster from elsewhere, the run says so instead.

Journal entries are limited to the time window of the test run, so that the
output holds what happened to this pod and not the whole history of the node.
They are read with `oc debug node/<node> -- chroot /host journalctl`, which
requires a kubeconfig with cluster-admin and a debug image reachable from the
cluster.
