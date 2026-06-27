.PHONY: build vet test conformance-ext conformance check

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
	go test -race -count=1 -run TestConformanceExtSuite ./

# The Lua 5.4 conformance fixtures, run by the standalone driver (not part of
# `go test`). verybig is slow, so allow a longer per-file timeout.
conformance:
	go run ./cmd/conformance -timeout 60s

# The full gate: build, vet, every Go test (conformance-ext included), and the
# conformance driver. Run this before pushing.
check: build vet test conformance
