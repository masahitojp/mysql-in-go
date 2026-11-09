#!/usr/bin/env python3
"""Initialize a disposable, socket-only MySQL and query it from Go."""
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time

root = Path(__file__).resolve().parent.parent
binary = Path(sys.argv[1]).resolve() if len(sys.argv) > 1 else root / 'bin/mysqlmem'
probe = root / 'bin/selectone'
run = Path(tempfile.mkdtemp(prefix='probe-', dir=root / '.work'))
data = run / 'data'
# Keep the Unix-domain socket path short enough for sockaddr_un.
socket = run / 'mysql.sock'
common = [str(binary), '--no-defaults', f'--basedir={root / ".work/mysql-build"}',
          f'--datadir={data}', f'--lc-messages-dir={root / ".work/mysql-build/share"}',
          f'--character-sets-dir={root / ".work/mysql/share/charsets"}']
print(f'Artifacts: {run}', flush=True)
# Configuration and connection failures must remain nonzero failures.
base_env = {key: value for key, value in os.environ.items() if key != 'MYSQL_DSN'}
for env, expected in [(base_env, 'MYSQL_DSN is required'),
                      ({**base_env, 'MYSQL_DSN': f'root@unix({run / "missing.sock"})/'}, 'no such file or directory')]:
    negative = subprocess.run([str(probe)], env=env, capture_output=True, text=True, timeout=10)
    if negative.returncode == 0 or expected not in negative.stderr:
        raise RuntimeError(f'Unexpected negative-control result: {negative}')
with (run / 'initialize.log').open('w') as log:
    subprocess.run([*common, '--initialize-insecure'], stdout=log, stderr=subprocess.STDOUT,
                   timeout=180, check=True)
with (run / 'server.log').open('w') as log:
    server = subprocess.Popen([*common, f'--socket={socket}', f'--pid-file={run / "mysqld.pid"}',
                               '--skip-networking', '--innodb-buffer-pool-size=64M'],
                              stdout=log, stderr=subprocess.STDOUT)
    try:
        deadline = time.monotonic() + 90
        env = {**os.environ, 'MYSQL_DSN': f'root@unix({socket})/'}
        while time.monotonic() < deadline:
            if server.poll() is not None:
                raise RuntimeError(f'Server exited with {server.returncode}; see {run / "server.log"}')
            if socket.exists():
                result = subprocess.run([str(probe), "-shutdown"], env=env, capture_output=True, text=True, timeout=35)
                if result.returncode == 0:
                    print(result.stdout, end='', flush=True)
                    (run / 'result.txt').write_text(result.stdout)
                    server.wait(timeout=45)
                    break
            time.sleep(0.25)
        else:
            raise RuntimeError(f'Server not ready; see {run / "server.log"}')
    finally:
        if server.poll() is None:
            server.terminate()
            try:
                server.wait(timeout=45)
            except subprocess.TimeoutExpired:
                server.kill()
                server.wait()
    if server.returncode != 0:
        raise RuntimeError(f'Server shutdown status {server.returncode}; see {run / "server.log"}')
