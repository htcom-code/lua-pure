# Examples

Runnable programs for the `luapure` embedding API. Each is a standalone
`main.go` importing `github.com/htcom-code/lua-pure/lua` (no other dependencies),
so they compile under `go build ./...` and stay current with the API.

Run one with `go run ./examples/<name>`:

| example | shows |
|---|---|
| [`basic`](basic/) | the README quickstart — expose a Go function, run a script, read results |
| [`embed`](embed/) | hand a Go table to a script; `ToValue`/`FromValue` data round-trip |
| [`userdata`](userdata/) | bind a Go type as userdata with a method table and `CheckUserData` |
| [`sandbox`](sandbox/) | `NewSandbox` + per-call `_ENV` (`RunWith`) + a `SetContext` deadline |
| [`channel`](channel/) | a Go channel as userdata: two goroutines/LStates message-pass |
| [`channel-timeout`](channel-timeout/) | cancellable channel `recv` via `L.Context()` + a `SetContext` deadline |
| [`openlib`](openlib/) | write a library that installs like a built-in (`OpenString`-style): a free `OpenX(L)` opener that exposes Go helpers as a global |
| [`customlib`](customlib/) | register host modules with `Requiref` (eager) and `Preload` (lazy) |

For small, doc-integrated snippets see the runnable `Example` functions in
[`lua/example_test.go`](../lua/example_test.go) (they render in `make doc-web`).
The full API surface is in `make doc`; differences from PUC are in
[`docs/COMPATIBILITY.md`](../docs/COMPATIBILITY.md).
