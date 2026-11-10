#!/usr/bin/env python3
"""Package the complete pinned sources, source packages and build instructions."""
import gzip
import hashlib
import io
from pathlib import Path
import tarfile

root = Path(__file__).resolve().parent.parent
output = root / 'dist/mysql-in-go-v0.1.0-corresponding-source.tar.gz'
packages = root / '.work/release-root/.work/license-sources'
if not list(packages.glob('gcc-13_*.dsc')) or not list(packages.glob('glibc_*.dsc')):
    raise SystemExit('First download the build-matching Ubuntu gcc-13 and glibc source packages.')

readme = '''# Corresponding source for mysql-in-go v0.1.0 / MySQL 8.4.13

This archive accompanies the MySQL-containing server binary. The root MIT
license applies only to the original wrapper, not the combined server.
MySQL's GPLv2 terms, additional permissions and third-party notices are in
upstream/mysql/LICENSE. Bao and its bootstrap notices are also preserved.

Contents:
- project-source.tar.gz: API, server entry point, patches and build scripts.
- upstream/mysql: MySQL commit 8a180268d832300c09a375115dfa0ea77d83f1af,
  with scripts/mysql-bao.patch applied (Abseil static linkage and TLS model).
- upstream/bao: Bao commit 18c47c82d494c5a7fff7b4ecebd02a98c728d7a5,
  with scripts/bao-gcc15.patch applied (weak GOT relocation handling).
- upstream/go: Go 1.26.10 runtime/library source and license.
- generated-native-source: generated bootstrap/binding sources and provenance
  (compiled .syso objects are recreated by the build scripts).
- ubuntu-source-packages: GCC 13 and glibc sources with Ubuntu patches.
  These cover the statically linked compiler runtime/libc_nonshared portions.
  libssl.so.3 and libcrypto.so.3 are OS-provided dynamic dependencies.

Build on Linux amd64 with Podman, curl, git, Python 3 and tar available:

  tar -xzf project-source.tar.gz
  cd mysqlmem
  make release-build

That command downloads the pinned Go toolchain and sources, applies the same
patches, builds in Ubuntu 24.04 with its gcc/g++13 and OpenSSL3.0 headers,
then creates the server and release bundle. It requires network access for
Ubuntu build dependencies and the pinned Go distribution. See
scripts/prepare-sources.sh, scripts/release-build.sh, scripts/configure-mysql.sh
and scripts/package-mysql.py for exact commands. The generated native code is
produced by Bao, not handwritten; the complete converter source is included.

To use the included source trees without cloning them again, unpack the project,
copy upstream/mysql and upstream/bao into project .work/mysql and .work/bao,
and run the body of release-build.sh after the prepare-sources.sh step. Build
Bao with the included Go version first:
  CGO_ENABLED=0 go build -C .work/bao -o ../tools/bao ./cmd/bao
The prepare-sources.sh identity checks intentionally require Git checkouts;
the included trees omit Git history, not source files.

The Ubuntu source packages are standard .dsc / .orig.tar / .debian.tar inputs;
extract with dpkg-source -x <package>.dsc. They also contain their own copyright
and license texts. Generated server/native and intermediates may be rebuilt by
replacing these runtime libraries and re-running package-mysql.py.
'''

# Only these MySQL files differ from upstream, and only by the included patch.
# Add a prominent modification/date notice to their source-distribution copy.
modified = {
 'extra/abseil/CMakeLists.txt': b'# Modified for mysql-in-go on 2026-11-10: build Abseil statically for Bao.\n',
 'extra/abseil/abseil-cpp-20230802.1/absl/base/attributes.h': b'// Modified for mysql-in-go on 2026-11-10: use global-dynamic TLS for Bao.\n',
}

def add_bytes(archive, name, data, mode=0o644):
    info = tarfile.TarInfo('mysql-in-go-source/' + name)
    info.size = len(data)
    info.mode = mode
    archive.addfile(info, io.BytesIO(data))

def add_tree(archive, source, destination, notices=None, skip_suffixes=()):
    for path in sorted(source.rglob('*')):
        rel = path.relative_to(source)
        if '.git' in rel.parts or '__pycache__' in rel.parts or path.suffix in skip_suffixes:
            continue
        if path.is_dir():
            continue
        name = 'mysql-in-go-source/' + destination + '/' + rel.as_posix()
        info = archive.gettarinfo(str(path), arcname=name)
        info.uid = info.gid = info.mtime = 0
        info.uname = info.gname = ''
        if path.is_symlink():
            # Upstream source symlinks are preserved; source archives are never
            # passed to the executable bundle extractor.
            archive.addfile(info)
        elif path.is_file():
            notice = (notices or {}).get(rel.as_posix())
            if notice:
                add_bytes(archive, destination + '/' + rel.as_posix(), notice + path.read_bytes())
            else:
                with path.open('rb') as stream:
                    archive.addfile(info, stream)
        else:
            raise RuntimeError(f'Unexpected source entry: {path}')

with output.open('wb') as raw, gzip.GzipFile(filename='', fileobj=raw, mode='wb', mtime=0, compresslevel=6) as compressed:
    with tarfile.open(fileobj=compressed, mode='w') as archive:
        add_bytes(archive, 'README.md', readme.encode())
        add_bytes(archive, 'project-source.tar.gz', (root / 'dist/mysqlmem-source.tar.gz').read_bytes())
        add_tree(archive, root / '.work/mysql', 'upstream/mysql', modified)
        add_tree(archive, root / '.work/bao', 'upstream/bao')
        add_tree(archive, root / '.work/tools/go/src', 'upstream/go/src')
        for name in ['LICENSE', 'PATENTS', 'VERSION']:
            path = root / '.work/tools/go' / name
            if path.exists():
                add_bytes(archive, 'upstream/go/' + name, path.read_bytes())
        add_tree(archive, packages, 'ubuntu-source-packages')
        add_tree(archive, root / '.work/release-root/server/native', 'generated-native-source', skip_suffixes=('.syso',))
        add_tree(archive, root / '.work/release-root/extra-notices', 'licenses')
print(f'{output}: {output.stat().st_size} bytes')
print(f'{hashlib.sha256(output.read_bytes()).hexdigest()}  {output.name}')
