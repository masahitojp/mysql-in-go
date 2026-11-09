#!/usr/bin/env python3
"""Package an explicit source allowlist; never traverse local build/data trees."""
import gzip
import hashlib
from pathlib import Path
import tarfile

root = Path(__file__).resolve().parent.parent
files = [root / name for name in [
    '.gitignore', 'AGENTS.md', 'LICENSE', 'THIRD_PARTY.md', 'README.md',
    'Makefile', 'go.mod', 'go.sum', 'signatures.json',
]]
for directory, suffixes in {
    'cmd': {'.go'}, 'scripts': {'.sh', '.py', '.cc', '.ld', '.patch'},
    'docs': {'.md'}, 'LICENSES': {'.txt'}, '.github/workflows': {'.yml'},
}.items():
    files.extend(path for path in (root / directory).rglob('*')
                 if path.is_file() and path.suffix in suffixes and '__pycache__' not in path.parts)
output = root / 'dist/mysqlmem-source.tar.gz'
output.parent.mkdir(exist_ok=True)
with output.open('wb') as raw, gzip.GzipFile(filename='', fileobj=raw, mode='wb', mtime=0) as compressed:
    with tarfile.open(fileobj=compressed, mode='w') as archive:
        for path in sorted(files):
            if path.is_symlink():
                raise RuntimeError(f'Refusing source symlink: {path}')
            info = archive.gettarinfo(str(path), arcname=str(Path('mysqlmem') / path.relative_to(root)))
            info.uid = info.gid = info.mtime = 0
            info.uname = info.gname = ''
            info.mode = 0o755 if path.suffix == '.sh' else 0o644
            with path.open('rb') as source:
                archive.addfile(info, source)
print(f'{output}: {len(files)} source files, {output.stat().st_size} bytes')
print(f'SHA256 {hashlib.sha256(output.read_bytes()).hexdigest()}')
