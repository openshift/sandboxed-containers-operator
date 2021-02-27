# Build
On RHEL
```
go build  -tags=containers_image_openpgp,exclude_graphdriver_btrfs  -o image/daemon cmd/daemon/main.go
```

On Fedora
```
sudo dnf install -y device-mapper-devel gpgme-devel btrfs-progs-devel
go build -o image/daemon cmd/daemon/main.go
```

Using Dockerfile
```
cd image
podman build -f Dockerfile.fedora -t kata-operator-daemon .
```
