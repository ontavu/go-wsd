// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"io"
	"net/netip"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/jfsmig/go-wsd/wsd"
)

// TestOrDashKeepsHostileFieldsInTheirColumn is the regression guard for the row this
// command prints. Every field but the timestamp and the sender comes from an
// unauthenticated datagram, and the row is tab-separated, so a tab or a newline in a
// UUID forges an entry that never existed.
//
// XML parsing already refuses a raw escape byte and a character reference to one, which
// rules out colour and cursor sequences; what it lets through is ordinary character data,
// and that is enough.
func TestOrDashKeepsHostileFieldsInTheirColumn(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  string
	}{
		{"absent", "", "-"},
		{"blank", " \t\n ", "-"},
		{"ordinary uuid", "urn:uuid:cam-1", "urn:uuid:cam-1"},
		{"ordinary url", "http://10.0.0.1/onvif/device_service", "http://10.0.0.1/onvif/device_service"},
		{"spaces are kept", "urn:uuid:a b", "urn:uuid:a b"},

		{"forged column", "urn:uuid:a\tHello", "urn:uuid:a�Hello"},
		{"forged row", "urn:uuid:a\n2026-01-01T00:00:00Z", "urn:uuid:a�2026-01-01T00:00:00Z"},
		{"carriage return", "urn:uuid:a\rurn:uuid:b", "urn:uuid:a�urn:uuid:b"},
		{"escape byte", "urn:uuid:\x1b[31m", "urn:uuid:�[31m"},
		{"bell", "urn:uuid:a\x07", "urn:uuid:a�"},
		// A format character contributes no glyph but reorders the line around it, so a
		// hostile address can be made to read as a trusted one.
		{"right to left override", "http://10.0.0.1/‮gnp.eliforp", "http://10.0.0.1/�gnp.eliforp"},
		{"zero width space", "urn:uuid:a​b", "urn:uuid:a�b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := orDash(tc.value); got != tc.want {
				t.Errorf("orDash(%q) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

// TestParseTypes covers the -types flag. An empty list is the untyped probe, which is
// the default and reaches every Target Service; a wrong name has to be reported rather
// than silently narrowing the probe to nothing.
func TestParseTypes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		raw     string
		want    []wsd.TypeName
		wantErr bool
	}{
		{"empty is the untyped probe", "", nil, false},
		{"blank is the untyped probe", "  ", nil, false},
		{"one well-known name", "onvif-nvt",
			[]wsd.TypeName{wsd.TypeONVIFNetworkVideoTransmitter}, false},
		{"several", "onvif-nvt,printer",
			[]wsd.TypeName{wsd.TypeONVIFNetworkVideoTransmitter, wsd.TypeWindowsPrinter}, false},
		{"spaces around the commas", " onvif-nvt , onvif-device ",
			[]wsd.TypeName{wsd.TypeONVIFNetworkVideoTransmitter, wsd.TypeONVIFDevice}, false},
		{"trailing comma", "onvif-nvt,",
			[]wsd.TypeName{wsd.TypeONVIFNetworkVideoTransmitter}, false},
		{"explicit pair", "{urn:x}Thing", []wsd.TypeName{{Namespace: "urn:x", Local: "Thing"}}, false},
		{"mixed forms", "onvif-nvt,{urn:x}Thing",
			[]wsd.TypeName{wsd.TypeONVIFNetworkVideoTransmitter, {Namespace: "urn:x", Local: "Thing"}}, false},

		{"unknown name", "camera", nil, true},
		{"one bad among good", "onvif-nvt,camera", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseTypes(tc.raw)
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseTypes(%q) err = %v, wantErr %v", tc.raw, err, tc.wantErr)
			}
			if err != nil {
				return
			}
			if len(got) != len(tc.want) {
				t.Fatalf("parseTypes(%q) = %v, want %v", tc.raw, got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("parseTypes(%q)[%d] = %v, want %v", tc.raw, i, got[i], tc.want[i])
				}
			}
		})
	}
}

// runWSDC drives the command tree the way main does, but against buffers, so a test can
// read the exit status and both streams without spawning a process. A variadic call with
// no argument gives a nil slice, which run turns into the empty one cobra needs — that is
// where the reason is written down.
//
// The context is bounded rather than context.Background(): listen ranges over the channel
// wsd.Listen returns until it closes, and that only happens once the context is done, so a
// row naming a resolvable interface would hang the test binary until go test's timeout, and
// only on a machine whose loopback accepts a multicast listener. Every row is meant to be
// rejected before it reaches a socket; the deadline is what keeps a row that stops being
// rejected from becoming a hang instead of a failure.
func runWSDC(args ...string) (code int, stdout, stderr string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var out, diag bytes.Buffer
	code = run(ctx, args, &out, &diag)
	return code, out.String(), diag.String()
}

