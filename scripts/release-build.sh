#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
root=$PWD
./scripts/prepare-sources.sh
python3 scripts/stage-release.py
podman build -t localhost/mysql-in-go-builder:ubuntu24 -f build/ubuntu24.Dockerfile build
podman run --rm --name mysql-in-go-noble-build \
  -v "$root/.work/release-root:/src:rw" \
  -v "$root/.work/mysql:/src/.work/mysql:ro" \
  -v "$root/.work/tools:/src/.work/tools:ro" \
  -e MYSQL_SYSTEM_OPENSSL=1 localhost/mysql-in-go-builder:ubuntu24 bash -c '
    set -e
    ./scripts/configure-mysql.sh
    cmake --build .work/mysql-build --target mysqld -j 10
    mkdir -p extra-notices
    cp /usr/share/common-licenses/GPL-3 /usr/share/common-licenses/LGPL-2.1 \
      /usr/share/common-licenses/LGPL-3 /usr/share/common-licenses/GFDL-1.3 extra-notices/
    dpkg-query -W gcc-13 g++-13 libc6-dev libssl-dev > build-versions.txt
    python3 scripts/package-mysql.py
    CGO_ENABLED=0 GOCACHE=/src/.work/go-cache GOPATH=/src/.work/go-path \
      .work/tools/go/bin/go -C server build -buildvcs=false -o ../bin/mysqlmem .
  '
python3 scripts/release-bundle.py
