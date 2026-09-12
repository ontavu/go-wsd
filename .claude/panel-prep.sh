#!/usr/bin/env bash
#
# Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
# SPDX-License-Identifier: MIT
#
# panel-prep.sh runs every mechanical check the reviewers in .claude/agents/ used to run
# for themselves, once, and writes a verdict they are handed instead.
#
# Five agents each running `go test -race ./...` costs almost nothing in tokens — the
# suite prints 184 bytes when it passes — but it costs five builds of wall clock, and it
# leaves each reviewer deciding for itself what "clean" means. Running it here makes the
# verdict one artefact, identical for all of them.
#
# Usage: .claude/panel-prep.sh [output-directory]
# Writes checks.txt there (default: the current directory) and exits non-zero if any
# check failed, so it is also usable as a pre-commit gate.

set -u -o pipefail

cd "$(dirname "$0")/.." || exit 2
out_dir="${1:-.}"
mkdir -p "$out_dir" || exit 2
out="$out_dir/checks.txt"
: >"$out"

failed=0

# run NAME COMMAND... records a command's verdict and its output. Output is quoted in
# full: it is small when a check passes and it is the finding when one does not.
run() {
	local name="$1"
	shift
	local output status
	output="$("$@" 2>&1)"
	status=$?
	if [ $status -eq 0 ]; then
		printf '== %s: PASS\n' "$name" >>"$out"
	else
		printf '== %s: FAIL (exit %d)\n' "$name" $status >>"$out"
		failed=1
	fi
	[ -n "$output" ] && printf '%s\n' "$output" >>"$out"
	printf '\n' >>"$out"
}

# gofmt reports by printing names rather than by exiting non-zero, so it is inverted here.
gofmt_check() {
	local unformatted
	unformatted="$(gofmt -l .)"
	[ -z "$unformatted" ] && return 0
	printf 'not gofmt-ed:\n%s\n' "$unformatted"
	return 1
}

# deps_check is the wsd dependency boundary, which go-architect.md used to ask that agent
# to run by hand on every diff adding an import. wsd must not reach an ONVIF client type:
# discovery runs before any client exists, and depending on one would drag a SOAP stack
# into the build of anyone who only wants to find devices on the link.
deps_check() {
	local got want status=0
	want=$'github.com/ontavu/go-wsd/gosoap\ngithub.com/ontavu/go-wsd/wsd\ngithub.com/ontavu/go-wsd/wsd/transport'

	got="$(go list -deps ./wsd | grep ontavu | sort)"
	if [ "$got" != "$want" ]; then
		printf 'go list -deps ./wsd crosses the boundary.\nwant:\n%s\ngot:\n%s\n' "$want" "$got"
		status=1
	fi

	got="$(go list -deps ./gosoap | grep ontavu | sort)"
	if [ "$got" != "github.com/ontavu/go-wsd/gosoap" ]; then
		printf 'gosoap is a leaf and must import nothing of ours.\ngot:\n%s\n' "$got"
		status=1
	fi
	return $status
}

# anchors_check verifies the path:line citations in the reviewer briefings. They are
# hand-maintained — about seventy-six of them — and every one moves when the file above it
# changes, so a briefing decays quietly into confident nonsense. The reviewers are told to
# open a file by line range rather than whole, which makes a stale anchor worse than an
# absent one: it sends them to the wrong lines and they report on what they find there.
#
# It checks exactly one thing: the file exists and every line named is inside it. Two
# richer checks were tried against the tree and rejected:
#
#   - Whether the anchored line looks like code. A blank-or-closing-brace heuristic fires
#     on 20 of the 76 anchors that are correct: a range legitimately ends on the closing
#     brace of the block it spans, and wsd/device.go:4-5 is deliberately the blank line
#     before `package`, which is the licence-spacing rule it exists to point at. A check
#     wrong a quarter of the time costs the reviewers their trust in checks.txt, which is
#     the one artefact all six are told to believe.
#   - The continuation form, a bare `:173,185` meaning "the file named in the prose above".
#     Resolving it means tracking prose context. Those anchors are NOT covered here.
#
# The range check alone is not enough, and that was learned the hard way: inserting twenty
# lines into wsd/listen.go silently moved eight anchors onto the wrong statements while
# every range stayed inside the file, so this reported PASS on briefings that had just
# become wrong. Hence the second half — when changed.txt is present, every anchor into a
# file the diff touched is listed for re-verification. It cannot tell a moved anchor from a
# still-correct one, so it advises rather than fails; the reviewers are told to check an
# anchor before citing it, and this is the list to check.
anchors_check() {
	local status=0 doc anchor file range first last total suspect=""

	for doc in .claude/agents/*.md .claude/PANEL.md; do
		[ -f "$doc" ] || continue
		# Backticked path.go:N or path.go:N-M. The backticks are what keeps prose out.
		for anchor in $(grep -o '`[A-Za-z0-9_/.-]\+\.go:[0-9]\+\(-[0-9]\+\)\?`' "$doc" |
			tr -d '`' | sort -u); do
			file="${anchor%%:*}"
			range="${anchor##*:}"
			first="${range%%-*}"
			last="${range##*-}"

			if [ ! -f "$file" ]; then
				printf '%s cites %s, and no such file exists\n' "$doc" "$anchor"
				status=1
				continue
			fi

			total=$(wc -l <"$file")
			if [ "$first" -gt "$total" ] || [ "$last" -gt "$total" ]; then
				printf '%s cites %s, but %s has only %d lines\n' \
					"$doc" "$anchor" "$file" "$total"
				status=1
			fi

			# A line number into a file this diff edited is suspect even when it still
			# resolves: inserting above it moves what it points at, not where it points.
			if [ -f "$out_dir/changed.txt" ] && grep -qxF "$file" "$out_dir/changed.txt"; then
				suspect="$suspect$doc cites $anchor, and this diff changes $file — re-verify it points at what the prose claims"$'\n'
			fi
		done
	done

	if [ -n "$suspect" ]; then
		printf 'Anchors into files this diff changes, to re-verify by hand:\n%s' "$suspect"
	fi
	return $status
}

printf 'Checks run by .claude/panel-prep.sh at %s\n\n' "$(date -Is)" >>"$out"

run 'gofmt -l .' gofmt_check
run 'go vet ./...' go vet ./...
run 'go build ./...' go build ./...
run 'go test -race ./...' go test -race ./...
run 'wsd dependency boundary' deps_check
run 'briefing anchors resolve' anchors_check

# The CLI smoke tests reach the link, so they are not gated here. AGENTS.md runs them by
# hand for the same reason CI does not: no runner has a device to answer a Probe.
{
	printf '== CLI smoke test: NOT RUN\n'
	printf 'go run ./bin/wsdc discover lo, and with no interface, need a device on the link.\n'
	printf 'Run them by hand before claiming a CLI change works.\n\n'
} >>"$out"

if [ $failed -eq 0 ]; then
	printf 'VERDICT: every gated check passed.\n' >>"$out"
else
	printf 'VERDICT: at least one gated check FAILED; see above.\n' >>"$out"
fi

cat "$out"
exit $failed
