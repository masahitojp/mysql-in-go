#!/usr/bin/env python3
"""Run SQL assertions, restart the Bao server, and check persisted data."""
import os
from pathlib import Path
import subprocess
import tempfile
import time

root = Path(__file__).resolve().parent.parent
run = Path(tempfile.mkdtemp(prefix='sql-', dir=root / '.work'))
socket = run / 'mysql.sock'
env = {**os.environ, 'MYSQL_DSN': f'root@unix({socket})/'}
common = [str(root / 'bin/mysqlmem'), '--no-defaults',
          f'--basedir={root / ".work/mysql-build"}', f'--datadir={run / "data"}',
          f'--lc-messages-dir={root / ".work/mysql-build/share"}',
          f'--character-sets-dir={root / ".work/mysql/share/charsets"}']
print(f'Artifacts: {run}', flush=True)
with (run / 'initialize.log').open('w') as log:
    subprocess.run([*common, '--initialize-insecure'], stdout=log, stderr=subprocess.STDOUT,
                   timeout=180, check=True)
for phase in ['setup', 'verify']:
    with (run / f'server-{phase}.log').open('w') as log:
        server = subprocess.Popen([*common, f'--socket={socket}',
                                   f'--pid-file={run / "mysqld.pid"}', '--skip-networking',
                                   '--innodb-buffer-pool-size=64M'], stdout=log, stderr=subprocess.STDOUT)
        try:
            deadline = time.monotonic() + 90
            while time.monotonic() < deadline:
                if server.poll() is not None:
                    raise RuntimeError(f'Server exited with {server.returncode}; see {run}')
                if socket.exists():
                    ready = subprocess.run([str(root / 'bin/selectone')], env=env,
                                           capture_output=True, text=True, timeout=5)
                    if ready.returncode == 0:
                        break
                time.sleep(0.25)
            else:
                raise RuntimeError(f'Server readiness timeout; see {run}')
            # Execute mutating assertions exactly once; a SQL failure is not retried.
            result = subprocess.run([str(root / 'bin/sqlcheck'), '-phase', phase], env=env,
                                    capture_output=True, text=True, timeout=90)
            (run / f'{phase}.txt').write_text(result.stdout + result.stderr)
            print(result.stdout, end='', flush=True)
            if result.returncode:
                raise RuntimeError(f'SQL {phase} failed: {result.stderr}; see {run}')
            server.wait(timeout=45)
            if server.returncode != 0:
                raise RuntimeError(f'Server shutdown status {server.returncode}; see {run}')
        finally:
            if server.poll() is None:
                server.terminate()
                try:
                    server.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    server.kill()
                    server.wait()
print('PASS both server runs shut down cleanly (exit 0)', flush=True)
