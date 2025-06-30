# OSC FBC

The file based catalog (**FBC**) for OpenShift sandboxed containers.

## Prerequisites

### Install `opm`

You need v1.46.0 or greater.

Download the binary from [Github releases](https://github.com/operator-framework/operator-registry/releases).

### Install `jq` and `curl`

Packages from your favorite distros should work.

## Update the FBC

1. Update the digests in the template.
2. Run `./update.sh [VERSION]` to update the digests in the template.
3. Run `./render.sh [VERSION]` to update the actual catalog.
4. Open a pull request with your changes.

## Add a new OpenShift version

In examples that follow, the latest release is `v4.17` and you want to release for `v4.18` too.

### New Konflux application

1. In the web UI, add a new application and a new component.
2. Ignore the pull request from the Konflux bot.
3. Add the new application to the ReleasePlanAdmission.
4. Create a new ReleasePlan.

### New files

1. Run the duplicate script:
    ```
    ./duplicate.sh v4.17 v4.18
    ```

2. Run the render script to update the actual catalog. Note that this command will not make any changes, if they are not needed.
    ```
    ./render.sh
    ```

## Add a previously released catalog

Run the migrate script. For example:
```
./migrate.sh v4.16
```

## Further reading

  - [File-based Catalogs](https://olm.operatorframework.io/docs/reference/file-based-catalogs/) in the Operator Lifecycle Manager (OLM) documentation.
