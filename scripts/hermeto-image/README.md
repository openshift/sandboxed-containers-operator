This folder contains the files necessary to build a custom version of the hermeto
container.
The goal is to enable subscription as part of the hermeto run, so that RPMs
coming from RHEL can be fetched.
See entrypoint.sh for further details on how to use the resulting container.

The container is published to quay.io/repository/redhat-user-workloads/ose-osc-tenant/osc-hermeto
and is used by Makefiles in our different repositories to make local hermetic
builds, as close as possible to our Konflux CI, for troubleshooting.
