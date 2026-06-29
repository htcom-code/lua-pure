<!--
Thanks for contributing to luapure! Keep the title in Conventional Commits
form, e.g. `fix(vm): correct OP_CONCAT overflow check`.
Delete any section that does not apply.
-->

## What & why

<!-- What does this change do, and what problem does it solve? -->

## Type of change

- [ ] `fix` — bug fix (behaviour now matches PUC-Lua)
- [ ] `feat` — new capability (embedding API / library surface)
- [ ] `perf` — performance, no observable behaviour change
- [ ] `docs` — documentation only
- [ ] `refactor` / `chore` / `test` / `ci` — no behaviour change

## Fidelity (PUC-Lua 5.4.8)

luapure tracks PUC upstream; observable semantics must match the reference.

- [ ] Behaviour matches PUC-Lua 5.4.8 (or this is a documented Go-native divergence)
- [ ] For VM/codegen changes: `luac`-byte-identity is preserved
- [ ] No new divergence from PUC; if there is one, it is documented in the README / `docs/COMPATIBILITY.md`

## Verification

<!-- Paste the relevant output. `make check` runs build + vet + race tests + conformance. -->

- [ ] `make check` passes (build, `go vet`, race tests, conformance **30/33**)
- [ ] Conformance count unchanged (or improvement explained)
- [ ] For `perf`: interleaved `benchstat` before/after included below; no regression on the `lua/*_bench_test.go` guardrails

```
<!-- benchstat / test output -->
```

## Related

<!-- Closes #123, related issues, brain/decision links if any. -->
