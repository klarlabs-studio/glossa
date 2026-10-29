.PHONY: help api packages test lint fmt migrate-up migrate-down sqlc-gen platform-test platform-lint platform-integration system-m2 system-m3 system-m4 system-m4-studio system-m5 system-m5-deps system

# Go modules of the rewrite (RFC 0002). apps/api and packages/format are the
# last of v0.3: fixtures of the importer's tests and of the M5 exit test
# (RFC 0006 §7.3, §12.6), not a product. They get no other work.
PLATFORM_MODULES := messageformat platform runtimes/go runtimes/go/examples/pdf

help: ## Show this help.
	@awk 'BEGIN{FS=":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  %-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# ── Backend ─────────────────────────────────────────────────────────
api: ## Run the Go API locally (requires Postgres at $DATABASE_URL).
	cd apps/api && go run ./cmd/api

api-test: ## Run Go tests.
	cd apps/api && go test ./...

# ── Web (packages) ──────────────────────────────────────────────────
packages: ## Build every TS package (topological order).
	pnpm -r --filter "./packages/*" --filter "./messageformat/js" --filter "./runtimes/js/*" build

web-test: ## Run vitest across the JS workspaces.
	pnpm -r test

# ── Platform (rewrite) ──────────────────────────────────────────────
platform-test: ## Unit tests for every rewrite Go module.
	@for m in $(PLATFORM_MODULES); do (cd $$m && go test ./...) || exit 1; done

platform-lint: ## go vet for every rewrite Go module.
	@for m in $(PLATFORM_MODULES); do (cd $$m && go vet ./...) || exit 1; done

platform-integration: ## Docker-backed integration tests (Postgres, object storage).
	@for m in $(PLATFORM_MODULES); do (cd $$m && go test -tags=integration -timeout=900s ./...) || exit 1; done

system-m2: ## M2 exit test (Docker): fill es/fr/ja through glossa-server; writes platform/internal/systemtest/m2/REPORT.md.
	cd platform && go test -tags=system -timeout=600s -count=1 -v ./internal/systemtest/m2/...

system-m3: ## M3 exit test (Docker + Chrome): context, the PR flow, the overlay guards and the documents; writes platform/internal/systemtest/m3/REPORT.md.
	cd platform && go test -tags=system -timeout=900s -count=1 -v ./internal/systemtest/m3/...

system-m4-studio: ## Build Studio so the M4 exit test can open its quality view (RFC 0005 §12.8).
	pnpm --filter "@klarlabs-studio/glossa..." build
	pnpm --filter @glossa/studio build
	pnpm --filter @glossa/studio exec playwright install chromium

system-m4: system-m4-studio ## M4 exit test (Docker + Chrome + Dart + a built Studio): RFC 0005 §12's eight criteria; writes platform/internal/systemtest/m4/REPORT.md.
	cd platform && go test -tags=system -timeout=2700s -count=1 -v ./internal/systemtest/m4/...

system-m5-deps: system-m4-studio ## Build what the M5 exit test renders and rolls out with: @klarlabs-studio/glossa, v0.3's formatter, the Dart runtime's packages.
	pnpm --filter @felixgeelhaar/glossa-format build
	cd runtimes/dart && dart pub get

system-m5: system-m5-deps ## M5 exit test (Docker + Node + Dart + Python, and everything M2–M4 need): RFC 0006 §12's seven criteria; red from wave 1 by design; writes platform/internal/systemtest/m5/REPORT.md.
	cd platform && go test -tags=system -timeout=5400s -count=1 -v ./internal/systemtest/m5/...

system: system-m5 ## Every exit test, one after the other: M5's §12.7 runs the M2, M3 and M4 exit tests unchanged before its own.

# ── Cross-cutting ───────────────────────────────────────────────────
test: api-test platform-test web-test ## Backend + frontend tests.

lint: platform-lint ## Backend (go vet) + frontend (tsc via pnpm).
	cd apps/api && go vet ./...
	pnpm -r lint

fmt: ## gofmt + prettier across the monorepo.
	cd apps/api && gofmt -w .
	@for m in $(PLATFORM_MODULES); do (cd $$m && gofmt -w .); done
	pnpm -r format

# ── DB / codegen (placeholders until apps/api lands) ────────────────
migrate-up: ## Apply DB migrations.
	cd apps/api && go run ./cmd/migrate up

migrate-down: ## Roll back one DB migration.
	cd apps/api && go run ./cmd/migrate down

sqlc-gen: ## Regenerate sqlc bindings.
	cd apps/api && go tool sqlc generate
