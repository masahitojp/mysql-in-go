#!/usr/bin/env bash
# Run inside the same Ubuntu builder image used for the server.
set -euo pipefail
sed -i 's/^Types: deb$/Types: deb-src/; s/^Components:.*/Components: main/' /etc/apt/sources.list.d/ubuntu.sources
apt-get update -qq
mkdir -p /src/.work/license-sources
cd /src/.work/license-sources
gcc_version=$(dpkg-query -W -f='${Version}' gcc-13)
glibc_version=$(dpkg-query -W -f='${Version}' libc6-dev)
apt-get source --download-only "gcc-13=$gcc_version" "glibc=$glibc_version"
