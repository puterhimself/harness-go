SHELL := /bin/sh

.DEFAULT_GOAL := help

GO ?= go
BIN ?= crush
ARGS ?=
GOFLAGS ?=
LDFLAGS ?=

.PHONY: help build run test fmt lint tidy install clean

help: ## Show available targets
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z0-9_.-]+:.*## / {printf "%-12s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build the crush binary
	$(GO) build $(GOFLAGS) $(if $(LDFLAGS),-ldflags "$(LDFLAGS)") -o $(BIN) .

run: build ## Build and run crush
	./$(BIN) $(ARGS)

test: ## Run all tests
	$(GO) test $(GOFLAGS) ./... $(ARGS)

fmt: ## Format Go code
	@if command -v gofumpt >/dev/null 2>&1; then \
		gofumpt -w .; \
	else \
		gofmt -w .; \
	fi

lint: ## Run golangci-lint if installed
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not installed"; \
		exit 1; \
	fi

tidy: ## Tidy Go module dependencies
	$(GO) mod tidy

install: ## Install crush
	$(GO) install $(GOFLAGS) $(if $(LDFLAGS),-ldflags "$(LDFLAGS)") .

clean: ## Remove built artifacts
	rm -f $(BIN) race.log
