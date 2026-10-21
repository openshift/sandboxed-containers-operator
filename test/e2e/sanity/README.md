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

## Logs collected on failure

Everything is printed to stdout and appended to
`results/<timestamp>/<workload>-failure.log`.

| Workload | Logs |
|---|---|
| all | `oc get pod`, `oc describe pod`, `oc logs` |
| `kata` | kata and CRI-O journal entries of the pod's node |

Journal entries are limited to the time window of the test run, so that the
output holds what happened to this pod and not the whole history of the node.
They are read with `oc debug node/<node> -- chroot /host journalctl`, which
requires a kubeconfig with cluster-admin and a debug image reachable from the
cluster.
