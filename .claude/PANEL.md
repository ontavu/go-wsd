# The review panel

Read this before running a review. It is deliberately **not** `@`-referenced from
`CLAUDE.md`: `CLAUDE.md` is injected in full into every subagent's context, measured and
not assumed, and none of what follows is of any use to a reviewer. It was 82% of that file
and every reviewer paid for it on every run.

## Why the panel exists

CI never sees a device. `.github/workflows/ci.yml` formats, vets, tests and builds, but
nothing on it can multicast a Probe at a camera or a printer, and the tests that need a
multicast listener skip themselves when they cannot open one. Every claim about what
equipment does is either pinned by a test over a canned datagram or recorded as a
measurement in `README.md`. Correctness therefore rests on review, so this repository
keeps six specialised reviewers in `.claude/agents/`:

| Agent | Owns |
| --- | --- |
| `wsd-protocol` | Bytes on the wire: the two WS-Discovery versions, SOAP 1.2 and `mustUnderstand` qualification, WS-Addressing, SOAP-over-UDP §4, `urn:uuid`, QName and namespace resolution, the discovery groups. |
| `untrusted-input` | The one hostile boundary: parse-and-drop discipline, nil roots, the retention and complexity bounds, which advertised addresses are let through, observed sender versus claimed identity, and what reaches a terminal. |
| `runtime-safety` | Concurrency and resource lifetime: the two ways a blocked read is unblocked, watchdog ordering, channel close ordering, context propagation, and the sockets and memberships a call holds. |
| `go-architect` | The `AGENTS.md` rulebook and design coherence, including the `wsd` dependency boundary and the deliberate decisions (the `prober` seam, the narrow `PacketConn`, finding nothing is not an error). |
| `cli-ux` | `wsdc` and `wsd` as an interface — flags, usage text, exit codes, the tab-separated output contract, and whether an exported symbol answers its caller's question. |
| `docs-drift` | Whether `README.md`, `wsd/doc.go` and `AGENTS.md` still describe the code, and which claims need equipment to re-verify rather than editing. |

**All six report; the main session applies the fixes.**

## Run them with `/panel`

`.claude/commands/panel.md` prepares the diff once, routes it to the reviewers its changed
paths implicate, launches only those in a single message, and ends with the guard below.
The routing table lives there rather than here, so there is one copy of it and it is the
copy that executes.

Three things it does are worth understanding, because each was a measured cost:

- **It routes.** Launching six reviewers for a one-file change is the largest waste
  available. A `wsd/transport/` change implicates two.
- **It prepares.** `.claude/panel-prep.sh` runs gofmt, vet, build, `go test -race ./...`
  and the `wsd` dependency boundary once and writes `checks.txt`. Six agents re-running
  the suite costs almost nothing in tokens — it prints 184 bytes when it passes — but it
  costs six builds of wall clock and six private opinions about what "clean" means.
- **It puts them on a reading diet.** The expensive part of a reviewer is not its
  briefing, it is the files it opens: a scope of 1,800 to 3,200 lines read whole dwarfs
  everything else. Each agent file now says to work from `diff.patch` and to open a file
  by line range only where a hunk or an anchor needs it.
- **It checks its own anchors.** The briefings carry seventy-six `path:line` citations,
  all hand-maintained, and the reading diet is what makes a stale one dangerous: a
  reviewer sent to the wrong lines reports on what it finds there. `panel-prep.sh` fails
  when one no longer resolves, and separately *lists* every anchor into a file the diff
  touches, to be re-verified by hand. That second half exists because the first was not
  enough: inserting twenty lines into `wsd/listen.go` moved eight anchors onto the wrong
  statements while every range stayed inside the file, and the check said PASS. The
  comment above `anchors_check` records that, the two richer checks tried and rejected,
  and the one anchor form it does not cover at all.

For a second round after applying fixes, **continue the same agent with `SendMessage` and
hand it only the incremental diff.** A fresh `Agent` call rebuilds the briefing, the
injected `CLAUDE.md` and every file it read. Never run the panel twice over one review.

Do not invoke a reviewer to rubber-stamp work another has already reviewed — they are
deliberately non-overlapping, and each will say so and redirect if handed something
outside its remit. The one mandated overlap is `wsd/parse.go`, which goes to
`wsd-protocol` **and** `untrusted-input` always: it is both the namespace resolver and the
trust filter. They are not merged, although merging would save one read of the same 750
lines, because two axes in one head is how the second axis stops finding anything.

## They are not sandboxed

`disallowedTools: Write, Edit, NotebookEdit` removes the file tools, but all of them keep
`Bash` — they need it for the mechanical checks their own files prescribe, `go list -deps`,
a targeted `go test -race`, `grep -l`, running the built binary — and a shell writes. One
of them demonstrated it: asked only to review a diff, it used a shell to modify five
tracked files, including an upper bound on `wsd.Discover`'s collection window that nobody
had asked for. It was reverted, and the bound was added deliberately afterwards, which is
where `MaxProbeTimeout` comes from.

