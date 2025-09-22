# OSC Release procedure

## Rationale

See [the documentation][doc_snapshots] about snapshots.

Konflux Snapshots are used to represent a related set of images, that can be used
for testing and releasing.

Snapshots are created automatically whenever something is rebuilt.
When a component is modified, its image is updated, and added to a new snapshot.
The snapshot is then completed with the latest build for the other images for our
application.

Automated snapshot creation makes no difference between `on-pull-request` and
`on-push` builds.
This results in snapshots that contains a mix of merged and unmerged code.
This is fine to test images from PRs (pre-merge), as no PR will rebuild all the
images. But when we try to make a release, if an unrelated PR comes up, its image
can get mixed in the snapshot that we are working on.

The only way to get a releasable snapshot from the automated snapshot creation is
to finely control what gets built to ensure that snapshot and bundle are synchronized.
In any case, when we make a release (stage or prod), the Enterprise Contract for
the release will check that the snapshot and the bundle are in sync, and will error
out if they are not.

This is cumbersome, and could lead to unneeded rebuilds to make sure the latest
image for each component is the one we have in the snapshot (this is what we did
in 1.10.2).

Instead of counting on automated snapshots, we can [create our own snapshots manually][doc_manual_snapshots].

This folder contains a Snapshot definition, listing all the images we want.
We will use the existing nudge PRs for our images to update it at the same time
as the bundle and test catalog are updated, making sure the bundle and snapshot
are synchronized.

Based on this snapshot definition, we can make a controlled release without
wondering what snapshot we should use.

## Process

### Prerequisite

This process requires to use the CLI to interact with our Konflux instance.
You need to `oc login` to our instance of Konflux, and use our team's namespace
on it.

```bash
$ oc login --web https://api.stone-prd-rh01.pg1f.p1.openshiftapps.com:6443/
...
$ oc project ose-osc-tenant
Now using project "ose-osc-tenant" on server "https://api.stone-prd-rh01.pg1f.p1.openshiftapps.com:6443".
```

Another pre-requisite is to have a working build of the operator, including a bundle
that lists all the expected images.

**This operator build needs to be tested and validated by the team.**

### Push the Snapshot

1) verify that the `snapshot.yaml` file contains the same image references as
   the tested operator's bundle.
2) run:

   ```bash
   $ oc create -f snapshot.yaml
   ...
   ```

3) in the Konflux console, or with the CLI, you can verify that the snapshot
   is listed appropriately under the name "osc-release-snapshot-[number]".

Note: snapshots need to have a unique name. They are deleted after some time, but
if you do multiple pushes in a short time, you will need to rename it.
This is why we append a number at the end of its name.
Modify this number to make sure the snapshot is unique in our instance.
You don't need to commit the name change to our repo.

### Make a stage release

This folder contains a `stage-release.yaml` file that references our snapshot
by its name. You can use it to trigger a stage release for the snapshot you
just pushed.

1) make sure `stage-release.yaml` is using the right snapshot name, as set in
   `snapshot.yaml`
2) run:

    ```bash
    $ oc create -f stage-release.yaml
    ...
    ```

Note: as for the snapshot, the release need to be uniquely named. Make sure you
edit that name if you need to make multiple stage releases.

### Make a prod release

Stage and prod releases are made exactly in the same way.
We could just edit `stage-release.yaml` and modify the `releasePlan` reference from
"stage" to "prod", and just push the same file again.
Now to avoid errors like "pushing a prod release when we thought we're making a stage one",
we are keeping two separate files for stage and prod releases.

1) make sure `prod-release.yaml` is using the right snapshot name, as set in
   `snapshot.yaml`
2) make sure `prod-release.yaml` has all the expected issues and CVEs listed for
   your release.
3) Fill the `synopsis` in `prod-release.yaml` to make sure the right version is
   referenced in our advisory. Optionally set the other fields too to override
   the defaults that come from our ReleasePlan.
4) Double check everything.
5) run:

    ```bash
    $ oc create -f prod-release.yaml
    ...
    ```

## Why not use the UI console to make the release?

All of the above can also be done from the Konflux console if you feel more
confortable with it. The only caveat is listing the CVEs for the prod release,
as the console UI have [a bug (at the time of writing)][konflux_bug] that generates
wrongly formatted structures when we try to list multiple components for the same
CVE.

We started using the CLI because of this bug when we did 1.10.2.
If we don't have CVEs to list, or when the bug is fixed in Konflux, we can
consider reusing the console.

---
[doc_snapshots]: https://konflux.pages.redhat.com/docs/users/testing/integration/snapshots/index.html
[doc_manual_snapshots]: https://konflux.pages.redhat.com/docs/users/testing/integration/snapshots/working-with-snapshots.html
[konflux_bug]: https://issues.redhat.com/browse/KFLUXSPRT-5045
