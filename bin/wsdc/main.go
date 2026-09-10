// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// SPDX-License-Identifier: MIT

// Command wsdc discovers WS-Discovery devices on the interfaces named, or on every
// interface a device can plausibly answer on when none is named.
//
//	wsdc discover                probe every probeable interface, in parallel
//	wsdc discover eth0 wlan0     probe only these two
//	wsdc listen                  print Hello and Bye until interrupted
//	wsdc completion bash         a shell completion script, on stdout
//
// The four probe flags configure a Probe, so they belong to discover and follow it:
// wsdc discover --types onvif-nvt eth0. Listen sends no Probe and takes none of them.
// --all-interfaces widens the automatic interface set and is unrelated to --all, which
// keeps devices advertising no ONVIF port type.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/jfsmig/go-wsd/wsd"
)

// The two cobra verbs this command has to name. Cobra keeps its own unexported copies,
// and getting either wrong would silently stop claimCompletionCmd from claiming anything.
const (
	compCmdName = "completion"
	helpCmdName = "help"
)

func main() {
	// Interrupting is the normal way to end a listen, so it must not look like a crash.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	// Called rather than deferred: os.Exit skips defers, so a deferred stop would read as
	// a release that never happens. Nothing here holds a descriptor, but the next line
	// added to it might.
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run executes one command tree against one pair of streams and returns the exit status,
// which is part of this command's interface: 2 for a wrong command line, 1 for a failure
// at runtime, and 0 otherwise — including finding no device. It is separate from main so
// that a test can read the status and both streams without spawning a process.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	root := newRootCmd(stdout, stderr)
	// Cobra reads os.Args[1:] when SetArgs is given nil, which under "go test" is the
	// test binary's own flags — it exempts only a binary named cobra.test (cobra 1.10.2
	// command.go:1104-1107). An empty slice is what a bare wsdc means.
	if args == nil {
		args = []string{}
	}
	root.SetArgs(args)

	// ExecuteContextC rather than ExecuteContext: on a wrong command line the usage block
	// to print is the offending command's, and that is not always the root.
	cmd, err := root.ExecuteContextC(ctx)
	if err == nil {
		return 0
	}

	// The tree silences cobra's own reporting, so this is the only place an error reaches
	// a stream. The diagnostic keeps the "wsdc: " prefix, and neither it nor the usage
	// block is data, so both go to stderr whatever stdout is being piped into.
	//
	// The message can quote an argv element verbatim: pflag interpolates the offending
	// word into "unknown flag: --%s" and its two relatives with no escaping (pflag v1.0.9
	// flag.go:978,996,1035). An argument is not a datagram, but it is not always typed
	// either — a wrapper interpolating a name from an inventory hands those bytes here,
	// and a name starting with a dash is read as a flag. So it is scrubbed for the reason
	// orDash scrubs a device field, and in the same way. The usage block needs no filter:
	// it is built from Use, Short, Long and Example, which are ours.
	fmt.Fprintf(stderr, "wsdc: %s\n", graphicOnly(err.Error()))
	var wrong usageError
	if errors.As(err, &wrong) {
		if wrong.usage {
			fmt.Fprintf(stderr, "\n%s", cmd.UsageString())
		}
		return 2
	}
	// __complete and __completeNoDesc are cobra's protocol for the generated shell
	// scripts, and they validate their own arguments with a plain error, which would
	// otherwise read here as a probe that failed. They cannot be reached any earlier:
	// cobra builds them inside Execute and removes them again unless one is the verb being
	// run (cobra 1.10.2 completions.go:230-300, the removal at :298). A shell always
	// passes a command line; a human typing the verb bare still typed a wrong one.
	if cmd.Name() == cobra.ShellCompRequestCmd || cmd.Name() == cobra.ShellCompNoDescRequestCmd {
		return 2
	}
	return 1
}

// usageError marks a wrong command line, as opposed to a link or a socket that failed.
// The two are distinct exit statuses because a script branches on them, and cobra reports
// both as a plain error, so the distinction has to travel inside the error.
//
// usage says whether the usage block is the remedy. A wrong verb, a wrong argument count
// or an unknown flag means the reader does not know the command line; a rejected value —
// a --types name, a help topic — means they do, and the block would only bury the line
// naming what is accepted.
type usageError struct {
	err   error
	usage bool
}

func (e usageError) Error() string { return e.err.Error() }

func (e usageError) Unwrap() error { return e.err }

// usagef reports a wrong command line whose remedy is the usage block.
func usagef(format string, args ...any) error {
	return usageError{err: fmt.Errorf(format, args...), usage: true}
}

