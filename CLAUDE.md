# CLAUDE.md

The instructions for this repository are agent-agnostic and live in @AGENTS.md.
Read them first: the commands, the licence-header rules and the conventions all apply
in full. `README.md` comes before both — it owns the protocol, the two flavors, the
`wsd` API, the `wsdc` CLI and the package layout.

## Claude-specific directives

### Attribution

`AGENTS.md` carries the rule: an LLM is never an author, and work it helped with is marked
`Assisted-By: Claude Code`. What is Claude-specific is where that line comes from —
`attribution` in `.claude/settings.json`, committed beside this file:

```json
{ "attribution": { "commit": "Assisted-By: Claude Code", "pr": "Assisted-By: Claude Code" } }
```

Three things about it are worth knowing before touching it. It **replaces** the default
trailer rather than suppressing it, so there is nothing to add on top — an instruction to
append the line would double it. Both sub-keys are set on purpose: the resolver defaults
whichever is missing, and the default is the `Co-Authored-By` line being removed. And a
malformed `attribution` object fails **silently** back to that default, so a change here is
verified by reading a commit message, never by reading the JSON.

`includeCoAuthoredBy` is the legacy key for the same job and only switches the line off.
Do not reach for it; `attribution` is checked first and is what this repository uses.

### The review panel

CI never sees a device. `.github/workflows/ci.yml` formats, vets, tests and builds, but
nothing on it can multicast a Probe at a camera or a printer, and the tests that need a
multicast listener skip themselves when they cannot open one. Every claim about what
equipment does is either pinned by a test over a canned datagram or recorded as a
measurement in `README.md`. Correctness therefore rests on review, so this repository
keeps five specialised reviewers in `.claude/agents/`:

| Agent | Owns |
| --- | --- |
| `wsd-protocol` | Bytes on the wire: the two WS-Discovery versions, SOAP 1.2 and `mustUnderstand` qualification, WS-Addressing, SOAP-over-UDP §4, `urn:uuid`, QName and namespace resolution, the discovery groups. |
| `untrusted-input` | The one hostile boundary: parse-and-drop discipline, nil roots, the retention and complexity bounds, which advertised addresses are let through, observed sender versus claimed identity, and what reaches a terminal. |
| `runtime-safety` | Concurrency and resource lifetime: the two ways a blocked read is unblocked, watchdog ordering, channel close ordering, context propagation, and the sockets and memberships a call holds. |
| `go-architect` | The `AGENTS.md` rulebook and design coherence, including the `wsd` dependency boundary and the deliberate decisions (the `prober` seam, the narrow `PacketConn`, finding nothing is not an error). |
| `cli-ux` | `wsdc` as an interface — flags, usage text, exit codes, the tab-separated output contract — and whether `README.md`, `wsd/doc.go` and `AGENTS.md` still describe the code. |

**All five are read-only** (`disallowedTools: Write, Edit, NotebookEdit`), so they are safe
to run in parallel on one diff — launch them in a single message. They report; the main
session applies the fixes.

They do not all run on the same model, and the split is deliberate. `wsd-protocol` and
`cli-ux` are pinned to `sonnet`: both work from a checklist their own file spells out, one
against clause numbers and one against three documents that drift. The other three stay on
`inherit`, because their job is to invent the failure rather than look it up — which
datagram panics the parser, which interleaving races, whether a design belongs here at all.
Prompt caching does not cover subagents, so each reviewer pays full price for its own
context on every run; that is what the cheaper pair is buying back. Raise one to `inherit`
if it starts missing findings, and say in its file why.

### Which to reach for

By what the change touches:

- `wsd/ws-discovery.go`, `wsd/flavor.go`, `wsd/types.go` → `wsd-protocol`
- `wsd/parse.go` → `wsd-protocol` **and** `untrusted-input`, always: it is both the
  namespace resolver and the trust filter
- `wsd/discover.go`, `wsd/listen.go` → `runtime-safety`, and `untrusted-input` if a bound,
  a cap or a parsed field moved
- `wsd/transport/` → `runtime-safety` and `wsd-protocol`
- `bin/wsdc/`, `README.md`, `wsd/doc.go` → `cli-ux`
- `gosoap/` → `wsd-protocol` for the envelope, `go-architect` for the vendored-code rules
- a new `.go` file, a new package, interface, exported symbol or dependency →
  `go-architect`
- any new `go func`, channel, deadline or socket → `runtime-safety`
- anything that parses, prints or stores a value taken from a datagram →
  `untrusted-input`, always

Non-trivial diffs get the whole panel. Do not invoke one to rubber-stamp work another has
already reviewed — they are deliberately non-overlapping, and each will say so and
redirect if handed something outside its remit.

### A finding arrives as a test

This repository has a habit worth keeping: the tests are as long as the code, and each one
records what went wrong once. `TestParseAnnouncementHostileInput` and
`TestParseProbeMatchesHostileInput` are tables of malformed datagrams;
`TestReadRepliesBoundsRetainedBytes` pins a retention cap;
`BenchmarkParseProbeMatchesCrafted` is the standing measurement behind the scope memo;
`TestProbeMustUnderstandIsQualified` pins one clause of SOAP 1.2;
`TestDiscoverRecordsReplySource` pins observed-versus-claimed with an actual forger;
`TestOrDashKeepsHostileFieldsInTheirColumn` pins the terminal-injection filter;
`TestNoStandardLogger` keeps `gosoap` from reporting to a logger its caller never chose.

So a reviewer's finding is not finished as prose. Where it can be pinned, it comes with the
test written out, in the style of the target package, with a comment saying what the slip
was and what it was verified against — a clause number, or a measurement. Two seams exist
so that this needs no hardware: `prober` (`wsd/discover.go:206`) for the aggregation path
and `fakeConn` (`wsd/discover_test.go:20`) for the socket path. Use them; a test that
needs an interface belongs behind a `t.Skipf` like `TestListenStopsOnContextCancel`.

Apply the test with the fix, in the same change.
