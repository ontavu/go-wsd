// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// SPDX-License-Identifier: MIT

package wsd

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot is relative to the package directory, which is where go test runs. It is the
// same reach as readme_test.go, which compares README.md against this package.
const repoRoot = ".."

// noticeLastLine ends the MIT notice every file carries.
const noticeLastLine = "// SPDX-License-Identifier: MIT"

// provenance is the extra line a file carries when it derives from someone else's work,
// keyed by the path AGENTS.md attributes it to. Every other file must carry none.
var provenance = map[string]string{
	"gosoap/soap-builder.go":      "github.com/jfsmig/onvif",
	"gosoap/soap-builder_test.go": "github.com/jfsmig/onvif",
	"gosoap/ws-security.go":       "github.com/jfsmig/onvif",
	"gosoap/ws-security_test.go":  "github.com/jfsmig/onvif",
	"wsd/discover.go":             "the ws-discovery project",
	"wsd/ws-discovery.go":         "the ws-discovery project",
}

// TestLicenceHeaderOnEveryGoFile pins the header rules of AGENTS.md over the whole tree.
//
// go-architect used to check them by running a grep and counting the answer to six. That
// is a rule nothing enforces between reviews, and every new file is a chance to break it:
// the notice is copied by hand, and which provenance line belongs depends on where the
// file lives.
//
// The blank-line rule is written as "the line after the notice is empty", never as "the
// line before package is empty". The second form reports wsd/doc.go,
// wsd/transport/transport.go and bin/wsdc/main.go, all three of which are correct — each
// has a legitimate package doc comment between the blank line and the package clause. The
// rule exists so that Go does not take the licence as the package doc, and a blank line
// after the notice is exactly what prevents that.
func TestLicenceHeaderOnEveryGoFile(t *testing.T) {
	seen := map[string]bool{}

	for _, path := range goFiles(t) {
		lines := strings.Split(readFile(t, path), "\n")

		notice := -1
		for i, line := range lines {
			if line == noticeLastLine {
				notice = i
				break
			}
		}
		if notice < 0 {
			t.Errorf("%s carries no MIT notice; copy the header from wsd/device.go", path)
			continue
		}
		if notice+1 >= len(lines) || strings.TrimSpace(lines[notice+1]) != "" {
			t.Errorf("%s has no blank line after the licence notice; without it Go takes "+
				"the licence as the package doc comment", path)
		}

		head := strings.Join(lines[:notice], "\n")
		derives := strings.Contains(head, "Portions of this file derive")
		want, expected := provenance[path]

		switch {
		case expected && !derives:
			t.Errorf("%s must carry the provenance line naming %s", path, want)
		case expected && !strings.Contains(head, want):
			t.Errorf("%s carries a provenance line, but not the one for its path (%s)", path, want)
		case !expected && derives:
			t.Errorf("%s carries a provenance line and AGENTS.md attributes none to it; "+
				"either the file derives from something and the table needs a row, or the "+
				"line was copied by mistake", path)
		}
		if expected {
			seen[path] = true
		}
	}

	for path := range provenance {
		if !seen[path] {
			t.Errorf("AGENTS.md attributes a provenance line to %s, which no longer exists "+
				"or no longer carries it", path)
		}
	}
}

// TestLibrariesImportNoLogger pins the AGENTS.md rule that a library here does not report
// to a logger its caller never chose.
//
// TestNoStandardLogger (gosoap/soap-builder_test.go) already proves gosoap stays silent,
// but it does so behaviourally and for one package only, while the rule covers every
// library in the tree. This is the cheap net beside it, not a replacement: an import is
// the earliest point the rule can be broken.
//
// bin/wsdc is excluded deliberately. It is a command, not a library, and writing to a
// terminal is its entire purpose.
func TestLibrariesImportNoLogger(t *testing.T) {
	for _, path := range goFiles(t) {
		if strings.HasPrefix(path, "bin/") || strings.HasSuffix(path, "_test.go") {
			continue
		}

		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(repoRoot, path), nil, parser.ImportsOnly)
		if err != nil {
			t.Errorf("parsing %s: %v", path, err)
			continue
		}
		for _, imported := range file.Imports {
			if imported.Path.Value == `"log"` || imported.Path.Value == `"log/slog"` {
				t.Errorf("%s imports %s; a library here does not print to a logger its "+
					"caller never chose", path, imported.Path.Value)
			}
		}
	}
}

// goFiles returns every .go file in the repository, as a path relative to its root.
// Finding none is a failure: these tests would otherwise pass by checking nothing.
func goFiles(t *testing.T) []string {
	t.Helper()

	var found []string
	err := filepath.Walk(repoRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && info.Name() == ".git" {
			return filepath.SkipDir
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		found = append(found, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository: %v", err)
	}
	if len(found) == 0 {
		t.Fatal("no .go file found; this test would pass by checking nothing")
	}
	return found
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(repoRoot, path))
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(body)
}