// newRootCmd builds the command tree on one pair of streams. The streams are arguments
// rather than something the caller sets afterwards because the completion group captures
// the writer once, when it is built (cobra 1.10.2 completions.go:798), and hands that one
// copy to each generator: a tree built before SetOut would generate "wsdc completion bash"
// to os.Stdout for ever after. claimCompletionCmd therefore comes last, below.
//
// It is a constructor rather than package variables, and that freshness is load-bearing
// twice over. A cobra command holds the state of one parse:
// the flag values, and the context. Cobra hands the context to a child only while the
// child's own is nil (cobra 1.10.2 command.go:1145) and never clears it, so a tree that
// outlives one run gives the next run the previous context — SIGINT would then cancel a
// context nobody watches, and wsd.Listen holds one socket and one group membership per IP
// family until the context it was given ends. A package-level tree, which is what
// cobra-cli scaffolds, is the whole regression.
func newRootCmd(stdout, stderr io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:   "wsdc discover|listen [interface...]",
		Short: "Discover WS-Discovery devices on the network interfaces of a host",
		Long: "wsdc probes a link for WS-Discovery devices, or watches it for the Hello and Bye\n" +
			"a device multicasts when it joins or leaves. Name no interface and every one a\n" +
			"device can plausibly answer on is used.\n" +
			"\n" + portTypeHelp(),
		// Every row shows a flag behind its verb. That is not decoration: a flag in front
		// of the verb fails, and a boolean fails without a hint, since cobra guesses the
		// unknown --all or --all-interfaces consumes the next word and so never sees
		// discover at all. Both booleans appear here for that reason.
		Example: "  wsdc discover                          # every interface a device can answer on\n" +
			"  wsdc discover eth0 wlan0               # only these two, in parallel\n" +
			"  wsdc discover --types onvif-nvt eth0   # only ONVIF video transmitters\n" +
			"  wsdc discover --all eth0               # keep non-ONVIF devices too\n" +
			"  wsdc discover --all-interfaces         # container and VM interfaces too\n" +
			"  wsdc listen                            # Hello and Bye until interrupted",
		// "wsdc [flags]" is not an invocation: the flags belong to discover.
		DisableFlagsInUseLine: true,
		// run is the only writer of either stream and the owner of the exit status. Cobra
		// printing an "Error:" line of its own would duplicate the diagnostic, and its
		// usage block goes to stdout, in the way of a pipe.
		SilenceErrors: true,
		SilenceUsage:  true,
		// Args has to be set for a wrong verb to arrive typed: cobra reports that case
		// from Find, through legacyArgs, only while Args is nil (cobra 1.10.2
		// args.go:35-37), and that error reaches a stream before run can classify it.
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return usagef("unknown command %q", args[0])
			}
			return nil
		},
		// A bare wsdc is how the tool is discovered, and it was a wrong command line
		// before: keep the status. RunE also has to exist for Args to run at all, since
		// cobra sends a command it cannot run straight to its help, with status 0
		// (cobra 1.10.2 command.go:955).
		RunE: func(cmd *cobra.Command, args []string) error {
			return usagef("a command is required")
		},
	}
	// Setting Args above costs cobra its own unknown-command detection, and the generated
	// help command depends on it: Find reports nothing once the command it stopped at
	// declares Args, so "wsdc help bogus" resolved to the root and printed the root help
	// with status 0 — a mistyped verb reported as a successful help request. This one asks
	// whether Find consumed the topic instead, so a wrong topic is a wrong command line
	// like any other.
	root.SetHelpCommand(&cobra.Command{
		Use:   helpCmdName + " [command]",
		Short: "Help about any command",
		RunE: func(cmd *cobra.Command, args []string) error {
			target, rest, err := cmd.Root().Find(args)
			if err != nil || len(rest) > 0 {
				// A wrong topic is a rejected argument, not a form the reader got wrong,
				// so it is answered the way a wrong --types value is: name what is
				// accepted, and leave out the usage block that would bury the list.
				return usageError{err: fmt.Errorf("unknown help topic %q: try one of %s",
					strings.Join(args, " "), strings.Join(childNames(cmd.Root()), ", ")), usage: false}
			}
			return target.Help()
		},
	})
	root.SetHelpTemplate(helpTemplate)
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return usageError{err: err, usage: true}
	})
	root.AddCommand(newDiscoverCmd(), newListenCmd())
	root.SetOut(stdout)
	root.SetErr(stderr)
	// Last, for the writer the group captures — see above.
	claimCompletionCmd(root)
	return root
}

