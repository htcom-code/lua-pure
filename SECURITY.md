# Security Policy

## Reporting a vulnerability

Please report security issues **privately** — do not open a public issue for an
unpatched vulnerability.

- Use GitHub's **"Report a vulnerability"** (Security → Advisories) on this
  repository, or
- email the maintainer at **htjulia1@gmail.com**.

Include a description, affected version/commit, and a minimal reproducer if you
have one. We'll acknowledge the report and work with you on a fix and
coordinated disclosure.

## Supported versions

luapure is pre-1.0; fixes land on the active line.

| Version | Supported |
|---|---|
| 0.1.x (PUC-Lua 5.4.8) | ✅ |
| < 0.1.0 | ❌ |

## Running untrusted Lua — threat model

luapure can execute untrusted Lua scripts, but **do so deliberately**. The
embedding API provides the building blocks; the host is responsible for the
policy.

- **Use a sandbox.** `NewSandbox()` opens only the safe libraries (base, string,
  table, math, utf8, coroutine) and omits `io`, `os`, `debug`, and `package`;
  `load`/`loadfile`/`dofile` are removed. A bare `NewState()` + `OpenLibs()`
  exposes the full standard library, including filesystem and process access via
  `io`/`os` — **never hand untrusted code a fully-opened state.** Note that
  `OpenLibs` after `NewSandbox` re-opens everything, defeating the sandbox.
- **Bound CPU and wall-clock — two orthogonal limits, both checked between VM
  instructions.** `SetContext(ctx)` cancels on a deadline/abort;
  `SetInstructionLimit(n)` caps executed Lua instructions (a runaway-CPU guard
  that, unlike a `debug.sethook` count hook, a script cannot remove). Coroutines
  share one instruction budget, so spawning threads can't multiply it.
- **A blocking Go callback ignores both limits.** A callback blocked *inside* a
  Go call — a channel receive, a network read, a syscall — is not interrupted by
  `SetContext`/`SetInstructionLimit` until control returns to the VM, because the
  VM only checks between instructions. So a hostile or slow script can pin the
  goroutine (and, in a pool, a worker slot) indefinitely. **Make blocking
  callbacks cancellable**: read `L.Context()` and `select` on its `Done()`, or
  pass it to a context-aware API. Pure-CPU spinning *inside* a callback (neither
  blocking nor executing Lua) is stoppable by none of these — keep callbacks
  short and cooperative.
- **A panicking Go callback escapes to the host by default.** A registered Go
  function that does a raw `panic(...)` (not a Lua error) propagates out of the
  VM and leaves that State unsafe to reuse. For a pool or any setup running
  less-trusted callbacks, construct with `WithRecoverGoPanics()` so such a panic
  becomes a catchable `*GoPanicError` and the VM is unwound cleanly. Proper Lua
  errors (`error`, `ArgError`/`TypeError`, `RaiseError`/`RaiseValue`) are always
  caught — this is only about unexpected Go panics.
- **Don't leak one State's values into another goroutine.** A `Value` for a
  reference type (table, function, userdata) points into the owning State's heap,
  which only one goroutine may touch at a time. When passing data across a
  goroutine or channel boundary, **materialize it** with `FromValue` (to a Go
  value) on the way out and `ToValue` on the way in; never share a raw reference
  `Value` or call into a State from two goroutines at once. (Scalars and strings
  are values and safe to copy; functions/threads are bound to their State and
  cannot cross at all.) `NewState` itself is safe to call concurrently, so
  building a pool of independent States is fine.
- **No built-in memory budget.** GC is delegated to the Go runtime. The
  configurable size caps (`MaxTableArraySize`, `MaxLexElement`, constant count)
  are guards against pathological inputs, not a general memory quota — a hostile
  script can still allocate heavily. Run untrusted workloads with OS/container
  limits if memory is a concern.
- **Userdata is host trust.** Any Go function or userdatum you register is fully
  reachable from script. Validate arguments (`Check*`/`Opt*`) and don't expose
  capabilities you don't want scripts to have.

## Scope

In scope: VM/compiler correctness bugs with security impact (e.g. sandbox escape
from `NewSandbox`, out-of-bounds in chunk loading/`string.pack`, crashes on
crafted bytecode).

Out of scope: resource exhaustion from a script run in a fully-opened state
(that's a configuration choice — sandbox it), and the documented structural
divergences from PUC.

> Loading **untrusted precompiled bytecode** is inherently risky — as in PUC-Lua,
> the undump path trusts its input is well-formed. Prefer compiling from source
> for untrusted input.
