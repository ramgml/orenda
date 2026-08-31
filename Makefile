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

# Where the Go embed.web.FS looks for files. `make build` copies web/dist
# into here right before `go build` so //go:embed all:dist picks up the
# SPA. The directory itself is committed (with .gitkeep) so the embed
# compiles in a fresh checkout.
EMBED_DIST := internal/embed/web/dist

# Version
VERSION    := $(shell git describe --tags --always --dirty 2>/dev/null || echo "0.1.0")
COMMIT     := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
BUILD_DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS    := -ldflags "-s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildDate=$(BUILD_DATE)"

.PHONY: all dev build test test-full lint lint-new clean migrate-up migrate-down \
        backup backup-push backup-snapshot backup-status \
        web-install web-dev web-build web-test web-typecheck test-e2e \
        embed-dists run version help govulncheck hooks \
        web-format web-format-check

all: build

## help: Show available targets
help:
	@grep -E '^##' Makefile | sed -E 's/## ?//'

## dev: Run Go (with air) + Vite dev-server (recommended for development)
##
## Phase 28.20: dev backend listens on :2138 by default — keeps it out of
## the way of the usage/dogfood systemd instance on :2137. Override with
## `make dev ORENDA_SERVER__PORT=2200` (or set the env var in your shell).
## Both air and Vite see the same ORENDA_SERVER__PORT — the proxy-target
## in web/vite.config.ts reads it from process.env.
dev:
	@command -v $(AIR) >/dev/null 2>&1 || $(GO) install github.com/air-verse/air@latest
	@if [ ! -d "$(WEB_DIR)/node_modules" ]; then $(MAKE) web-install; fi
	@trap 'kill 0' EXIT; \
	  export ORENDA_SERVER__PORT=$${ORENDA_SERVER__PORT:-2138}; \
	  echo "==> dev backend on :$$ORENDA_SERVER__PORT (override: make dev ORENDA_SERVER__PORT=...)"; \
	  (cd $(WEB_DIR) && $(NPM) run dev) & \
	  $(AIR)

## build: Build production binary with embedded web/dist
##
## Phase 27.1: web/dist is now embedded via `//go:embed all:dist` in
## internal/embed/web/embed.go (no build tag needed). The Makefile
## copies web/dist/* into internal/embed/web/dist/ right before `go build`
## so the SPA lands inside the binary. The dist/ directory ships with a
## .gitkeep, so a fresh checkout (no npm install / no npm run build) still
## compiles — the resulting FS is just empty and DistSubFS falls back to
## the on-disk web/dist/ during dev.
build: web-build embed-dists
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 $(GO) build $(LDFLAGS) -o $(BINARY) ./cmd/orenda
	@echo "Built $(BINARY)"

## embed-dists: copy the freshly built web/dist/* into the embed drop.
## Preserves .gitkeep so the directory stays under .gitignore tracking.
embed-dists:
	@mkdir -p $(EMBED_DIST)
	@# Keep .gitkeep (always), refresh everything else from web/dist.
	@touch $(EMBED_DIST)/.gitkeep
	@if [ -d "$(WEB_DIST)" ]; then \
		rsync -a --delete --exclude='.gitkeep' $(WEB_DIST)/ $(EMBED_DIST)/ ; \
		echo "Embedded $(WEB_DIST) → $(EMBED_DIST)" ; \
	else \
		echo "WARNING: $(WEB_DIST) not present; embed will be empty" ; \
	fi

## run: Run the production binary
run: build
	./$(BINARY) serve --config $(CONFIG)

## test: Run all tests (Go + vitest) — fast cached everyday run + pre-push gate.
## Phase 26.F: vitest is part of the pre-commit / pre-push gate now
## (no longer a separate "wave rule"). E2E stays separate — it requires
## Chromium and a built binary; see test-e2e.
##
## Go test cache is ENABLED (no `-count=1`): an unchanged tree re-runs
## in seconds; a push touching one package re-runs only that package and
## its dependents (the cache keys on file contents of the package and its
## dependency graph, plus env and flags — a real code change can never be
## served stale from cache). This is the local pre-push gate and the
## everyday developer test.
test:
	$(GO) test ./... -race
	cd $(WEB_DIR) && $(NPM) run test

## test-full: full uncached run — the CI backstop on push to dev and the
## release gate. Identical coverage to `test` (go test -race over ./...
## + vitest), but WITH `-count=1` to deliberately disable the Go test
## cache. This is the safety net for exotica the cache cannot see
## (ports, clocks).
test-full:
	$(GO) test ./... -race -count=1
	cd $(WEB_DIR) && $(NPM) run test

## lint: Run linters (golangci-lint + eslint)
lint:
	@command -v golangci-lint >/dev/null 2>&1 || echo "install: https://golangci-lint.run/usage/install/"
	golangci-lint run ./...
	cd $(WEB_DIR) && $(NPM) run lint

