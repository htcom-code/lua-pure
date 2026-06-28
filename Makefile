# luapure — build, test, perf, and docs targets. Run `make` for the list.

DOC_PORT ?= 6060   # local pkgsite port (override: make doc-web DOC_PORT=8080)

.DEFAULT_GOAL := help
.PHONY: help build vet fmt test conformance-ext conformance bench check doc doc-web

help: ## List the available targets
	@awk -F':.*## ' '/^[a-z][a-z-]*:.*## /{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# --- build & static checks ---

build: ## Compile every package (engine, cmds, examples)
	go build ./...

vet: ## Run go vet
	go vet ./...

fmt: ## Format all Go code in place (gofmt)
	gofmt -w .

# --- tests ---

test: ## All Go tests, race-enabled (unit + examples + byte-identity + ext suite)
	go test -race -count=1 ./...

conformance-ext: ## Only the PUC behaviour-probe ext suite (quick focused run)
	go test -race -count=1 -run TestConformanceExtSuite ./conformance

conformance: ## Official Lua 5.4 fixtures via the standalone driver
	go run ./cmd/conformance -timeout 60s

check: build vet test conformance ## Full pre-push gate (build + vet + test + conformance)

# --- performance ---

bench: ## Engine micro-benchmarks — the perf-regression guardrail
	go test -run '^$$' -bench . -benchmem ./lua

# --- docs ---

doc: ## Print the engine's exported API as text
	go doc -all ./lua

doc-web: ## Serve browsable HTML API docs locally (pkgsite)
	@echo "Docs: http://localhost:$(DOC_PORT)/github.com/htcom-code/lua-pure/lua  (Ctrl-C to stop)"
	go run golang.org/x/pkgsite/cmd/pkgsite@latest -http=localhost:$(DOC_PORT) -open .