// TestExitStatusSeparatesUsageFromRuntime pins the two documented statuses. They were two
// os.Exit calls in main under the flag package; under cobra every wrong command line comes
// back as an error instead, and only the usageError wrapper tells 2 from 1 — a plain error
// escaping unwrapped would report a typo as a link that failed.
//
// Every row is hermetic. The status-1 row relies on wsd.Discover resolving the interface
// name before it opens anything (wsd/discover.go:280), which is the only route to a
// runtime failure that needs no link.
func TestExitStatusSeparatesUsageFromRuntime(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		want   int
		stderr string
	}{
		{"no argument", nil, 2, "a command is required"},
		{"unknown command", []string{"probe", "lo"}, 2, `unknown command "probe"`},
		{"no interface", []string{"discover"}, 2, "discover takes exactly one interface name"},
		{"two interfaces", []string{"discover", "lo", "eth0"}, 2, "discover takes exactly one interface name"},
		{"listen with no interface", []string{"listen"}, 2, "listen takes exactly one interface name"},
		{"unknown flag", []string{"discover", "--flavor", "lo"}, 2, "unknown flag: --flavor"},
		{"flag without a value", []string{"discover", "--timeout"}, 2, "needs an argument"},
		{"bad port type", []string{"discover", "--types", "camera", "lo"}, 2, "unknown port type"},

		// pflag has no single-dash long form, so the spelling every transcript written
		// against the flag package used is read as a cluster of shorthands.
		{"single dash long flag", []string{"discover", "-types", "onvif-nvt", "lo"}, 2, "unknown shorthand flag"},
		// The four flags describe a Probe, so they belong to discover and follow it. One
		// taking a value still reaches it from in front, because cobra looks for the verb
		// past the flags and cannot know that an unknown --types consumes the next word;
		// this row proves it by having the value refused. A boolean has no such luck: the
		// same guess eats the verb, and the flag is then unknown to the root. Documenting
		// only the form that follows the subcommand is what keeps the two consistent.
		{"flag with a value before the subcommand", []string{"--types", "camera", "discover", "lo"}, 2, "unknown port type"},
		{"boolean flag before the subcommand", []string{"--all", "discover", "lo"}, 2, "unknown flag: --all"},
		// listen sends no Probe. The flag package accepted all four here and ignored them,
		// which cost a run and a reread of the output to notice.
		{"probe flag on listen", []string{"listen", "--timeout", "5s", "lo"}, 2, "unknown flag: --timeout"},

		{"unknown interface", []string{"discover", "no-such-interface"}, 1, "no such network interface"},

		{"help", []string{"--help"}, 0, ""},
		{"help of a subcommand", []string{"discover", "--help"}, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			code, stdout, stderr := runWSDC(tc.args...)
			elapsed := time.Since(start)
			// No row is meant to open a socket: the usage rows are rejected during
			// parsing, and the status-1 row fails at net.InterfaceByName before anything
			// is opened (wsd/discover.go:280). A probe on a real interface costs the whole
			// collection window, so the elapsed time is what tells a row that stopped
			// being rejected from one that never was.
			if elapsed > 250*time.Millisecond {
				t.Errorf("run(%q) took %v: it reached the network", tc.args, elapsed)
			}
			if code != tc.want {
				t.Errorf("run(%q) = %d, want %d (stderr %q)", tc.args, code, tc.want, stderr)
			}
			if tc.stderr != "" && !strings.Contains(stderr, tc.stderr) {
				t.Errorf("run(%q) stderr = %q, want it to contain %q", tc.args, stderr, tc.stderr)
			}
			// Stdout is the data stream: "wsdc discover eth0 | cut -f2" must never be fed
			// a diagnostic or a usage block.
			if tc.want != 0 && stdout != "" {
				t.Errorf("run(%q) wrote %q on stdout", tc.args, stdout)
			}
		})
	}
}

