BINARY_NAME := worksome
MODULE := github.com/worksome/worksome-cli
SCHEMA := schema/schema.graphql
OVERRIDES := schema/overrides.yaml
GENERATED_DIR := internal/generated
PLATFORM_SCHEMA ?= $(HOME)/Projects/platform/_schema_dump.graphql
INTROSPECT_ENDPOINT ?= https://api.worksome.com/graphql

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
LDFLAGS := -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT)"

.PHONY: build test lint generate sync-schema sync clean verify-generated help

## build: Build the CLI binary
build:
	go build $(LDFLAGS) -o $(BINARY_NAME) ./cmd/worksome/

## install: Install the CLI binary to $GOPATH/bin
install:
	go install $(LDFLAGS) ./cmd/worksome/

## test: Run all unit tests
test:
	go test ./internal/... -v -count=1

## test-short: Run tests without verbose output
test-short:
	go test ./internal/... -count=1

## test-integration: Run integration tests (requires WORKSOME_API_TOKEN)
test-integration:
	WORKSOME_INTEGRATION_TEST=1 go test ./test/integration/... -v -count=1

## lint: Run linter
lint:
	golangci-lint run ./...

## generate: Generate Go code from GraphQL schema
generate:
	go run ./cmd/generate/ -schema $(SCHEMA) -overrides $(OVERRIDES) -output $(GENERATED_DIR) -module $(MODULE)

## sync-schema: Sync the GraphQL schema
sync-schema:
ifeq ($(SYNC_MODE),platform)
	@if [ -f "$(PLATFORM_SCHEMA)" ]; then \
		echo "Syncing schema from platform repo..."; \
		cp "$(PLATFORM_SCHEMA)" "$(SCHEMA)"; \
		echo "Schema synced successfully."; \
	else \
		echo "Error: Platform schema not found at $(PLATFORM_SCHEMA)"; \
		exit 1; \
	fi
else
	@echo "Syncing schema via introspection..."
	@go run ./cmd/introspect/ --endpoint $(INTROSPECT_ENDPOINT) --token "$${WORKSOME_API_TOKEN}" > $(SCHEMA)
endif

## sync: Sync schema and regenerate code
sync: sync-schema generate

## verify-generated: Verify generated code is up to date
verify-generated: generate
	@if git diff --quiet $(GENERATED_DIR); then \
		echo "Generated code is up to date."; \
	else \
		echo "ERROR: Generated code is out of date. Run 'make generate' and commit."; \
		git diff --stat $(GENERATED_DIR); \
		exit 1; \
	fi

## clean: Remove build artifacts
clean:
	rm -f $(BINARY_NAME)
	rm -f internal/generated/types/*.unformatted
	rm -f internal/generated/commands/*.unformatted

## help: Show this help
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //' | column -t -s ':'
