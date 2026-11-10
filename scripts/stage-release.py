#!/usr/bin/env python3
"""Stage only build sources, without secrets or existing database files."""
from pathlib import Path
import shutil

root = Path(__file__).resolve().parent.parent
destination = root / '.work/release-root'
destination.mkdir(parents=True, exist_ok=True)
for name in ['server', 'mysql', 'mysqltest', 'internal', 'scripts', 'LICENSES', 'build']:
    shutil.copytree(root / name, destination / name, dirs_exist_ok=True,
                    ignore=shutil.ignore_patterns('native', '__pycache__'))
for name in ['go.mod', 'go.sum', 'mysql_in_go.go', 'signatures.json', 'LICENSE', 'THIRD_PARTY.md']:
    shutil.copyfile(root / name, destination / name)
for name in ['mysql', 'tools']:
    (destination / '.work' / name).mkdir(parents=True, exist_ok=True)
