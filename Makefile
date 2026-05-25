VERSION ?= dev
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS := -X github.com/sqlrush/opendb/internal/version.Version=$(VERSION) \
           -X github.com/sqlrush/opendb/internal/version.GitCommit=$(GIT_COMMIT) \
           -X github.com/sqlrush/opendb/internal/version.BuildDate=$(BUILD_DATE)

# All DB drivers (Oracle + MySQL + PostgreSQL + OpenGauss + GaussDB).
# `full` is the default for any production build — partial tags are only for
# special-purpose binaries (e.g. embedded, single-DB customer drops).
TAGS_OPENDB := full
TAGS_DBAA   := full,dbaa
TAGS_GOLDEN ?= opengauss,gaussdb,dbaa
OPENDB_GOLDEN_REPORT ?= /private/tmp/opendb-golden-reports/model-matrix.md

PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

.PHONY: build build-dbaa build-dbaa-golden build-probe test golden-tier0 golden-tui golden-tui-live golden-db golden-models golden lint clean release release-dbaa release-probe install

build:
	go build -tags "$(TAGS_OPENDB)" -ldflags "$(LDFLAGS)" -o bin/opendb ./cmd/opendb

build-dbaa:
	go build -tags "$(TAGS_DBAA)" -ldflags "$(LDFLAGS)" -o bin/dbaa ./cmd/opendb

build-dbaa-golden:
	go build -tags "$(TAGS_GOLDEN)" -ldflags "$(LDFLAGS)" -o bin/dbaa-golden ./cmd/opendb

build-probe:
	go build -ldflags "$(LDFLAGS)" -o bin/gaussdb-probe ./cmd/gaussdb-probe

test:
	go test -tags "$(TAGS_OPENDB)" ./... -race -cover

golden-tier0:
	go test -tags "$(TAGS_GOLDEN)" ./internal/opengauss/... ./internal/gaussdb ./internal/diagtrace ./internal/ui ./internal/skill/builtin/shared ./internal/skill/external ./internal/engine/profile ./internal/engine/memory ./internal/trace ./internal/drone

golden-tui:
	go test -tags "$(TAGS_GOLDEN)" ./internal/ui ./internal/ui/uitest -run "TestTier3|TestRenderCodeBlock_WrapsLongSQLWithoutEllipsis" -count=1

golden-tui-live: build-dbaa-golden
	OPENDB_BIN=$(CURDIR)/bin/dbaa-golden OPENDB_GOLDEN_TUI=1 go test -tags "$(TAGS_GOLDEN)" ./internal/ui/uitest -run TestTier3LivePTY -count=1

golden-db: build-dbaa-golden
	OPENDB_GOLDEN_BIN=$(CURDIR)/bin/dbaa-golden go test -tags "$(TAGS_GOLDEN)" ./internal/opengauss/golden -run TestTier1DBGoldenCases -count=1

golden-models: build-dbaa-golden
	mkdir -p $(dir $(OPENDB_GOLDEN_REPORT))
	OPENDB_GOLDEN_BIN=$(CURDIR)/bin/dbaa-golden OPENDB_GOLDEN_REPORT=$(OPENDB_GOLDEN_REPORT) go test -tags "$(TAGS_GOLDEN)" ./internal/opengauss/golden -run TestTier2ModelGoldenMatrix -count=1 -v

golden: golden-tier0 golden-tui

lint:
	golangci-lint run --build-tags "$(TAGS_OPENDB)" ./...

clean:
	rm -rf bin/

release: clean
	@for platform in $(PLATFORMS); do \
		GOOS=$$(echo $$platform | cut -d/ -f1) \
		GOARCH=$$(echo $$platform | cut -d/ -f2) \
		go build -tags "$(TAGS_OPENDB)" -ldflags "$(LDFLAGS)" \
			-o bin/opendb-$$(echo $$platform | tr / -) ./cmd/opendb; \
	done
	@echo "OpenDB release binaries built in bin/"
	@ls -la bin/

release-dbaa: clean
	@for platform in $(PLATFORMS); do \
		GOOS=$$(echo $$platform | cut -d/ -f1) \
		GOARCH=$$(echo $$platform | cut -d/ -f2) \
		go build -tags "$(TAGS_DBAA)" -ldflags "$(LDFLAGS)" \
			-o bin/dbaa-$$(echo $$platform | tr / -) ./cmd/opendb; \
	done
	@echo "dbaa release binaries built in bin/"
	@ls -la bin/

release-probe: clean
	@for platform in $(PLATFORMS); do \
		GOOS=$$(echo $$platform | cut -d/ -f1) \
		GOARCH=$$(echo $$platform | cut -d/ -f2) \
		go build -ldflags "$(LDFLAGS)" \
			-o bin/gaussdb-probe-$$(echo $$platform | tr / -) ./cmd/gaussdb-probe; \
	done
	@echo "gaussdb-probe binaries built in bin/"
	@ls -la bin/

install:
	go install -tags "$(TAGS_OPENDB)" -ldflags "$(LDFLAGS)" ./cmd/opendb
