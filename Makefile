GO ?= $(CURDIR)/.work/tools/go/bin/go
export GOCACHE := $(CURDIR)/.work/go-cache
export GOPATH := $(CURDIR)/.work/go-path
export GOTOOLCHAIN := local
export CGO_ENABLED := 0
export M4 := $(CURDIR)/.work/sysroot/usr/bin/m4
export BISON_PKGDATADIR := $(CURDIR)/.work/sysroot/usr/share/bison
export PATH := $(CURDIR)/.work/sysroot/usr/bin:$(PATH)
JOBS ?= 10

.PHONY: bootstrap configure mysql package build fmt vet test test-sql check-source source-archive test-api example test-select-one timing compare-testcontainers test-fixture test-fixture-tmpfs test-lifecycle test-fixture-discard
bootstrap:
	./scripts/bootstrap.sh
configure:
	./scripts/configure-mysql.sh
mysql:
	cmake --build .work/mysql-build --target mysqld -j $(JOBS)
package:
	python3 scripts/package-mysql.py
build:
	$(GO) build -buildvcs=false -o bin/mysqlmem ./cmd/mysqlmem
	$(GO) build -buildvcs=false -o bin/selectone ./cmd/selectone
	$(GO) build -buildvcs=false -o bin/sqlcheck ./cmd/sqlcheck
	$(GO) build -buildvcs=false -o bin/example ./cmd/example
fmt:
	$(GO) fmt ./cmd/... ./mysql ./mysqltest ./examples/... ./internal/...
vet:
	$(GO) vet -buildvcs=false ./cmd/... ./mysql ./mysqltest ./examples/... ./internal/...

test:
	python3 scripts/probe.py

test-sql:
	python3 scripts/sql-suite.py

check-source:
	./scripts/check-source.sh "$(GO)"
source-archive:
	python3 scripts/source-archive.py

test-api:
	MYSQLMEM_INTEGRATION=1 $(GO) test -buildvcs=false -count=1 -v ./mysql
example:
	$(GO) run -buildvcs=false ./cmd/example

test-select-one:
	MYSQLMEM_INTEGRATION=1 $(GO) test -buildvcs=false -count=1 -v ./mysql -run '^TestSelectOne$$'

timing:
	$(GO) build -buildvcs=false -o bin/startuptime ./cmd/startuptime
	./bin/startuptime -runs 5

compare-testcontainers:
	$(GO) -C benchmarks/testcontainers build -buildvcs=false -o ../../bin/compare-testcontainers .
	TESTCONTAINERS_PROVIDER=podman ./bin/compare-testcontainers -root . -runs 5

test-fixture:
	MYSQLMEM_INTEGRATION=1 $(GO) test -buildvcs=false -count=1 -v ./mysqltest ./examples/fixture

test-fixture-tmpfs:
	MYSQLMEM_TMPFS=1 MYSQLMEM_INTEGRATION=1 $(GO) test -buildvcs=false -count=1 -v ./mysqltest ./examples/fixture ./mysql -run 'TestFixture|TestPrepareFailure|TestCloseActive|TestPreparedDatabase|TestTmpfs'

test-lifecycle: build
	MYSQLMEM_INTEGRATION=1 $(GO) test -buildvcs=false -count=1 -v ./mysql -run 'TestParentDeathStopsMySQLIntegration|TestIntegrationPersistentDataSurvivesRestart'
	MYSQLMEM_INTEGRATION=1 $(GO) test -buildvcs=false -count=1 -v ./mysqltest -run 'TestChildUUIDsAreUniqueIntegration|TestCopyExcludesOnlyRootAutoCNF'

test-fixture-discard:
	MYSQLMEM_DISCARD_CHILDREN=1 MYSQLMEM_TMPFS=1 MYSQLMEM_INTEGRATION=1 $(GO) test -buildvcs=false -count=1 -v ./mysqltest ./examples/fixture ./mysql -run 'TestDiscard|TestFixtureIntegration|TestPreparedDatabase'
