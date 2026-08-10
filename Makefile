.PHONY: build test test-integration lint coverage proto ci clean run help fmt tidy docker-build dev release-snapshot

GO ?= go
PROTOC ?= $(shell which protoc 2>/dev/null || echo ~/.local/bin/protoc)
PROTOC_GEN_GO ?= $(shell which protoc-gen-go 2>/dev/null || echo ~/go/bin/protoc-gen-go)
PROTOC_GEN_GO_GRPC ?= $(shell which protoc-gen-go-grpc 2>/dev/null || echo ~/go/bin/protoc-gen-go-grpc)
MODULE_DIR ?= ../modules
CORE_PKGS ?= ./internal/... ./pkg/... ./cmd/...
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "0.0.0-dev")
LDFLAGS ?= -s -w -X github.com/Muxcore-Media/core/internal/version.Version=$(VERSION)

build:
	$(GO) build -ldflags="$(LDFLAGS)" -o muxcored ./cmd/muxcored

build-watchdog:
	$(GO) build -ldflags="$(LDFLAGS)" -o muxcore-watchdog ./cmd/muxcore-watchdog

build-modules:
	@for d in $(MODULE_DIR)/*/; do \
		if [ -f "$$d/go.mod" ]; then \
			echo "=== building $$d ==="; \
			cd "$$d" && $(GO) build ./... || exit 1; \
			cd - > /dev/null; \
		fi; \
	done

test:
	$(GO) test -race -count=1 -timeout 60s $(CORE_PKGS)

test-integration:
	$(GO) test -tags=integration -race -count=1 -timeout 120s ./internal/integration/...

test-modules:
	@for d in $(MODULE_DIR)/*/; do \
		if [ -f "$$d/go.mod" ]; then \
			echo "=== testing $$d ==="; \
			cd "$$d" && $(GO) test -race -count=1 -timeout 60s ./... || exit 1; \
			cd - > /dev/null; \
		fi; \
	done

test-all: test test-modules

GOLANGCI_LINT ?= $(shell which golangci-lint 2>/dev/null)

lint:
	@if [ -n "$(GOLANGCI_LINT)" ]; then \
		$(GOLANGCI_LINT) run --timeout 120s $(CORE_PKGS); \
	else \
		$(GO) vet $(CORE_PKGS); \
	fi

lint-modules:
	@for d in $(MODULE_DIR)/*/; do \
		if [ -f "$$d/go.mod" ]; then \
			echo "=== linting $$d ==="; \
			cd "$$d" && (which golangci-lint >/dev/null 2>&1 && golangci-lint run --timeout 60s ./... || $(GO) vet ./...); \
			cd - > /dev/null; \
		fi; \
	done

lint-all: lint lint-modules

coverage:
	$(GO) test -race -count=1 -coverprofile=coverage.out -covermode=atomic $(CORE_PKGS)
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "coverage report: coverage.html"

proto:
	PATH="$$HOME/go/bin:$$PATH" $(PROTOC) \
		--proto_path=proto \
		--go_out=proto/gen --go_opt=paths=source_relative \
		--go-grpc_out=proto/gen --go-grpc_opt=paths=source_relative \
		proto/muxcore/mesh/v1/*.proto \
		proto/muxcore/health/v1/*.proto \
		proto/muxcore/events/v1/*.proto \
		proto/muxcore/discovery/v1/*.proto \
		proto/muxcore/storage/v1/*.proto \
		proto/muxcore/module/v1/*.proto \
		proto/muxcore/spool/v1/*.proto \
		proto/muxcore/lifecycle/v1/*.proto \
		proto/muxcore/audit/v1/*.proto \
		proto/muxcore/auth/v1/*.proto \
		proto/muxcore/policy/v1/*.proto \
		proto/muxcore/cache/v1/*.proto \
		proto/muxcore/serialization/v1/*.proto \
		proto/muxcore/database/v1/*.proto \
		proto/muxcore/healthmonitor/v1/*.proto \
		proto/muxcore/tracing/v1/*.proto \
		proto/muxcore/logging/v1/*.proto \
		proto/muxcore/secrets/v1/*.proto \
		proto/muxcore/distributedlock/v1/*.proto \
		proto/muxcore/circuitbreaker/v1/*.proto \
		proto/muxcore/dataredaction/v1/*.proto \
		proto/muxcore/spoolresolver/v1/*.proto \
		proto/muxcore/workflow/v1/*.proto \
		proto/muxcore/encryption/v1/*.proto \
		proto/muxcore/featureflags/v1/*.proto \
		proto/muxcore/metrics/v1/*.proto \
		proto/muxcore/ratelimit/v1/*.proto \
		proto/muxcore/configwatcher/v1/*.proto

ci: lint-all test-all build build-modules

run: build
	./muxcored

clean:
	rm -f muxcored muxcore-watchdog coverage.out coverage.html

fmt:
	$(GO) fmt ./...

tidy:
	$(GO) mod tidy

docker-build:
	docker build -t muxcore:dev .

# Local cross-build preview (linux/darwin × amd64/arm64). Tag releases use GoReleaser on self-hosted CI.
release-snapshot:
	goreleaser release --snapshot --clean

dev:
	docker-compose up -d

.PHONY: hooks
hooks:
	git config core.hooksPath .githooks
	@echo "Git hooks installed (path: .githooks)"

help:
	@echo "MuxCore build targets:"
	@echo "  build             Build the muxcored binary"
	@echo "  test              Run unit tests with race detection"
	@echo "  test-integration  Run integration tests (spins up real subsystems)"
	@echo "  lint         Run golangci-lint (or go vet if not installed)"
	@echo "  coverage     Generate HTML coverage report"
	@echo "  proto        Regenerate protobuf Go code"
	@echo "  ci           Full CI pipeline (lint + test + build)"
	@echo "  run          Build and run locally"
	@echo "  fmt          Run go fmt ./..."
	@echo "  tidy         Run go mod tidy"
	@echo "  docker-build Build Docker image (muxcore:dev)"
	@echo "  release-snapshot  GoReleaser snapshot (no publish)"
	@echo "  dev          Start development services via docker-compose"
	@echo "  clean        Remove build artifacts"
