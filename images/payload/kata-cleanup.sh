#!/bin/bash

set -e

DIR="${1:-/opt/kata-install}"
cd $DIR


echo "====================================================="
echo "Uninstall all kata-packages"
echo "====================================================="
rpm-ostree uninstall --idempotent --all
if rpm-ostree override reset -a; then
	echo "rpm-ostree override reset -a failed"
fi

echo "====================================================="
echo "removing files in install directory"
echo "====================================================="
rm -rf /usr/local/kata* /opt/kata-*

echo "====================================================="
echo "reboot node"
echo "====================================================="
systemctl reboot