// TestWrongCommandLinePrintsTheUsageBlockOnStderr pins where an unrequested usage block
// goes, and that it carries the example that replaces what the reader typed: the flags
// moved behind the subcommand and lost their single-dash spelling in the same change, so
// the two transcripts that shipped for years both fail now.
//
// A rejected flag *value* is the exception, and the row for it is the reason the usage
// field on usageError exists: the message already lists every accepted port type, and
// fifteen lines of usage under it would only bury that.
func TestWrongCommandLinePrintsTheUsageBlockOnStderr(t *testing.T) {
	code, stdout, stderr := runWSDC("-types", "onvif-nvt", "discover", "lo")
	if code != 2 || stdout != "" {
		t.Fatalf("code = %d, stdout = %q, want 2 and nothing", code, stdout)
	}
	for _, want := range []string{"wsdc: ", "Usage:", "wsdc discover --types onvif-nvt eth0"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr does not contain %q:\n%s", want, stderr)
		}
	}

	code, stdout, stderr = runWSDC("discover", "--types", "camera", "lo")
	if code != 2 || stdout != "" {
		t.Fatalf("code = %d, stdout = %q, want 2 and nothing", code, stdout)
	}
	if strings.Contains(stderr, "Usage:") {
		t.Errorf("a rejected flag value should not drag the usage block along:\n%s", stderr)
	}
}

// TestHelpListsThePortTypeNames keeps the promise README.md makes with
// "wsdc -h  # lists the well-known names", on every route to the help text. The names come
// from wsd.WellKnownTypeNames, so one added to the library appears here with no change to
// this command, and the conjunctive-matching paragraph is the one piece of help text that
// stops a reader from silently discovering nothing.
//
// An explicit help request is the output that was asked for: status 0, and on stdout so
// that "wsdc --help | grep onvif" works. The flag package could only answer -h as a parse
// failure, on stderr with status 2.
func TestHelpListsThePortTypeNames(t *testing.T) {
	for _, args := range [][]string{{"-h"}, {"--help"}, {"help"}, {"discover", "--help"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			code, stdout, stderr := runWSDC(args...)
			if code != 0 {
				t.Fatalf("run(%q) = %d, want 0 (stderr %q)", args, code, stderr)
			}
			if stderr != "" {
				t.Errorf("run(%q) wrote %q on stderr; a help request is not a diagnostic", args, stderr)
			}
			for _, name := range wsd.WellKnownTypeNames() {
				if !strings.Contains(stdout, name) {
					t.Errorf("run(%q) does not name the port type %q", args, name)
				}
			}
			if !strings.Contains(stdout, "conjunctively") {
				t.Errorf("run(%q) drops the warning that naming several types narrows the probe:\n%s", args, stdout)
			}
		})
	}
}

// TestListenDeclaresNoProbeFlag records the split by reading the command tree rather than
// a command line, which is what catches a flag added to the root out of habit: listen
// would inherit it as a persistent flag and go back to accepting one it cannot honour.
func TestListenDeclaresNoProbeFlag(t *testing.T) {
	commands := map[string]*cobra.Command{}
	root := newRootCmd()
	for _, cmd := range root.Commands() {
		commands[cmd.Name()] = cmd
	}
	for _, name := range []string{"discover", "listen"} {
		if commands[name] == nil {
			t.Fatalf("the tree declares no %s command", name)
		}
	}
	for _, name := range []string{"timeout", "oasis11", "all", "types"} {
		if commands["discover"].Flags().Lookup(name) == nil {
			t.Errorf("discover has no --%s", name)
		}
		if commands["listen"].Flags().Lookup(name) != nil {
			t.Errorf("listen has --%s; a Probe flag on a command that sends none has no meaning", name)
		}
		if root.PersistentFlags().Lookup(name) != nil {
			t.Errorf("--%s is persistent on the root, so every subcommand inherits it", name)
		}
	}
}

