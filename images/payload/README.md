# kata-operator-payload

A container image used by kata-operator to install Kata and dependencies (like QEMU...) on worker nodes.
During container build (e.g., podman, docker, etc), the RPMs are downloaded and a local repo is created. The resulting container image includes the RPMs.
The kata-operat-daemon pulls down this image unpacks it and installs it on the node where it is running. 
