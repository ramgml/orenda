SHELL := /bin/bash
GO    := go
NPM   := npm

# air (live-reload) — may live in GOPATH/bin which is not always in PATH
AIR   := $(shell command -v air 2>/dev/null || echo "$(shell go env GOPATH)/bin/air")

BIN_DIR    := bin
BINARY     := $(BIN_DIR)/orenda
DATA_DIR   := data
CONFIG     := $(DATA_DIR)/config.yaml
DB_PATH    := $(DATA_DIR)/orenda.db

# Frontend
WEB_DIR    := web
WEB_DIST   := $(WEB_DIR)/dist

# Version
VERSION    := $(shell git describe --tags --always --dirty 2>/dev/null || echo "0.1.0")
LDFLAGS    := -ldflags "-s -w -X main.version=$(VERSION)"

.PHONY: all dev build test lint clean migrate-up migrate-down \
        backup backup-push backup-snapshot backup-status \
        web-install web-dev web-build run version help

all: build

## help: Show available targets
help:
	@grep -E '^##' Makefile | sed -E 's/## ?//'

## dev: Run Go (with air) + Vite dev-server (recommended for development)
dev:
	@command -v $(AIR) >/dev/null 2>&1 || $(GO) install github.com/air-verse/air@latest
	@if [ ! -d "$(WEB_DIR)/node_modules" ]; then $(MAKE) web-install; fi
	@trap 'kill 0' EXIT; \
	  (cd $(WEB_DIR) && $(NPM) run dev) & \
	  $(AIR)

## build: Build production binary with embedded web/dist
build: web-build
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o $(BINARY) ./cmd/orenda
	@echo "Built $(BINARY)"

## run: Run the production binary
run: build
	./$(BINARY) serve --config $(CONFIG)

## test: Run all tests
test:
	$(GO) test ./... -race -count=1

## lint: Run linters (golangci-lint + eslint)
lint:
	@command -v golangci-lint >/dev/null 2>&1 || echo "install: https://golangci-lint.run/usage/install/"
	golangci-lint run ./...
	cd $(WEB_DIR) && $(NPM) run lint

## clean: Remove build artifacts
clean:
	rm -rf $(BIN_DIR) $(WEB_DIST)
	$(GO) clean -cache -testcache

## migrate-up: Apply all pending migrations
migrate-up:
	./$(BINARY) migrate up --config $(CONFIG)

## migrate-down: Rollback last migration
migrate-down:
	./$(BINARY) migrate down --config $(CONFIG)

## backup: Manual backup (git push + sqlite snapshot)
backup: backup-push backup-snapshot
	@echo "Backup complete"

## backup-push: Force git push of mirror
backup-push:
	./$(BINARY) backup push --config $(CONFIG)

## backup-snapshot: Force sqlite snapshot
backup-snapshot:
	./$(BINARY) backup snapshot --config $(CONFIG)

## backup-status: Show backup status
backup-status:
	./$(BINARY) backup status --config $(CONFIG)

## web-install: Install npm dependencies
web-install:
	cd $(WEB_DIR) && $(NPM) install

## web-dev: Run Vite dev-server only (use with `make run` separately)
web-dev:
	cd $(WEB_DIR) && $(NPM) run dev

## web-build: Build React SPA
web-build:
	cd $(WEB_DIR) && $(NPM) run build

## version: Print version
version:
	@echo $(VERSION)