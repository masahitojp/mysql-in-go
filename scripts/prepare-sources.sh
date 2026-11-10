#!/usr/bin/env bash
# Source/tool preparation only; native build dependencies live in the container.
set -euo pipefail
cd "$(dirname "$0")/.."
root=$PWD
mkdir -p .work/tools
if [[ ! -x .work/tools/go/bin/go ]]; then
  curl -fL https://go.dev/dl/go1.26.10.linux-amd64.tar.gz -o .work/tools/go.tar.gz
  echo 'ccfe7ae822d2c2f41f4fe7c956199967a4dac22d0ee0d2b2bfdb440aab3b20c5  .work/tools/go.tar.gz' | sha256sum -c -
  tar -xzf .work/tools/go.tar.gz -C .work/tools
fi
if [[ ! -d .work/bao ]]; then
  git clone https://github.com/moriyoshi/bao.git .work/bao
  git -C .work/bao checkout 18c47c82d494c5a7fff7b4ecebd02a98c728d7a5
fi
[[ $(git -C .work/bao rev-parse HEAD) == 18c47c82d494c5a7fff7b4ecebd02a98c728d7a5 ]]
if git -C .work/bao apply --check "$root/scripts/bao-gcc15.patch" 2>/dev/null; then
  git -C .work/bao apply "$root/scripts/bao-gcc15.patch"
else
  git -C .work/bao apply --reverse --check "$root/scripts/bao-gcc15.patch"
fi
CGO_ENABLED=0 GOCACHE="$root/.work/go-cache" GOPATH="$root/.work/go-path" GOTOOLCHAIN=local \
  .work/tools/go/bin/go build -C .work/bao -o ../tools/bao ./cmd/bao
if [[ ! -d .work/mysql ]]; then
  git clone --depth 1 --branch mysql-8.4.13 https://github.com/mysql/mysql-server.git .work/mysql
fi
[[ $(git -C .work/mysql rev-parse HEAD) == 8a180268d832300c09a375115dfa0ea77d83f1af ]]
if git -C .work/mysql apply --check "$root/scripts/mysql-bao.patch" 2>/dev/null; then
  git -C .work/mysql apply "$root/scripts/mysql-bao.patch"
else
  git -C .work/mysql apply --reverse --check "$root/scripts/mysql-bao.patch"
fi
