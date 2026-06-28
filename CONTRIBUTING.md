# Contributing to luapure

luapure is a **faithful Go port of PUC-Lua**. The single most important rule is
that behaviour is decided by the reference C sources, not by intuition. Most of
this document is about keeping that fidelity.

## The golden rule: PUC-source-first

Before implementing or changing any library function, VM behaviour, error
message, or chunk format, **read the corresponding PUC-Lua source first**
(`lua-5.4.8/src`) and match its behaviour, output format, and edge cases. Do not
code from memory or assumption — Lua's observable details (error text, argument
coercion, off-by-one bounds, format specifiers) are subtle and `conformance`
will catch the mismatch later, expensively.

When in doubt, the PUC source is the spec.

## Project conventions

- **File names mirror PUC.** `lfoo.c` → `lfoo.go`. Where one PUC source is split
  across several Go files, the parts keep the parent name plus a suffix
  (e.g. `lvm.go`, `lvm_execute.go`, `lvm_arith.go`). See
  [`docs/FILEMAP.md`](docs/FILEMAP.md) for the full PUC↔Go map.
- **One engine package.** The VM lives in `lua/` as `package luapure`, flat like
  PUC's `src/`. Add-ons that build on the public API are separate packages
  (`debugmcp/`, `debugdap/`, `cmd/...`).
- **Intentional divergences only.** The Go-native adaptations (GC via the Go
  runtime, `panic`/`recover` instead of `longjmp`, goroutine coroutines, etc.)
  are documented in the README. Don't add new divergences in observable
  behaviour without a documented reason.
- **`gofmt`.** All Go code is `gofmt`-clean.

## Tests are part of the change

Every behavioural change must be pinned so it can't regress:

1. **Unit / example tests** sit beside their subject as `*_test.go`. Runnable
   examples in `lua/example_test.go` double as embedding-API documentation
   (they render in `make doc-web`) — keep their `// Output:` blocks correct.
2. **The PUC behaviour-probe (ext) suite.** When you fix or add behaviour that
   the official fixtures don't exercise, add a probe under `_glue5.4-tests/`
   pinned to the PUC oracle value. A probe **must pass on both** real
   `lua5.4`/`luac 5.4.8` **and** luapure — that's what makes it a fidelity test
   rather than a snapshot of our current output.
3. **Conformance fixtures.** The official Lua 5.4 suite lives in `_lua5.4-tests/`
   and runs via `go run ./cmd/conformance`.

## The gate: `make check`

Run the full gate before pushing. It must be green:

```sh
make check    # build + vet + go test -race (unit/examples/byte-identity/ext) + conformance driver
```

Individual targets:

| target | what |
|---|---|
| `make build` | `go build ./...` |
| `make vet` | `go vet ./...` |
| `make test` | all Go tests, race-enabled (includes the ext probe suite) |
| `make conformance-ext` | just the ext probe suite, for a quick focused run |
| `make conformance` | the official 5.4 fixtures via the standalone driver |
| `make doc` / `make doc-web` | API docs as text / browsable pkgsite |

`conformance` is **30/33** — the PASS-able ceiling. A change must not drop that
count. The three non-passing files are structural won't-fixes documented in
[`ROADMAP.md`](ROADMAP.md); don't "fix" them by weakening the runner.

## Branches & commits

- Development happens on **`lua-5.4.8`**. `main` is the release space; merging
  `lua-5.4.8` → `main` is a deliberate, explicit step — never automatic.
- Commit messages follow **Conventional Commits** (`feat(api): …`,
  `fix(io): …`, `perf(vm): …`, `docs: …`, `refactor: …`, `test(conf): …`).

## Porting a new PUC release (e.g. 5.5)

The roadmap is to track PUC upstream. Porting a new release follows the same
discipline: read the new sources, port instruction/library/semantic deltas,
extend the conformance fixtures to the new test suite, and re-establish
`luac`-byte-identity against the matching reference `luac`. See
[`ROADMAP.md`](ROADMAP.md).
