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
  `io`/`os` — **never hand untrusted code a fully-opened state.**
- **Bound execution time.** Use `SetContext(ctx)` for cooperative cancellation
  (deadline/timeout/abort). Note the current limit: cancellation is checked at
  the VM's poll points, so a script blocked *inside a host Go call* is not
  interrupted until control returns.
- **No hard instruction/step budget yet.** An instruction-budget cap is on the
  roadmap. Until then, treat wall-clock (`SetContext`) as the execution bound.
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
