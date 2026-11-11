# wishbone - development workflow

set shell := ["bash", "-euo", "pipefail", "-c"]
set dotenv-load := true

golangci_lint := "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0"
embed_dir := "internal/server/web/dist"

# List recipes
default:
    @just --list

# ============================================================================
# Setup
# ============================================================================

# Install JS dependencies (also installs git hooks)
install:
    pnpm install --frozen-lockfile

# Install Playwright's Chromium and WebKit
install-playwright: install
    pnpm --filter web exec playwright install chromium webkit

# ============================================================================
# Build
# ============================================================================

# Build the frontend into web/dist
build-frontend: install
    pnpm --filter web build

# Copy web/dist into the Go embed directory
[private]
embed-frontend:
    rm -rf {{embed_dir}}
    mkdir -p {{embed_dir}}
    cp -R web/dist/. {{embed_dir}}/

# Write a placeholder index.html so Go compiles without a frontend build
[private]
embed-placeholder:
    #!/usr/bin/env bash
    set -euo pipefail
    if [ ! -f {{embed_dir}}/index.html ]; then
        mkdir -p {{embed_dir}}
        echo '<!doctype html><title>wishbone</title><h1>wishbone</h1>' > {{embed_dir}}/index.html
    fi

# Go build tags come only from the argument, never from the environment or
# .env, so only `build-e2e` can put the e2e switch in a binary.

# Build the Go binary into bin/wishbone (expects web/dist to exist)
build-backend tags="": embed-frontend
    #!/usr/bin/env bash
    set -euo pipefail
    COMMIT="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo unknown)}"
    VERSION="${VERSION:-dev+${COMMIT}}"
    BUILD_TIME="${BUILD_TIME:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
    CGO_ENABLED=0 go build \
        -trimpath \
        {{ if tags != "" { "-tags " + tags } else { "" } }} \
        -ldflags="-s -w -X 'main.version=${VERSION}' -X 'main.commit=${COMMIT}' -X 'main.buildTime=${BUILD_TIME}'" \
        -o bin/wishbone \
        ./cmd/wishbone
    echo "✓ bin/wishbone ${VERSION}{{ if tags != "" { " (tags: " + tags + ")" } else { "" } }}"

# Build frontend and backend
build: build-frontend build-backend

# The -tags e2e binary can reach private addresses when
# WISHBONE_EXTRACT_ALLOW_PRIVATE=true, so a test can point a link read at a fixture
# page. Never ship it.

# Build the e2e test binary (never ship it)
build-e2e: build-frontend
    just build-backend e2e

# Build and run the binary
run: build
    ./bin/wishbone serve

# ============================================================================
# Development
# ============================================================================

# Run the backend from source on :8080 (pair with dev-frontend)
dev-backend: embed-placeholder
    WISHBONE_PUBLIC_URL="${WISHBONE_PUBLIC_URL:-http://localhost:3000}" \
    WISHBONE_DATA_DIR="${WISHBONE_DATA_DIR:-./tmp/dev-data}" \
    WISHBONE_LOG_LEVEL="${WISHBONE_LOG_LEVEL:-debug}" \
    go run ./cmd/wishbone serve

# Run the Vite dev server on :3000 (proxies /api to :8080)
dev-frontend: install
    pnpm --filter web dev

# ============================================================================
# Test
# ============================================================================

# Run Go tests with race detector and coverage
test-backend: embed-placeholder
    mkdir -p coverage
    go test -race -timeout 25m -coverprofile=coverage/backend.out ./...
    go test -tags e2e ./cmd/wishbone

# Run frontend unit tests with coverage
test-frontend: install
    pnpm --filter web test:coverage

# Run backend and frontend tests
test: test-backend test-frontend

# Run Playwright end-to-end tests against bin/wishbone
test-e2e: build-e2e
    pnpm --filter web test:e2e

# Regenerate the PNG icons in web/public from favicon.svg (needs Playwright's Chromium)
icons: install-playwright
    pnpm --filter web exec node scripts/make-icons.mjs

# ============================================================================
# Database
# ============================================================================

# Create a timestamped SQL migration in internal/store/migrations
migration name:
    go run github.com/pressly/goose/v3/cmd/goose@v3.29.0 -dir internal/store/migrations create {{name}} sql

# ============================================================================
# Lint and format
# ============================================================================

# Run all linters and the type checker
lint: lint-backend lint-frontend lint-ci typecheck

# Lint Go code
lint-backend: embed-placeholder
    go run {{golangci_lint}} run ./...
    go run {{golangci_lint}} run --build-tags e2e ./cmd/...

# Lint frontend code
lint-frontend: install
    pnpm --filter web lint

# Lint the GitHub workflows and scripts
lint-ci:
    actionlint
    shellcheck .github/scripts/*.sh

# Type-check the frontend
typecheck: install
    pnpm --filter web typecheck

# Auto-fix lint issues
lint-fix: embed-placeholder install
    go run {{golangci_lint}} run --fix ./...
    pnpm --filter web lint --fix

# Format Go code
fmt: embed-placeholder
    go run {{golangci_lint}} fmt ./...

# ============================================================================
# Utilities
# ============================================================================

# Remove build output (pass "all" to also remove node_modules)
clean target="":
    rm -rf bin tmp coverage web/dist web/coverage {{embed_dir}} web/playwright-report web/test-results
    if [ "{{target}}" = "all" ]; then rm -rf node_modules web/node_modules; fi

# Build the Docker image
docker-build tag="dev":
    docker build -t wishbone:{{tag}} .