## lint-new: golangci-lint on NEW code only (--new-from-merge-base=origin/dev).
## wiki:ci-local-gates-hooks — mirrors the PR CI gate semantics locally so
## pre-existing debt (Phase 30.16) does not drown out new issues. ~8.5s
## warm on this repo. Used by the tracked pre-push hook.
##
## Override the base ref: `make lint-new BASE_REF=origin/main` (e.g. when
## preparing a release PR off main).
lint-new:
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "install: https://golangci-lint.run/usage/install/" >&2; exit 1; }
	@base="$${BASE_REF:-origin/dev}"; \
	if ! git rev-parse --verify --quiet "$$base" >/dev/null; then \
		echo "lint-new: $$base not local; fetching…" >&2; \
		git fetch --no-tags origin "$${base#origin/}" || { \
			echo "lint-new: fetch failed; aborting" >&2; exit 1; }; \
	fi; \
	echo "lint-new: golangci-lint run --new-from-merge-base=$$base ./..."; \
	golangci-lint run --new-from-merge-base="$$base" ./...

## hooks: Install tracked git hooks (scripts/git-hooks/) into core.hooksPath.
## wiki:ci-local-gates-hooks. Idempotent — safe to re-run. Writes
## core.hooksPath into the SHARED git config (the main checkout's .git/),
## so all existing and future worktrees inherit it automatically — no
## per-worktree install needed.
##
## Fresh clones need this once before the first commit/push.
hooks:
	@hooks="scripts/git-hooks"; \
	main_git="$$(git rev-parse --git-common-dir)"; \
	current="$$(git config --get core.hooksPath 2>/dev/null || true)"; \
	if [ "$$current" = "$$hooks" ]; then \
		echo "hooks: core.hooksPath already = $$hooks (no change)"; \
	else \
		echo "hooks: setting core.hooksPath = $$hooks in $$main_git/config"; \
		GIT_DIR="$$main_git" git config core.hooksPath "$$hooks"; \
	fi
	@echo "hooks: active — pre-commit (gofmt + prettier --check) and pre-push (make lint-new + make web-typecheck + make test)"
	@echo "hooks: bypass with SKIP_ORENDA_HOOKS=1 (avoid --no-verify — see AGENTS.md)"

## web-format: Format web/ sources with Prettier (writes in-place).
## Phase 28.7: prettier setup. We deliberately do NOT auto-format
## in `make lint` — that would create a giant mixed-style commit
## the first time someone runs the target. Operators run this
## explicitly when they're ready to absorb a formatter pass.
web-format:
	cd $(WEB_DIR) && $(NPM) run format

## web-format-check: Verify formatting (CI-style). Exits non-zero
## if any file would change — useful as a pre-push gate later.
web-format-check:
	cd $(WEB_DIR) && $(NPM) run format:check

## test-e2e: Playwright E2E smoke suite.
## Requires a built binary (make build). Spawns its own test server
## on ORENDA_SERVER__PORT=21371 (override to avoid clashing with
## the usage/dogfood instance on 2137 or `make dev` on 2138). The
## webServer.config in web/playwright.config.ts already points at 21371.
test-e2e: build
	cd $(WEB_DIR) && $(NPM) run test:e2e

## clean: Remove build artifacts
clean:
	rm -rf $(BIN_DIR) $(WEB_DIST)
	@# Restore the embed'd dist to its gitkeep-only state.
	@find $(EMBED_DIST) -mindepth 1 -not -name '.gitkeep' -delete 2>/dev/null || true
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

## web-build: Install npm dependencies (strict, lockfile-pinned) and build the SPA.
## `npm ci` is idempotent and fast when package-lock.json is unchanged;
## it guarantees that tsc/Vite see exactly the dependencies committed in
## package-lock.json, so a release that adds a new npm package can no
## longer break install.sh / update-dogfood.sh with "Cannot find module"
## (Task #26).
web-build:
	cd $(WEB_DIR) && $(NPM) ci
	cd $(WEB_DIR) && $(NPM) run build

## web-test: Run the vitest suite (component / unit / hook tests)
web-test:
	cd $(WEB_DIR) && $(NPM) run test

## web-typecheck: Run tsc --noEmit on the SPA.
## Task 44: catches TS errors locally before push; mirrors `web-test` style.
web-typecheck:
	cd $(WEB_DIR) && $(NPM) run typecheck

## version: Print version
version:
	@echo $(VERSION)

## govulncheck: Run Go's official vulnerability scanner against the
## module's dependencies. Phase 28.6 (polish) — `lint` was previously
## only ESLint + golangci-lint, neither of which cross-references the
## Go vuln database. govulncheck pulls its own copy of the database
## on each run (network access required) and exits non-zero on any
## known CVE in the call graph — i.e. it does NOT flood you with
## advisories for libraries you don't actually use.
##
## Installation is gated: govulncheck ships as `golang.org/x/vuln/cmd/govulncheck`.
## If `which govulncheck` returns nothing, we install into the local
## Go bin (GOBIN) and re-run. Subsequent invocations use the cached
## binary.
govulncheck:
	@if ! command -v govulncheck >/dev/null 2>&1; then \
		echo "installing govulncheck (first run only)..."; \
		$(GO) install golang.org/x/vuln/cmd/govulncheck@latest; \
	fi
	$(GO) run golang.org/x/vuln/cmd/govulncheck@latest ./...