// TestDiagnosticsKeepArgvOutOfTheControlChannel is the argv counterpart of
// TestOrDashKeepsHostileFieldsInTheirColumn. run is the single place an error reaches a
// stream, and pflag builds three of those errors by interpolating the offending word
// verbatim: "unknown flag: --%s", "flag needs an argument: %s" and "unknown shorthand
// flag: %q in -%s" (pflag v1.0.9 flag.go:978,996,1035). An argument is not a datagram, but
// it is not always typed either: a wrapper interpolating a name from a config or a device
// inventory hands those bytes to the terminal, and a name beginning with a dash is read as
// a flag. ESC [2K CR then erases the diagnostic and rewrites the line, and a newline forges
// a whole one.
//
// Our own messages use %q, which is sufficient on its own: strconv escapes every rune that
// is not unicode.IsPrint, a strictly smaller set than the unicode.IsGraphic orDash keeps.
// The rows assert the property rather than the wording, so a cobra upgrade that rephrases
// a message still has to keep it.
func TestDiagnosticsKeepArgvOutOfTheControlChannel(t *testing.T) {
	const forgery = "\x1b[2K\rwsdc: urn:uuid:trusted\thttp://10.0.0.1/onvif/device_service"
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"unknown long flag", []string{"discover", "--" + forgery, "lo"}},
		{"unknown shorthand cluster", []string{"discover", "-" + forgery, "lo"}},
		{"unknown command", []string{forgery, "lo"}},
		{"unknown help topic", []string{"help", forgery}},
		{"rejected flag value", []string{"discover", "--types", forgery, "lo"}},
		{"newline in a flag name", []string{"discover", "--x\nurn:uuid:a\tb", "lo"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runWSDC(tc.args...)
			if code != 2 || stdout != "" {
				t.Fatalf("code = %d, stdout = %q, want 2 and nothing", code, stdout)
			}
			// The usage block below the diagnostic is ours and static. The diagnostic
			// itself has to be one line: a newline in it forges a second.
			diagnostic := stderr
			if i := strings.Index(stderr, "\n\nUsage:"); i >= 0 {
				diagnostic = stderr[:i]
			}
			diagnostic = strings.TrimSuffix(diagnostic, "\n")
			if strings.Contains(diagnostic, "\n") {
				t.Fatalf("the diagnostic is more than one line: %q", diagnostic)
			}
			for _, r := range diagnostic {
				if !unicode.IsGraphic(r) {
					t.Fatalf("U+%04X reaches the terminal in %q", r, diagnostic)
				}
			}
		})
	}
}

// TestRowsKeepHostileFieldsInTheirColumns pins the two row shapes rather than the filter
// alone. TestOrDashKeepsHostileFieldsInTheirColumn proves orDash works; nothing proved that
// every column of a row goes through it, and both printers moved to new files in the cobra
// migration with their format strings carried over by inspection only. A field added to
// wsd.Device and printed raw would pass every other test in this package.
//
// The assertion is the column count and the absence of anything non-graphic, not the text:
// a row of a tab-separated table is what "wsdc discover eth0 | cut -f2" reads, and one
// forged row is one device that never answered.
func TestRowsKeepHostileFieldsInTheirColumns(t *testing.T) {
	// A tab forges a column, a newline a whole row, ESC a cursor move, U+202E reorders
	// what is left. Only the first two survive XML parsing, which is why they lead.
	const forgery = "urn:uuid:a\tb\nc\x1b[2K\u202e"
	hostile := wsd.Device{
		UUID:             forgery,
		DeviceServiceURL: forgery,
		Xaddr:            forgery,
		From:             netip.MustParseAddrPort("10.0.0.99:3702"),
	}
	for _, tc := range []struct {
		name    string
		columns int
		print   func(io.Writer)
	}{
		{"discover", 2, func(w io.Writer) { printDevice(w, hostile) }},
		{"listen", 5, func(w io.Writer) {
			printAnnouncement(w, time.Unix(0, 0).UTC(),
				wsd.Announcement{Kind: wsd.KindHello, Device: hostile, Types: []string{forgery}})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			tc.print(&out)
			row, ok := strings.CutSuffix(out.String(), "\n")
			if !ok {
				t.Fatalf("the row does not end in a newline: %q", out.String())
			}
			fields := strings.Split(row, "\t")
			if len(fields) != tc.columns {
				t.Fatalf("the row has %d columns, want %d: %q", len(fields), tc.columns, row)
			}
			for i, field := range fields {
				for _, r := range field {
					if !unicode.IsGraphic(r) {
						t.Errorf("column %d carries U+%04X: %q", i, r, field)
					}
				}
			}
		})
	}
}