Two fixes were measured and rejected. `permissionMode: plan` in an agent's frontmatter is
accepted and enforces nothing: a reviewer under it still appended to `AGENTS.md`, still ran
`sed -i` on it, and still created a file, none of it refused. A `PreToolUse` hook *can*
target them precisely — `agent_id` and `agent_type` are present in the payload of a
subagent's call and absent from the main session's, which was verified rather than assumed
— but the policy it would have to encode is either a path judgement that is not airtight or
an allowlist that costs the reviewers the ability to invent an experiment, and inventing
experiments is where their best findings come from.

So the guard is procedural, and it is one line. **End a panel run with `git status --short`
and confirm the diff is only yours.**

## Models

They do not all run on the same model, and the split is deliberate. `wsd-protocol`,
`cli-ux` and `docs-drift` are pinned to `sonnet`: each works from a checklist its own file
spells out — against clause numbers, against a command tree, against three documents that
drift. The other three stay on `inherit`, because their job is to invent the failure rather
than look it up — which datagram panics the parser, which interleaving races, whether a
design belongs here at all. Prompt caching does not cover subagents, so each reviewer pays
full price for its own context on every run; that is what the cheaper trio is buying back.
Raise one to `inherit` if it starts missing findings, and say in its file why.

## Where a known defect goes

The recurring idea is to give each reviewer a list of known defects. Mostly: no. Try these
in order, and stop at the first that fits.

1. **If it can be pinned, it becomes a test**, and the briefing names the test in one line
   instead of retelling the story. `wsd/readme_test.go` and `bin/wsdc/readme_test.go` are
   the models — they replaced about fifteen lines of drift narrative in `cli-ux.md`. A
   prose list is a second copy of what the test asserts, and the copy that cannot fail is
   the copy that drifts.
2. **If it generalises, it becomes a rule** in the agent body — one line, permanent,
   covering every future instance. The preamble's "check an anchor before you cite it" is
   the generalised form of a briefing that once described a `wsdc -h` transcript
   `README.md` does not carry. One rule beats one entry per incident.
3. **If it is neither** — unpinnable, un-generalisable, and true — it goes in the body **at
   its point of use**, never in a list at the top. `runtime-safety.md` puts the
   deadline-before-watchdog bug inside the paragraph about the two ways a blocked read is
   unblocked, where it changes a judgement. Hoisted into a registry it would be read
   before it means anything and missing where it matters. The `MaxProbeTimeout` origin and
   the empty `d:Types` element are the same shape.
4. **Never in `description:`.** That field is routing metadata — it answers "reach for me
   when" — and it is loaded into the *main session's* context on every turn, including
   every session that never reviews anything. The six total 2,670 bytes. Keep them that
   size.

There is a further reason not to keep a growing list, and it is the expensive one: three
of these agents are on `inherit` because their job is to invent the failure rather than
look one up. A defect list turns an inventor into a checklist-follower, which is what the
`sonnet` three already are, more cheaply. You would be paying inherit prices for sonnet
behaviour.

## A finding arrives as a test

This repository has a habit worth keeping: the tests are as long as the code, and each one
records what went wrong once. `TestParseAnnouncementHostileInput` and
`TestParseProbeMatchesHostileInput` are tables of malformed datagrams;
`TestReadRepliesBoundsRetainedBytes` pins a retention cap;
`BenchmarkParseProbeMatchesCrafted` is the standing measurement behind the scope memo;
`TestProbeMustUnderstandIsQualified` pins one clause of SOAP 1.2;
`TestDiscoverRecordsReplySource` pins observed-versus-claimed with an actual forger;
`TestOrDashKeepsHostileFieldsInTheirColumn` pins the terminal-injection filter;
`TestNoStandardLogger` keeps `gosoap` from reporting to a logger its caller never chose;
and `TestREADMEDocumentsProbeOptionDefaults` with
`TestREADMEListsEveryFlagTheTreeRegisters` pin the two documentation drifts that a
reviewer used to hunt by eye.

Those last two are the pattern to extend. A check a reviewer performs by reading two
documents and comparing is a check that belongs in the suite: it then runs on every commit
instead of whenever someone remembers to launch an agent, and it costs nothing per run.
When writing one, make it fail when its own pattern stops matching — a drift test that
silently finds nothing to compare is worse than none.

So a reviewer's finding is not finished as prose. Where it can be pinned, it comes with the
test written out, in the style of the target package, with a comment saying what the slip
was and what it was verified against — a clause number, or a measurement. Two seams exist
so that this needs no hardware: `prober` (`wsd/discover.go:224`) for the aggregation path
and `fakeConn` (`wsd/discover_test.go:20`) for the socket path. Use them; a test that
needs an interface belongs behind a `t.Skipf` like `TestListenStopsOnContextCancel`.

Apply the test with the fix, in the same change.
