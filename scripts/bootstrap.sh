#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
if [[ $(uname -s) != Linux || $(uname -m) != x86_64 ]]; then
  echo 'This build currently supports Linux/amd64 only.' >&2
  exit 1
fi
for required in git curl python3 cmake ninja gcc g++ ld objcopy make apt dpkg-deb sha256sum tar; do
  command -v "$required" >/dev/null || { echo "Missing host tool: $required" >&2; exit 1; }
done
mkdir -p .work/tools .work/debs .work/sysroot .work/openssl3
if [[ ! -x .work/tools/go/bin/go ]]; then
  curl -fL https://go.dev/dl/go1.26.10.linux-amd64.tar.gz -o .work/tools/go.tar.gz
  echo 'ccfe7ae822d2c2f41f4fe7c956199967a4dac22d0ee0d2b2bfdb440aab3b20c5  .work/tools/go.tar.gz' | sha256sum -c -
  tar -xzf .work/tools/go.tar.gz -C .work/tools
fi
if [[ ! -d .work/bao ]]; then
  git clone https://github.com/moriyoshi/bao.git .work/bao
  git -C .work/bao checkout 18c47c82d494c5a7fff7b4ecebd02a98c728d7a5
fi
if [[ $(git -C .work/bao rev-parse HEAD) != 18c47c82d494c5a7fff7b4ecebd02a98c728d7a5 ]]; then
  echo 'Bao checkout does not match the pinned revision.' >&2
  exit 1
fi
if git -C .work/bao apply --check "$PWD/scripts/bao-gcc15.patch" 2>/dev/null; then
  git -C .work/bao apply "$PWD/scripts/bao-gcc15.patch"
else
  git -C .work/bao apply --reverse --check "$PWD/scripts/bao-gcc15.patch"
fi
GOCACHE="$PWD/.work/go-cache" GOTOOLCHAIN=local \
  .work/tools/go/bin/go build -C .work/bao -o ../tools/bao ./cmd/bao
if [[ ! -d .work/mysql ]]; then
  git clone --depth 1 --branch mysql-8.4.13 https://github.com/mysql/mysql-server.git .work/mysql
fi
if [[ $(git -C .work/mysql rev-parse HEAD) != 8a180268d832300c09a375115dfa0ea77d83f1af ]]; then
  echo 'MySQL checkout does not match the pinned revision.' >&2
  exit 1
fi
if git -C .work/mysql apply --check "$PWD/scripts/mysql-bao.patch" 2>/dev/null; then
  git -C .work/mysql apply "$PWD/scripts/mysql-bao.patch"
else
  git -C .work/mysql apply --reverse --check "$PWD/scripts/mysql-bao.patch"
fi
if [[ ! -x .work/sysroot/usr/bin/bison || ! -x .work/sysroot/usr/bin/rpcgen || ! -x .work/sysroot/usr/bin/m4 ]]; then
  (cd .work/debs && apt download bison=2:3.8.2+dfsg-1ubuntu1 rpcsvc-proto=1.4.4-1 m4=1.4.21-1)
  for pkg in .work/debs/bison_*.deb .work/debs/rpcsvc-proto_*.deb .work/debs/m4_*.deb; do
    dpkg-deb -x "$pkg" .work/sysroot
  done
fi
if [[ ! -f .work/openssl3/usr/include/openssl/ssl.h ]]; then
  curl -fL https://archive.ubuntu.com/ubuntu/pool/main/o/openssl/libssl-dev_3.5.5-1ubuntu3.7_amd64.deb -o .work/debs/openssl3-dev.deb
  dpkg-deb -x .work/debs/openssl3-dev.deb .work/openssl3
fi
