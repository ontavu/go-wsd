// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/ontavu/go-wsd/wsd"
)

// newListenCmd builds the watch verb, which declares none of the probe flags on purpose.
// Replies are matched on local element names, so one parser serves both dialects and there
// is no flavor to pick; nothing is collected, so there is no window to bound. Under the
// flag package all four were accepted here and silently ignored.
func newListenCmd() *cobra.Command {
	var allInterfaces bool

	cmd := &cobra.Command{
		Use:   "listen [interface...]",
		Short: "Print Hello and Bye until interrupted",
		Long: "Watch network interfaces and print a row for every Hello and Bye a device\n" +
			"multicasts, until interrupted. Interrupting is the normal way to stop, and the\n" +
			"status is 0.\n" +
			"\n" +
			"Name the interfaces to watch, or name none and every interface a device can\n" +
			"plausibly announce itself on is watched, by the same rule discover uses. The\n" +
			"streams are merged, so rows from several interfaces interleave in the order they\n" +
			"were read.\n" +
			"\n" +
			"The columns are tab-separated: the interface, the time the announcement was read,\n" +
			"its kind, the address it came from, the endpoint reference and the device service\n" +
			"URL. Only the interface and the address were observed; the rest is what its\n" +
			"sender chose to claim.",
		Example: "  wsdc listen\n" +
			"  wsdc listen eth0\n" +
			"  wsdc listen eth0 wlan0",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			names, skipped, err := interfacesToPoll(args, allInterfaces, net.Interfaces)
			if err != nil {
				return err
			}
			if len(names) == 0 {
				reportNothingToPoll(cmd.ErrOrStderr(), skipped)
				return nil
			}
			reportSelection(cmd.ErrOrStderr(), names, skipped)
			return listen(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), names, wsd.Listen)
		},
	}
	// Not --all: discover already spends that name on something else, and the two verbs
	// have to spell the same idea the same way.
	cmd.Flags().BoolVar(&allInterfaces, "all-interfaces", false, allInterfacesHelp)
	return cmd
}

// heard is one announcement and the interface it was read on, which is what the merge has
// to carry: the interface is a property of the stream, not of the datagram.
type heard struct {
	iface string
	// at is stamped where the datagram was read, not where the row is printed. The merge
	// is what made that distinction matter: a row crosses an unbuffered channel and waits
	// for the previous row's write to stdout, so stamping in the printing loop would put
	// the pipe's latency in the column and could invert two interfaces' read order.
	at           time.Time
	announcement wsd.Announcement
}

// watch is one open listener, kept beside the interface it belongs to so the forwarders
// can label what they read.
type watch struct {
	iface         string
	announcements <-chan wsd.Announcement
}

// listen merges the announcements of every interface and prints them until the context is
// done.
//
// Opening comes first and all at once, because an interface that cannot open a listener is
// worth reporting before any row is printed rather than in the middle of the stream. One
// interface failing must not lose the others, the rule discover applies too; the status is
// 1 only when none could be opened.
func listen(ctx context.Context, out, diag io.Writer, names []string, watchOn watcher) error {
	// A failure is collected rather than printed here, so that the one which is also
	// returned is not said twice.
	type failure struct {
		iface string
		err   error
	}
	watches := make([]watch, 0, len(names))
	var failures []failure
	for _, name := range names {
		announcements, err := watchOn(ctx, name)
		if err != nil {
			failures = append(failures, failure{iface: name, err: err})
			continue
		}
		watches = append(watches, watch{iface: name, announcements: announcements})
	}
	if len(watches) == 0 {
		if len(failures) == 0 {
			return nil
		}
		// Every interface failed, reported the way discover reports it: once, and named
		// when there was a choice about which one to name.
		if len(failures) == 1 {
			return failures[0].err
		}
		return fmt.Errorf("%s: %w", failures[0].iface, failures[0].err)
	}
	// One interface failing must not lose the others. Said here rather than returned, so
	// the rows from the interfaces that opened still reach stdout.
	for _, f := range failures {
		fmt.Fprintf(diag, "%s: %s\n", orDash(f.iface), graphicOnly(f.err.Error()))
	}

	// One forwarder per interface into one channel, and both of its selects watch ctx.
	// Without them this command's exit would rest on wsd.Listen's contract that every
	// source closes when the context is done: the range below ends only when merged
	// closes, which needs every forwarder to return, so one source that stayed open would
	// make wsdc ignore SIGINT and need a SIGKILL. Abandoning a source mid-send cannot
	// block its reader — readAnnouncements selects on ctx.Done() while sending — so the
	// only cost is a row already read, at shutdown, which is when the exit matters more.
	merged := make(chan heard)
	var wg sync.WaitGroup
	for _, w := range watches {
		wg.Go(func() {
			for {
				select {
				case announcement, ok := <-w.announcements:
					if !ok {
						return
					}
					select {
					case merged <- heard{iface: w.iface, at: time.Now(), announcement: announcement}:
					case <-ctx.Done():
						return
					}
				case <-ctx.Done():
					return
				}
			}
		})
	}
	go func() {
		wg.Wait()
		close(merged)
	}()

	for row := range merged {
		printAnnouncement(out, row.iface, row.at, row.announcement)
	}
	return nil
}

// printAnnouncement writes one row of the listen table. now is a parameter so that a test
// can pin the columns against a fixed timestamp.
//
// Kind is one of two constants and From is a netip.AddrPort, one of the two columns that
// were observed rather than claimed; neither can carry a tab. The interface is the other
// observed one, and takes orDash because it can come from argv. The last two were
// advertised by an unauthenticated host and go through orDash for that reason.
func printAnnouncement(out io.Writer, iface string, now time.Time, announcement wsd.Announcement) {
	// Bye usually carries no address, so the endpoint reference is all there is.
	fmt.Fprintf(out, "%s\t%s\t%-5s\t%s\t%s\t%s\n",
		orDash(iface),
		now.Format(time.RFC3339),
		announcement.Kind,
		announcement.Device.From,
		orDash(announcement.Device.UUID),
		orDash(announcement.Device.DeviceServiceURL))
}
