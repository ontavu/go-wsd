// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/jfsmig/go-wsd/wsd"
)

// newListenCmd builds the watch verb, which declares no flag on purpose. Replies are
// matched on local element names, so one parser serves both dialects and there is no
// flavor to pick; nothing is collected, so there is no window to bound. Under the flag
// package all four probe flags were accepted here and silently ignored.
func newListenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "listen <interface>",
		Short: "Print Hello and Bye until interrupted",
		Long: "Watch one network interface and print a row for every Hello and Bye a device\n" +
			"multicasts, until interrupted. Interrupting is the normal way to stop, and the\n" +
			"status is 0.\n" +
			"\n" +
			"The columns are tab-separated: the time the announcement was read, its kind, the\n" +
			"address it came from, the endpoint reference and the device service URL. Only the\n" +
			"address was observed; the rest is what its sender chose to claim.",
		Example: "  wsdc listen eth0",
		Args:    exactlyOneInterface,
		RunE: func(cmd *cobra.Command, args []string) error {
			return listen(cmd.Context(), cmd.OutOrStdout(), args[0])
		},
	}
}

// listen prints announcements until the context is done.
func listen(ctx context.Context, out io.Writer, iface string) error {
	announcements, err := wsd.Listen(ctx, iface)
	if err != nil {
		return err
	}
	for announcement := range announcements {
		printAnnouncement(out, time.Now(), announcement)
	}
	return nil
}

// printAnnouncement writes one row of the listen table. now is a parameter so that a test
// can pin the columns against a fixed timestamp.
//
// Kind is one of two constants and From is a netip.AddrPort, the one column that was
// observed rather than claimed; neither can carry a tab. The last two were advertised by an
// unauthenticated host and go through orDash.
func printAnnouncement(out io.Writer, now time.Time, announcement wsd.Announcement) {
	// Bye usually carries no address, so the endpoint reference is all there is.
	fmt.Fprintf(out, "%s\t%-5s\t%s\t%s\t%s\n",
		now.Format(time.RFC3339),
		announcement.Kind,
		announcement.Device.From,
		orDash(announcement.Device.UUID),
		orDash(announcement.Device.DeviceServiceURL))
}
