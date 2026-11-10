#!/usr/bin/env python3
"""Assemble a server bundle; no database or account files are included."""
import argparse
import gzip
import hashlib
import json
from pathlib import Path
import shutil
import tarfile
import tempfile

parser = argparse.ArgumentParser()
parser.add_argument('--build-root', type=Path, default=Path('.work/release-root'))
parser.add_argument('--output', type=Path, default=Path('dist'))
args = parser.parse_args()
root = Path(__file__).resolve().parent.parent
args.output.mkdir(parents=True, exist_ok=True)
name = 'mysql-8.4.13-linux-amd64-ubuntu24.tar.gz'
with tempfile.TemporaryDirectory(dir=root / '.work', prefix='release-bundle-') as temporary:
    stage = Path(temporary)
    (stage / 'bin').mkdir()
    shutil.copyfile(args.build_root / 'bin/mysqlmem', stage / 'bin/mysqlmem')
    (stage / 'bin/mysqlmem').chmod(0o755)
    shutil.copytree(root / '.work/mysql/share/charsets', stage / 'share/charsets')
    shutil.copytree(root / '.work/mysql/extra/icu/icudt77l', stage / 'lib/private/icudt77l')
    (stage / 'share/english').mkdir()
    shutil.copyfile(args.build_root / '.work/mysql-build/share/english/errmsg.sys', stage / 'share/english/errmsg.sys')
    licenses = stage / 'licenses'
    shutil.copytree(root / 'LICENSES', licenses)
    shutil.copyfile(root / '.work/mysql/LICENSE', licenses / 'MySQL-LICENSE.txt')
    shutil.copyfile(root / '.work/tools/go/LICENSE', licenses / 'Go-LICENSE.txt')
    for source in (args.build_root / 'server/native').iterdir():
        if source.is_file() and any(word in source.name.lower() for word in ['license', 'notice', 'provenance']):
            shutil.copyfile(source, licenses / source.name)
    shutil.copytree(args.build_root / 'extra-notices', licenses / 'system-license-texts')
    shutil.copyfile(root / 'THIRD_PARTY.md', licenses / 'THIRD_PARTY.md')
    shutil.copyfile(args.build_root / 'build-versions.txt', stage / 'BUILD-PACKAGES.txt')
    (stage / 'BUILD.json').write_text(json.dumps({
        'mysql': '8.4.13', 'mysql_commit': '8a180268d832300c09a375115dfa0ea77d83f1af',
        'bao_commit': '18c47c82d494c5a7fff7b4ecebd02a98c728d7a5',
        'build_os': 'Ubuntu 24.04 amd64', 'glibc': '2.39-0ubuntu8.9',
        'gcc': '13.3.0-6ubuntu2~24.04.1', 'openssl': '3.0.13-0ubuntu3.16 (dynamic)',
        'requires': ['glibc', 'libssl.so.3', 'libcrypto.so.3'],
        'source': 'mysql-in-go-v0.1.0-corresponding-source.tar.gz',
    }, indent=2) + '\n')
    with (args.output / name).open('wb') as raw, gzip.GzipFile(filename='', fileobj=raw, mode='wb', mtime=0) as compressed:
        with tarfile.open(fileobj=compressed, mode='w') as archive:
            for path in sorted(stage.rglob('*')):
                if path.is_dir():
                    continue
                if not path.is_file() or path.is_symlink():
                    raise RuntimeError(f'Unexpected bundle entry: {path}')
                info = archive.gettarinfo(str(path), arcname=str(path.relative_to(stage)))
                info.uid = info.gid = info.mtime = 0
                info.uname = info.gname = ''
                info.mode = 0o755 if path == stage / 'bin/mysqlmem' else 0o644
                with path.open('rb') as source:
                    archive.addfile(info, source)
output = args.output / name
checksum = hashlib.sha256(output.read_bytes()).hexdigest()
print(f'{checksum}  {output.name}')
manifest = {'version': 'v0.1.0', 'mysql': '8.4.13', 'assets': {'linux-amd64': {
    'url': f'https://github.com/masahitojp/mysql-in-go/releases/download/v0.1.0/{name}',
    'sha256': checksum,
}}}
(root / 'internal/serverbundle/manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
