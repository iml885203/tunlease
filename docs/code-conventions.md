# Code conventions

[English](code-conventions.md) · [繁體中文](code-conventions.zh-TW.md)

These conventions guide structural changes in Tunlease. They supplement the
architecture invariants in [`AGENTS.md`](../AGENTS.md) and the observable
contracts in [`architecture.md`](architecture.md). Prefer the smallest design
that makes ownership and behaviour clear.

## Organize by domain

File names answer “what concept does this file own?”, not “what layer is this?”
Use domain names such as `claim.go`, `release.go`, `tunnel_session.go`, or
`tunnel_proxy.go`. Do not create catch-all files such as `utils.go`,
`helpers.go`, or `services.go`.

When code is shared by several operations, name its file after the shared
concept. For example, code shared by claim, list, and release belongs in a
claim- or lifecycle-named file rather than in one arbitrary command file.
Package entry-point files and `_test.go` files are exceptions.

Splitting a large file is not by itself a reason to introduce new packages,
interfaces, or layers. Keep cohesive code in its current package unless a real
ownership boundary exists.

## Preserve structured errors

Wrap errors with `%w` when adding context. Classify errors with `errors.Is` or
`errors.As`; production code must not inspect error strings to make control-flow
decisions.

The domain that detects a condition owns its error vocabulary. Use sentinel or
typed errors when callers need to distinguish the condition. User-facing
layers may translate those errors into stable API codes or actionable CLI
messages without discarding the unwrap chain.

Tests may inspect error text when verifying a user-facing message. Returning an
error unchanged is acceptable when the caller already supplies all useful
context; do not add redundant wrapping.

## Put repeated operations on the owning domain

When the same non-trivial, multi-step operation appears in at least three
production call sites, expose an entry point on the type or package that owns
the operation. That domain also owns the relevant sentinel errors and
behavioural policy.

Do not extract an entry point merely because code could be named:

- Keep a function with one production caller near that caller.
- Inline trivial one-to-three-line wrappers around the standard library.
- Do not merge operations that intentionally use different retry policies,
  timeouts, output sinks, or failure semantics.

The three-call-site threshold is a design signal, not a mandate. Prefer
duplication when the apparent similarity hides different domain behaviour.

## Choose callbacks and interfaces deliberately

For simple signalling within one package, prefer function-field callbacks over
a thin interface created only to name a layer. Callbacks are appropriate for
optional observational hooks such as output or activity notifications.

Use an interface when it represents a real boundary:

- the caller needs to replace an implementation in tests;
- implementations live in another package;
- multiple implementations have meaningful behaviour; or
- an established Go interface such as `http.Handler`, `io.Reader`, or
  `net.Conn` already expresses the contract.

Keep interfaces narrow and define them from the consumer’s needs. Do not
replace existing cross-package boundaries such as `registry.Store` with
callbacks merely to reduce the number of types.

## Document channel delivery policy

Every exported event channel or subscription API documents whether delivery is
blocking, buffered, best-effort, or lossless. A non-blocking send must state
that slow consumers may lose events.

Observational events, such as request activity used for terminal display, may
be best-effort. Correctness-critical signals, including terminal release and
expiry, must not share a channel whose delivery policy permits drops.

Channel ownership must also be clear: the producer closes its output channel,
and consumers never close a channel they did not create.

## Encapsulate mutable shared state

Mutable shared state belongs to a receiver type that owns its lock. Avoid
package-level mutable maps and slices. Comments identify which fields a mutex
guards when that relationship is not obvious.

Do not expose mutable state that requires callers to hold an internal lock.
Return snapshots or clones of maps, slices, and pointer-bearing values when
callers could otherwise mutate internal state after the lock is released.

Keep the locked region focused on state transitions. Do not hold a mutex across
network calls, stream I/O, callbacks, or other operations that may block.

## Prefer sociable unit tests and end-to-end tests

Tunlease relies on two kinds of tests. Sociable unit tests drive a real entry
point — the CLI command, the gateway HTTP handler, the exported client API — and
let it use its real collaborators, so one case covers the chain it touches: auth,
path validation, routing, and fallback. End-to-end tests in
`scripts/e2e-compose.sh` cover what only appears across processes: fail-open,
claim release after a tunnel dies, claim expiry.

Avoid solitary unit tests: do not isolate a unit behind mocks to test it alone.
Mirroring one function's branches restates the implementation, so every code
change forces a matching test edit and the pair becomes two copies of the same
logic. Cover unexported helpers through the entry point that uses them.

## Refactoring verification

A refactor preserves observable behaviour unless the change explicitly says
otherwise. Run the narrowest relevant tests while editing, then before
handoff:

```bash
make preflight
make e2e
git diff --check
```

If a refactor changes user-facing semantics, update the English and paired
Traditional Chinese documentation in the same change.
