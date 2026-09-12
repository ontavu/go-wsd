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

Six specialised reviewers live in `.claude/agents/`, and `/panel` is how they are run:
it prepares the diff once, routes it to the reviewers the changed paths implicate, and
launches only those. **`.claude/PANEL.md` is the full account** — what each owns, why they
are not sandboxed and what the one procedural guard is, which models they run on and why,
and the house rule that a finding arrives as a test. Read it before running a review.

It is a separate file on purpose. This one is injected in full into every subagent's
context — measured, not assumed: a reviewer asked to report what it could see found
`CLAUDE.md` verbatim and no trace of `AGENTS.md`, whose `@` reference is not expanded for
a subagent. Orchestration prose here is paid for by every reviewer on every run, so it
lives where only the main session reads it. Keep this file small for the same reason.
