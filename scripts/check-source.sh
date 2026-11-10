#!/usr/bin/env bash
# Lightweight checks that also work before native/ has been generated.
set -euo pipefail
cd "$(dirname "$0")/.."
go_bin=${1:-go}
gofmt_bin="$("$go_bin" env GOROOT)/bin/gofmt"
unformatted=$("$gofmt_bin" -l mysql_in_go.go server/main.go cmd mysql mysqltest examples internal)
if [[ -n "$unformatted" ]]; then
  printf 'Run gofmt on:\n%s\n' "$unformatted" >&2
  exit 1
fi
bash -n scripts/*.sh
python3 - <<'PY'
import ast
from pathlib import Path
for source in sorted(Path('scripts').glob('*.py')):
    ast.parse(source.read_text(), filename=str(source))
    print(f'Python syntax OK: {source}')
PY
mkdir -p .work/source-check
"$go_bin" mod verify
"$go_bin" build -buildvcs=false -o .work/source-check/selectone ./cmd/selectone
"$go_bin" build -buildvcs=false -o .work/source-check/sqlcheck ./cmd/sqlcheck
"$go_bin" vet -buildvcs=false . ./cmd/selectone ./cmd/sqlcheck ./cmd/example ./cmd/startuptime ./mysql ./mysqltest ./examples/... ./internal/...
"$go_bin" test -buildvcs=false . ./mysql ./mysqltest ./examples/... ./internal/...
