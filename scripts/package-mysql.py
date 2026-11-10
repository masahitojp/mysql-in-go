#!/usr/bin/env python3
"""Build the Bao package from CMake's server archive list (Linux/amd64)."""
import json
from pathlib import Path
import shlex
import shutil
import subprocess
import tempfile

root = Path(__file__).resolve().parent.parent
work = root / '.work'
build = work / 'mysql-build'

def run(args, **kwargs):
    subprocess.run([str(arg) for arg in args], check=True, **kwargs)

def compiler_file(name):
    path = subprocess.check_output(['g++', f'-print-file-name={name}'], text=True).strip()
    if not Path(path).is_file():
        raise RuntimeError(f'Missing compiler runtime: {name}')
    return path

commands = subprocess.check_output(['ninja', '-C', str(build), '-t', 'commands', 'mysqld'], text=True)
link = shlex.split(commands.splitlines()[-1])
archives = list(dict.fromkeys(arg for arg in link if arg.endswith('.a')))
if not archives:
    raise SystemExit('No server archives found in the CMake link command')
entry = work / 'server_entry.o'
server = work / 'mysql-server.o'
runtime = work / 'mysql-runtime-unmerged.o'
aligned = work / 'mysql-aligned.o'
clean = work / 'mysql-aligned-clean.o'
run(['g++', '-fPIC', '-O1', '-ftls-model=global-dynamic', '-mtls-dialect=gnu', '-c', root / 'scripts/server_entry.cc', '-o', entry])
run(['ld', '-r', '--gc-sections', '-u', 'mysqlmem_server_main', '-o', server, entry, '--start-group', *archives, '--end-group'], cwd=build)
run(['ld', '-r', '-o', runtime, server, '--start-group', *[compiler_file(name) for name in ['libstdc++.a', 'libgcc_eh.a', 'libgcc.a', 'libc_nonshared.a']], '--end-group'])
# Compact section indices and avoid overflowing Go's findfunc table. The BSS
# start marker is used by MySQL's diagnostic stacktrace code.
run(['ld', '-r', '--force-group-allocation', '-T', root / 'scripts/merge-sections.ld', '-o', aligned, runtime])
# GNU property notes from distro runtime archives are unsupported by Bao.
run(['objcopy', '--remove-section=.note.gnu.property', aligned, clean])
stage = Path(tempfile.mkdtemp(prefix='package-', dir=work))
out = stage / 'native'
run([work / 'tools/bao', 'build', '-v', '-target', 'linux/amd64', '-sysroot', '/', '-bindings', root / 'signatures.json', '-linux-system-library', 'libssl.so.3', '-linux-system-library', 'libcrypto.so.3', '-link-notice', work / 'mysql/LICENSE', '-link-notice', f'/usr/share/doc/gcc-{subprocess.check_output(["g++", "-dumpversion"], text=True).strip().split(".")[0]}-base/copyright', '-link-notice', '/usr/share/doc/libc6/copyright', '-o', out, clean], cwd=root)
# Unversioned ELF imports bind the old glibc condition-variable ABI. Ask Go's
# internal linker for the current default ABI explicitly (all six operations).
versions = {f'pthread_cond_{name}': 'GLIBC_2.3.2' for name in ['init', 'destroy', 'wait', 'timedwait', 'signal', 'broadcast']}
source = out / 'symbols_linux_amd64.go'
text = source.read_text()
for name, version in versions.items():
    old = f' {name} "libc.so.6"'
    if text.count(old) != 1:
        raise RuntimeError(f'Expected exactly one dynamic import for {name}')
    text = text.replace(old, f' {name}#{version} "libc.so.6"')
source.write_text(text)
(out / 'mysqlmem-glibc-versions.json').write_text(json.dumps(versions, indent=2) + '\n')
destination = root / 'server/native'
if destination.exists():
    shutil.move(destination, stage / 'previous-native')
shutil.move(out, destination)
print(f'Published {destination}; intermediate artifacts: {stage}')
