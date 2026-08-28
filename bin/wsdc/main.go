// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// SPDX-License-Identifier: MIT

// Command wsdc discovers WS-Discovery devices on a network interface.
//
//	wsdc discover eth0        probe the link and print what answers
//	wsdc listen eth0          print Hello and Bye until interrupted
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/jfsmig/go-wsd/wsd"
)

func main() {
	var (
		timeout = flag.Duration("timeout", wsd.DefaultProbeTimeout, "collection window for a probe")
		oasis   = flag.Bool("oasis11", false, "use OASIS WS-Discovery 1.1 instead of the ONVIF dialect")
		all     = flag.Bool("all", false, "keep devices that do not advertise an ONVIF port type")
		types   = flag.String("types", "", "comma-separated port types to probe for; empty probes for every Target Service")
	)
	flag.Usage = usage
	flag.Parse()

	if flag.NArg() != 2 {
		usage()
		os.Exit(2)
	}
	command, iface := flag.Arg(0), flag.Arg(1)

	portTypes, err := parseTypes(*types)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wsdc: %v\n", err)
		os.Exit(2)
	}

	opts := wsd.ProbeOptions{Timeout: *timeout, IncludeNonOnvif: *all, PortTypes: portTypes}
	if *oasis {
		opts.Flavor = wsd.FlavorOASIS11
	}

	// Interrupting is the normal way to end a listen, so it must not look like a crash.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch command {
	case "discover":
		err = discover(ctx, iface, opts)
	case "listen":
		err = listen(ctx, iface)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "wsdc: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage: wsdc [flags] discover|listen <interface>\n\nflags:\n")
	flag.PrintDefaults()
	fmt.Fprintf(os.Stderr, "\nport types for -types:\n  %s\n",
		strings.Join(wsd.WellKnownTypeNames(), ", "))
	fmt.Fprintf(os.Stderr, "  or an explicit {namespace}LocalName, the form printed for a discovered type\n")
	fmt.Fprintf(os.Stderr, "\nNaming several narrows the probe rather than widening it: WS-Discovery matches\n"+
		"types conjunctively, so a device must implement all of them to answer.\n")
}

// parseTypes turns the -types list into port types. An empty list is not an error: it is
// the untyped probe, which reaches every Target Service on the link.
func parseTypes(raw string) ([]wsd.TypeName, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var out []wsd.TypeName
	for _, field := range strings.Split(raw, ",") {
		if strings.TrimSpace(field) == "" {
			continue
		}
		parsed, err := wsd.ParseTypeName(field)
		if err != nil {
			return nil, err
		}
		out = append(out, parsed)
	}
	return out, nil
}

// discover probes once and prints the devices that answered.
func discover(ctx context.Context, iface string, opts wsd.ProbeOptions) error {
	devices, err := wsd.Discover(ctx, iface, opts)
	if err != nil {
		return err
	}
	// Finding nothing is a result, not a failure: say so rather than exiting silently.
	if len(devices) == 0 {
		fmt.Fprintln(os.Stderr, "no device answered")
		return nil
	}
	for _, device := range devices {
		fmt.Printf("%s\t%s\n", orDash(device.UUID), orDash(device.DeviceServiceURL))
	}
	return nil
}

// listen prints announcements until the context is done.
func listen(ctx context.Context, iface string) error {
	announcements, err := wsd.Listen(ctx, iface)
	if err != nil {
		return err
	}
	for announcement := range announcements {
		// Bye usually carries no address, so the endpoint reference is all there is.
		fmt.Printf("%s\t%-5s\t%s\t%s\t%s\n",
			time.Now().Format(time.RFC3339),
			announcement.Kind,
			announcement.Device.From,
			orDash(announcement.Device.UUID),
			orDash(announcement.Device.DeviceServiceURL))
	}
	return nil
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
	return strings.Map(func(r rune) rune {
		if !unicode.IsGraphic(r) {
			return '\uFFFD'
		}
		return r
	}, value)
}
