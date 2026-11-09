#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
root=$PWD
export PATH="$root/.work/sysroot/usr/bin:$PATH"
export BISON_PKGDATADIR="$root/.work/sysroot/usr/share/bison"
cmake -S .work/mysql -B .work/mysql-build -G Ninja \
  -DCMAKE_BUILD_TYPE=Release -DCMAKE_INSTALL_PREFIX="$root/.work/mysql-install" \
  -DCMAKE_POSITION_INDEPENDENT_CODE=ON \
  -DCMAKE_C_FLAGS="-O1 -fPIC -ftls-model=global-dynamic -mtls-dialect=gnu -I$root/.work/openssl3/usr/include/x86_64-linux-gnu" \
  -DCMAKE_CXX_FLAGS="-O1 -fPIC -ftls-model=global-dynamic -mtls-dialect=gnu -I$root/.work/openssl3/usr/include/x86_64-linux-gnu" \
  -DCMAKE_C_FLAGS_RELEASE='-O1 -DNDEBUG' -DCMAKE_CXX_FLAGS_RELEASE='-O1 -DNDEBUG' \
  -DWITH_SSL=system \
  -DOPENSSL_SSL_LIBRARY=/usr/lib/x86_64-linux-gnu/libssl.so.3 \
  -DOPENSSL_CRYPTO_LIBRARY=/usr/lib/x86_64-linux-gnu/libcrypto.so.3 \
  -DOPENSSL_INCLUDE_DIR="$root/.work/openssl3/usr/include" \
  -DOPENSSL_LIBRARY=/usr/lib/x86_64-linux-gnu/libssl.so.3 \
  -DCRYPTO_LIBRARY=/usr/lib/x86_64-linux-gnu/libcrypto.so.3 \
  -DWITH_UNIT_TESTS=OFF -DWITH_ROUTER=OFF -DWITH_MYSQLX=OFF \
  -DWITH_NDB=OFF -DWITH_NDBCLUSTER=OFF -DWITH_NDBCLUSTER_STORAGE_ENGINE=OFF -DWITH_DEBUG=OFF \
  -DWITH_LTO=OFF -DWITH_SYSTEMD=OFF -DWITH_AUTHENTICATION_LDAP=OFF \
  -DWITH_AUTHENTICATION_KERBEROS=OFF -DWITH_AUTHENTICATION_FIDO=OFF \
  -DWITH_AUTHENTICATION_WEBAUTHN=OFF -DWITH_KEYRING_TEST=OFF \
  -DWITH_LIBEVENT=bundled -DWITH_LZ4=bundled -DWITH_ZSTD=bundled \
  -DWITH_TIRPC=bundled -Dprotobuf_BUILD_SHARED_LIBS=OFF -DWITH_PROTOBUF=bundled -DWITH_ICU=bundled \
  -DWITH_EDITLINE=bundled -DFORCE_INSOURCE_BUILD=OFF "$@"