// claimCompletionCmd builds the completion group early and gives it something to run.
//
// Cobra builds it inside Execute (cobra 1.10.2 command.go:1113 calling
// completions.go:769), where it cannot be reached, and a group with nothing to run goes to
// its help with status 0 (command.go:955) — so "wsdc completion" and "wsdc completion
// no-such-shell" both answered a wrong command line with a success on stdout. Building it
// here claims the name, and Execute then finds one present and leaves it alone, so the
// group answers a missing or unknown shell the way the rest of the tree does. Its four
// children, which do the generating, are cobra's own.
//
// It has to run after the real verbs are added: with none of them present cobra treats the
// group as the only subcommand and removes it again unless it is being called
// (completions.go:782-796).
func claimCompletionCmd(root *cobra.Command) {
	root.InitDefaultCompletionCmd()
	completion, _, err := root.Find([]string{compCmdName})
	if err != nil || completion.Name() != compCmdName {
		return
	}
	needsShell := func(cmd *cobra.Command, args []string) error {
		// Reaching the group itself means no child matched the shell name.
		return usageError{err: fmt.Errorf("%s needs a shell: one of %s",
			compCmdName, strings.Join(childNames(cmd), ", "))}
	}
	completion.Args = needsShell
	// Cobra gives each generator Args: NoArgs (completions.go:825,862,890,912), whose
	// error is plain, so "wsdc completion bash junk" reported a wrong command line as a
	// probe that failed. This command owns the group's statuses now, children included.
	for _, shell := range completion.Commands() {
		accept := shell.Args
		if accept == nil {
			continue
		}
		shell.Args = func(cmd *cobra.Command, args []string) error {
			if err := accept(cmd, args); err != nil {
				return usageError{err: err, usage: true}
			}
			return nil
		}
	}
	// A group cobra cannot run goes to its help with status 0 (command.go:955), and that
	// check precedes ValidateArgs (command.go:968), so the group needs a RunE to reach
	// Args at all. Args rejects every call that gets this far, which makes this provably
	// unreachable until cobra stops validating first — do not delete it as dead.
	completion.RunE = needsShell
}

// helpTemplate prints Long after the usage block rather than before it, which is the order
// the hand-written usage() had: synopsis, then the commands and the flags, then the notes.
// Cobra's default puts Long first, which buries the command list under the port type list
// and moves the conjunctive-matching warning off the last line, where it is most likely to
// be read. Children inherit the template from the root.
const helpTemplate = `{{if or .Runnable .HasSubCommands}}{{.UsageString}}{{end}}
{{with (or .Long .Short)}}{{. | trimTrailingWhitespaces}}
{{end}}`

// childNames lists the subcommands a command publishes: the topics "wsdc help" accepts
// when asked of the root, and the shells "wsdc completion" accepts when asked of the
// completion group. help is named explicitly because cobra does not count it as available.
func childNames(parent *cobra.Command) []string {
	var names []string
	for _, cmd := range parent.Commands() {
		if cmd.IsAvailableCommand() || cmd.Name() == helpCmdName {
			names = append(names, cmd.Name())
		}
	}
	return names
}

// portTypeHelp is the part of the help no flag description can carry: the names --types
// accepts, and the warning that naming several narrows the probe. Matching is conjunctive,
// so that paragraph is what stops a reader from discovering nothing and blaming the link.
// Both the root and discover print it, because the tool is found by typing wsdc and the
// flag is documented by "wsdc discover --help".
func portTypeHelp() string {
	return fmt.Sprintf("Port types for --types:\n  %s\n"+
		"  or an explicit {namespace}LocalName, the form printed for a discovered type\n"+
		"\n"+
		"Naming several narrows the probe rather than widening it: WS-Discovery matches\n"+
		"types conjunctively, so a device must implement all of them to answer.",
		strings.Join(wsd.WellKnownTypeNames(), ", "))
}

// orDash prepares a field of a device for a tab-separated row: a dash when it is
// legitimately absent, and nothing that can escape the row when it is not.
//
// The value was advertised by an unauthenticated host on the link. XML parsing already
// refuses a raw escape byte and a character reference to one, but a tab and a newline are
// legal character data, and either forges a row here. Anything not printable is replaced
// rather than dropped, so a field cannot be made to read as a different one, and that
// covers the format characters too: U+202E and its relatives reorder a line on a terminal
// without contributing a glyph.
func orDash(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return graphicOnly(value)
}

// graphicOnly replaces every rune that contributes no glyph with U+FFFD. It is the whole
// of the terminal boundary: orDash applies it to a field a device advertised, and run to a
// diagnostic, which can quote a word the caller's own argv supplied.
func graphicOnly(value string) string {
	return strings.Map(func(r rune) rune {
		if !unicode.IsGraphic(r) {
			return '\uFFFD'
		}
		return r
	}, value)
}
