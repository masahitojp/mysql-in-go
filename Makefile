GO ?= $(CURDIR)/.work/tools/go/bin/go
export GOCACHE := $(CURDIR)/.work/go-cache
export GOPATH := $(CURDIR)/.work/go-path
export GOTOOLCHAIN := local
export CGO_ENABLED := 0
export M4 := $(CURDIR)/.work/sysroot/usr/bin/m4
export BISON_PKGDATADIR := $(CURDIR)/.work/sysroot/usr/share/bison
export PATH := $(CURDIR)/.work/sysroot/usr/bin:$(PATH)
JOBS ?= 10

.PHONY: bootstrap configure mysql package build fmt vet test test-sql check-source source-archive
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
fmt:
	$(GO) fmt ./cmd/...
vet:
	$(GO) vet -buildvcs=false ./cmd/...

test:
	python3 scripts/probe.py

test-sql:
	python3 scripts/sql-suite.py

check-source:
	./scripts/check-source.sh "$(GO)"
source-archive:
	python3 scripts/source-archive.py
