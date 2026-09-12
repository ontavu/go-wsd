---
description: Review the working diff with only the reviewers its changed paths implicate
argument-hint: "[git revision range, default: the working tree]"
allowed-tools: Bash, Read, Grep, Glob, Agent, SendMessage
---

Run the review panel over `$ARGUMENTS` (default: the working tree, `git diff HEAD`).

Reviewers cost their whole context on every run — prompt caching does not cover
subagents, and a reviewer that opens its scope whole reads 1,800 to 3,200 lines. So the
panel is routed, prepared and bounded rather than launched wholesale.

## 1. Prepare the bundle, once

Into the scratchpad directory:

```sh
git diff ${ARGUMENTS:-HEAD} > "$SCRATCH/diff.patch"
git diff --name-only ${ARGUMENTS:-HEAD} > "$SCRATCH/changed.txt"
.claude/panel-prep.sh "$SCRATCH"
```

`panel-prep.sh` runs gofmt, vet, build, `go test -race ./...` and the `wsd` dependency
boundary, and writes `checks.txt`. **It is the only run of those commands in this panel.**
If it reports a failure, fix that before launching anyone: reviewers are for what a
compiler and a race detector cannot see.

If `diff.patch` is empty, say so and stop.

## 2. Route

Launch **only** the reviewers the changed paths implicate. The whole panel is for a diff
touching three or more areas, not the default.

| Changed path | Reviewer |
| --- | --- |
| `wsd/ws-discovery.go`, `wsd/flavor.go`, `wsd/types.go` | `wsd-protocol` |
| `wsd/parse.go` | `wsd-protocol` **and** `untrusted-input`, always — it is both the namespace resolver and the trust filter |
| `wsd/discover.go`, `wsd/listen.go` | `runtime-safety` |
| `wsd/transport/**` | `runtime-safety` **and** `wsd-protocol` |
| `bin/wsdc/**` | `cli-ux` |
| `README.md`, `wsd/doc.go`, `AGENTS.md`, `.github/workflows/ci.yml` | `docs-drift` |
| `gosoap/**` | `wsd-protocol` for the envelope, `go-architect` for the vendored-code rules |

Then add, by what the diff *does* rather than where it lands — read `diff.patch` to decide:

- a new `.go` file, package, interface, exported symbol or dependency → `go-architect`
- a new `go func`, channel, deadline or socket → `runtime-safety`
- anything that parses, prints or stores a value taken from a datagram → `untrusted-input`
- a moved bound, cap or retention limit → `untrusted-input`
- a changed exported default → `docs-drift`, because `README.md` quotes the table

Say which reviewers you selected and which you skipped, with the reason, before launching.

## 3. Launch, in a single message

They are deliberately non-overlapping, so one diff goes to all of them at once. Hand each
the same bundle — that is the whole point: the expensive part is shared as text, not
re-derived per agent.

Each prompt carries, and nothing more:

- the absolute paths of `diff.patch`, `changed.txt` and `checks.txt`
- **the verdict line from `checks.txt`, quoted** — and the instruction not to re-run those
  commands, only the targeted test or benchmark its own axis needs
- the subset of changed files inside its own remit
- the report budget: at most five findings, ranked, each at most eight lines; the test
  written out in full for the top finding only, the seam named for the rest

Do not paste the diff into the prompt. They have the path and a shell.

## 4. Apply, then continue — never relaunch

The reviewers report; **this session applies every fix.** Apply the test with the fix, in
the same change.

For a second round, **continue the same agent with `SendMessage`, handing it only the
incremental diff.** A fresh `Agent` call rebuilds the briefing, the injected project
instructions and every file it read; a continuation costs the delta. Never run the panel
twice over the same review.

## 5. Guard

They are not sandboxed. `disallowedTools` removes the file tools, but all five keep `Bash`
and a shell writes — one of them once modified five tracked files when asked only to
review a diff.

**End the run with `git status --short` and confirm the diff is only yours.** If anything
in the tree changed that you did not change, revert it and say which agent did it.
