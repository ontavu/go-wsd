// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// SPDX-License-Identifier: MIT

package wsd

import (
	"os"
	"regexp"
	"strconv"
	"testing"
	"time"
)

// readmePath is relative to the package directory, which is where go test runs.
const readmePath = "../README.md"

// readmeClaim is one number README.md states about this package, the pattern that finds
// every place it is stated, and the value the code currently gives it.
type readmeClaim struct {
	what string
	re   *regexp.Regexp
	want string
}

// TestREADMEDocumentsProbeOptionDefaults pins the "Probe options" table and the inline
// bounds of README.md against the exported constants of discover.go.
//
// The table once gave Timeout a 5s default while DefaultProbeTimeout was 3s. It was not
// wrong when written; a later change moved the constant and left the row behind, and the
// row is what a caller reads instead of the code. Nothing in CI noticed, because nothing
// in CI reads prose — so the comparison belongs here, where a changed default fails the
// build until the table follows it.
//
// Durations are compared as time.Duration and never as strings: (90*time.Second).String()
// is "1m30s", and README.md is right to write 90s.
func TestREADMEDocumentsProbeOptionDefaults(t *testing.T) {
	readme := readREADME(t)

	for _, c := range []readmeClaim{
		{"the Timeout row of the Probe options table", tableRow("Timeout"), DefaultProbeTimeout.String()},
		{"the MatchTimeout floor quoted beside it", quotedBeside("MatchTimeout"), MatchTimeout.String()},
		{"the MaxProbeTimeout ceiling, everywhere it is quoted", quotedBeside("MaxProbeTimeout"), MaxProbeTimeout.String()},
	} {
		for _, stated := range statements(t, readme, c) {
			got, err := time.ParseDuration(stated)
			if err != nil {
				t.Errorf("%s reads %q, which is not a duration: %v", c.what, stated, err)
				continue
			}
			if want, _ := time.ParseDuration(c.want); got != want {
				t.Errorf("%s says %q; the code says %v. Update README.md or the constant.",
					c.what, stated, want)
			}
		}
	}

	for _, c := range []readmeClaim{
		{"the Attempts row of the Probe options table", tableRow("Attempts"), strconv.Itoa(DefaultProbeAttempts)},
		{"the HopLimit row of the Probe options table", tableRow("HopLimit"), strconv.Itoa(DefaultHopLimit)},
	} {
		for _, stated := range statements(t, readme, c) {
			if stated != c.want {
				t.Errorf("%s says %q; the code says %q. Update README.md or the constant.",
					c.what, stated, c.want)
			}
		}
	}
}

// tableRow matches the Default cell of one row of the "Probe options" table, which names
// its field in backticks in the first cell.
func tableRow(field string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^\|\s*` + "`" + regexp.QuoteMeta(field) + "`" + `\s*\|\s*([^|]+?)\s*\|`)
}

// quotedBeside matches the parenthesised value README.md puts after a constant it names,
// as in "Lowered to `MaxProbeTimeout` (90s) if higher". Both bounds are stated more than
// once, and every statement is checked: the prose under "Untrusted input" repeats the
// ceiling the table already gives.
func quotedBeside(constant string) *regexp.Regexp {
	return regexp.MustCompile("`" + regexp.QuoteMeta(constant) + "`" + `\s*\(([^)]+)\)`)
}

// statements returns every value README.md states for one claim. Finding none is a
// failure in itself: a pattern that stops matching means the sentence carrying the number
// was rewritten, and this test would otherwise pass by checking nothing.
func statements(t *testing.T, readme string, c readmeClaim) []string {
	t.Helper()

	found := c.re.FindAllStringSubmatch(readme, -1)
	if len(found) == 0 {
		t.Errorf("README.md no longer states %s; this test cannot see it any more", c.what)
		return nil
	}

	stated := make([]string, 0, len(found))
	for _, m := range found {
		stated = append(stated, m[1])
	}
	return stated
}

func readREADME(t *testing.T) string {
	t.Helper()

	body, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("reading %s: %v", readmePath, err)
	}
	return string(body)
}
