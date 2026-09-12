// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// SPDX-License-Identifier: MIT

package main

import (
	"io"
	"os"
	"regexp"
	"sort"
	"testing"

	"github.com/spf13/pflag"
)

// readmePath is relative to the package directory, which is where go test runs.
const readmePath = "../../README.md"

// cobraOwnedFlags are declared by cobra rather than by this package, so README.md is
// under no obligation to list them.
var cobraOwnedFlags = map[string]bool{"help": true, "no-descriptions": true}

// readmeFlag finds every long flag README.md mentions.
var readmeFlag = regexp.MustCompile(`--[a-z][a-z0-9-]*`)

// flagList matches the paragraph under "Code organization" that enumerates the flags,
// from its "Flags:" lead-in to the blank line that ends it.
//
// The search is deliberately scoped to that paragraph rather than run over the whole
// file. --types was missing from the list while the "Port types" section above still
// documented it at length, so a file-wide search finds it and reports nothing — which is
// precisely the drift that went unnoticed.
var flagList = regexp.MustCompile(`(?s)\n\s*Flags: .*?\n\s*\n`)

// TestREADMEListsEveryFlagTheTreeRegisters pins the flag list under "Code organization"
// (README.md) against the command tree newRootCmd builds.
//
// The list omitted --types for a while although the section above documented it at
// length. Neither half was wrong when written; a flag was added to the tree and to one
// of the two places in README.md that name flags. The tree is the authority, so it is
// what this test reads — TestListenDeclaresNoProbeFlag already walks it for placement,
// and this walks it for documentation.
//
// It checks both directions. A flag the tree registers and README.md omits is
// undiscoverable to a reader of the documentation; a flag README.md names and the tree
// does not register sends them to type a command line that fails.
func TestREADMEListsEveryFlagTheTreeRegisters(t *testing.T) {
	readme := readREADME(t)

	registered := map[string]bool{}
	root := newRootCmd(io.Discard, io.Discard)
	for _, cmd := range root.Commands() {
		if cmd.Name() == "completion" || cmd.Name() == "help" {
			continue
		}
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			if !cobraOwnedFlags[f.Name] {
				registered[f.Name] = true
			}
		})
	}
	root.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		if !cobraOwnedFlags[f.Name] {
			registered[f.Name] = true
		}
	})

	if len(registered) == 0 {
		t.Fatal("the tree registers no flag of its own; this test would pass by checking nothing")
	}

	list := flagList.FindString(readme)
	if list == "" {
		t.Fatalf("README.md no longer carries a flag list under Code organization, "+
			"or it no longer begins with %q; this test cannot see it any more", "Flags: ")
	}

	for _, name := range sorted(registered) {
		// A word may not follow the name, or --all matches inside --all-interfaces.
		stated := regexp.MustCompile(`--` + regexp.QuoteMeta(name) + `([^a-z0-9-]|$)`)
		if !stated.MatchString(list) {
			t.Errorf("the tree registers --%s and the flag list under Code organization "+
				"does not name it; documenting it elsewhere is not the same thing", name)
		}
	}

	documented := map[string]bool{}
	for _, m := range readmeFlag.FindAllString(readme, -1) {
		documented[m[2:]] = true
	}
	for _, name := range sorted(documented) {
		if !registered[name] && !cobraOwnedFlags[name] {
			t.Errorf("README.md names --%s and the tree registers no such flag; "+
				"a reader following the documentation types a command line that fails", name)
		}
	}
}

func sorted(set map[string]bool) []string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func readREADME(t *testing.T) string {
	t.Helper()

	body, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("reading %s: %v", readmePath, err)
	}
	return string(body)
}
