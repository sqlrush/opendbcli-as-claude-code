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

PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

.PHONY: build build-dbaa build-probe test lint clean release release-dbaa release-probe install

build:
	go build -tags "$(TAGS_OPENDB)" -ldflags "$(LDFLAGS)" -o bin/opendb ./cmd/opendb

build-dbaa:
	go build -tags "$(TAGS_DBAA)" -ldflags "$(LDFLAGS)" -o bin/dbaa ./cmd/opendb

build-probe:
	go build -ldflags "$(LDFLAGS)" -o bin/gaussdb-probe ./cmd/gaussdb-probe

test:
	go test -tags "$(TAGS_OPENDB)" ./... -race -cover

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
