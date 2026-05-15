.PHONY: build test lint coverage proto ci clean run

GO ?= go
# Note: golangci-lint currently incompatible (requires go1.25 build, project uses go1.26).
# Use go vet + staticcheck directly for now.
PROTOC ?= $(shell which protoc 2>/dev/null || echo ~/.local/bin/protoc)
PROTOC_GEN_GO ?= $(shell which protoc-gen-go 2>/dev/null || echo ~/go/bin/protoc-gen-go)
PROTOC_GEN_GO_GRPC ?= $(shell which protoc-gen-go-grpc 2>/dev/null || echo ~/go/bin/protoc-gen-go-grpc)
MODULE_DIR ?= ../modules
CORE_PKGS ?= ./internal/... ./pkg/... ./cmd/...

build:
	$(GO) build -tags default -ldflags="-s -w" -o muxcored ./cmd/muxcored

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

test-modules:
	@for d in $(MODULE_DIR)/*/; do \
		if [ -f "$$d/go.mod" ]; then \
			echo "=== testing $$d ==="; \
			cd "$$d" && $(GO) test -race -count=1 -timeout 60s ./... || exit 1; \
			cd - > /dev/null; \
		fi; \
	done

test-all: test test-modules

lint:
	$(GO) vet $(CORE_PKGS)

lint-modules:
	@for d in $(MODULE_DIR)/*/; do \
		if [ -f "$$d/go.mod" ]; then \
			echo "=== vetting $$d ==="; \
			cd "$$d" && $(GO) vet ./... || exit 1; \
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
		proto/muxcore/discovery/v1/*.proto

ci: lint-all test-all build build-modules

run: build
	./muxcored

clean:
	rm -f muxcored coverage.out coverage.html
	rm -rf proto/gen