// TestOnlyRequestedOutputReachesStdout is the other half of
// TestExitStatusSeparatesUsageFromRuntime, which looks at stdout only when the status is
// not 0. Stdout is the data stream, so the question is not just whether it is empty when
// the command fails but whether anything on it is something the reader asked for: a wrong
// command line answering 0 with a usage block on stdout is worse than one answering 2,
// because the pipe swallows it.
//
// Under the flag package no route to stdout existed at all — even "wsdc -h" printed to
// stderr. Cobra adds commands with a Run of their own, and a command it cannot run goes to
// its help with status 0, which is why the root declares a RunE and why the generated help
// command was replaced.
//
// One line escapes the rule and is left alone: "wsdc completion" with no shell named prints
// its own help on stdout with status 0, because the generated command is built inside
// Execute and cannot be constrained from here. That is stock cobra, the same in every tool
// built on it, and the verb is documented in README.md.
func TestOnlyRequestedOutputReachesStdout(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		wantStdout bool
	}{
		// Help was asked for: stdout, status 0, so that "wsdc --help | grep onvif" works.
		{"help flag", []string{"--help"}, true},
		{"help command", []string{"help"}, true},
		{"help of a subcommand", []string{"discover", "--help"}, true},
		{"a completion script", []string{"completion", "bash"}, true},

		// These are wrong command lines, and none of them is data.
		{"an unknown help topic", []string{"help", "no-such-command"}, false},
		{"an unknown command", []string{"no-such-command"}, false},
		{"a bare wsdc", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, stdout, _ := runWSDC(tc.args...)
			if (stdout != "") != tc.wantStdout {
				t.Errorf("run(%q) wrote %d bytes on stdout, want %v:\n%s",
					tc.args, len(stdout), tc.wantStdout, stdout)
			}
		})
	}
}

// TestHelpRejectsAnUnknownTopic pins the case setting Args on the root took away. Find
// reports nothing once the command it stopped at declares Args (cobra 1.10.2
// command.go:775-778), so the generated help command could no longer tell a topic it had
// never heard of from the root: "wsdc help nosuch" answered with the root help and status
// 0, reporting a mistyped verb as a successful help request.
func TestHelpRejectsAnUnknownTopic(t *testing.T) {
	for _, args := range [][]string{{"help", "nosuch"}, {"help", "discover", "nosuch"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			code, stdout, stderr := runWSDC(args...)
			if code != 2 || stdout != "" {
				t.Fatalf("code = %d, stdout = %q, want 2 and nothing", code, stdout)
			}
			if !strings.Contains(stderr, "unknown help topic") {
				t.Errorf("stderr = %q, want it to name the unknown topic", stderr)
			}
		})
	}
}

// TestBooleanFlagBeforeSubcommandTeachesTheFix guards the worse of the two flag-placement
// failures. A boolean discover flag ahead of the verb is swallowed as if it were the verb's
// value, so the root reports a bare unknown flag with no discover in sight; --types happens
// to survive that placement because it takes a value. Until the Examples block showed a
// boolean in a working position, the message alone gave no way back to a line that works.
func TestBooleanFlagBeforeSubcommandTeachesTheFix(t *testing.T) {
	code, stdout, stderr := runWSDC("--all", "discover", "lo")
	if code != 2 || stdout != "" {
		t.Fatalf("code = %d, stdout = %q, want 2 and nothing", code, stdout)
	}
	if !strings.Contains(stderr, "wsdc discover --all eth0") {
		t.Errorf("stderr does not show the corrected form for --all:\n%s", stderr)
	}
}

// TestEveryRunBuildsItsOwnCommandTree pins what carries cancellation. Cobra hands the
// context to a child only while the child's own is nil (cobra 1.10.2 command.go:1145) and
// never clears it, so a tree that outlives one run gives the next run the previous
// context: SIGINT would cancel a context nobody watches, and wsd.Listen holds one socket
// and one group membership per IP family until the context it was given ends
// (wsd/listen.go:63-67). A package-level tree, which is what cobra-cli scaffolds, is the
// whole regression.
func TestEveryRunBuildsItsOwnCommandTree(t *testing.T) {
	if newRootCmd() == newRootCmd() {
		t.Fatal("newRootCmd returns a shared tree; a second run would inherit the first run's context")
	}
	for _, cmd := range newRootCmd().Commands() {
		if cmd.Context() != nil {
			t.Errorf("%s carries a context before execution", cmd.Name())
		}
	}

	// The characterisation half: this is the cobra behaviour that makes freshness
	// load-bearing. If it ever changes, the guard above is no longer the thing to keep.
	root := newRootCmd()
	var seen []context.Context
	root.AddCommand(&cobra.Command{
		Use: "record",
		RunE: func(cmd *cobra.Command, args []string) error {
			seen = append(seen, cmd.Context())
			return nil
		},
	})
	for i := 0; i < 2; i++ {
		root.SetArgs([]string{"record"})
		if _, err := root.ExecuteContextC(context.WithValue(context.Background(), runKey{}, i)); err != nil {
			t.Fatal(err)
		}
	}
	if seen[1].Value(runKey{}) != 0 {
		t.Fatalf("cobra now overwrites a child's context on re-execution (second run saw %v); "+
			"the freshness checked above is no longer what carries cancellation", seen[1].Value(runKey{}))
	}
}

type runKey struct{}
