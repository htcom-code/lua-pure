.PHONY: build vet test conformance-ext conformance check doc doc-web

# Port for the local pkgsite docs server (override: make doc-web DOC_PORT=8080).
DOC_PORT ?= 6060

# Compile everything.
build:
	go build ./...

vet:
	go vet ./...

# All Go tests, race-enabled: unit + examples + dump byte-identity + the PUC
# behaviour-probe extension suite (TestConformanceExtSuite).
test:
	go test -race -count=1 ./...

# Just the PUC behaviour-probe extension suite (also covered by `make test`),
# for a quick focused run.
conformance-ext:
	go test -race -count=1 -run TestConformanceExtSuite ./conformance

# The Lua 5.4 conformance fixtures, run by the standalone driver (not part of
# `go test`). verybig is slow, so allow a longer per-file timeout.
conformance:
	go run ./cmd/conformance -timeout 60s

# The full gate: build, vet, every Go test (conformance-ext included), and the
# conformance driver. Run this before pushing.
check: build vet test conformance

# Print the engine package's exported API documentation as text.
doc:
	go doc -all ./lua

# Serve browsable HTML docs locally with pkgsite (the pkg.go.dev engine),
# fetched on demand via `go run`. The embedding API examples render here.
doc-web:
	@echo "Docs: http://localhost:$(DOC_PORT)/github.com/htcom-code/lua-pure/lua  (Ctrl-C to stop)"
	go run golang.org/x/pkgsite/cmd/pkgsite@latest -http=localhost:$(DOC_PORT) -open .
