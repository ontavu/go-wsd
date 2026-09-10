// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/jfsmig/go-wsd/wsd"
)

// newDiscoverCmd builds the probe verb. The four flags are declared here rather than on
// the root because they describe a Probe, and only this command sends one: on the root
// they would reach listen, which cannot honour any of them.
func newDiscoverCmd() *cobra.Command {
	var (
		opts          wsd.ProbeOptions
		oasis         bool
		types         string
		allInterfaces bool
	)
	cmd := &cobra.Command{
		Use:   "discover [interface...]",
		Short: "Probe the link and print what answers",
		Long: "Probe network interfaces and print, tab-separated, the interface, the endpoint\n" +
			"reference and the device service URL of every device that answers within the\n" +
			"collection window. A field the device did not advertise is printed as a dash.\n" +
			"\n" +
			"Name the interfaces to probe, or name none and every interface a device can\n" +
			"plausibly answer on is probed: up, not the loopback, multicast-capable and not a\n" +
			"container, VM or overlay device. A name given explicitly is probed whatever the\n" +
			"filter would have said about it. Several interfaces are probed in parallel, so\n" +
			"the run costs one collection window per IP family rather than one per interface,\n" +
			"and a device answering on two of them prints one row per interface.\n" +
			"\n" +
			"Finding nothing is a result rather than a failure, and the status stays 0. So is\n" +
			"one interface failing while another answers, and so is interrupting; the status\n" +
			"is 1 only when every interface failed for a reason of its own.\n" +
			"\n" + portTypeHelp(),
		Example: "  wsdc discover\n" +
			"  wsdc discover eth0\n" +
			"  wsdc discover eth0 wlan0\n" +
			"  wsdc discover --types onvif-nvt eth0\n" +
			"  wsdc discover --types '{urn:x}Thing' eth0",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// The library reads any Timeout at or below zero as "use the default", which
			// is the zero value's contract. A negative one is a typo rather than a
			// request, and answering it with a silent three-second probe hides that.
			//
			// A too-large value is deliberately not refused here: that is a request the
			// library cannot honour rather than a typo, so it is clamped there and the
			// flag description names the ceiling, instead of failing a script that asks
			// for five minutes.
			if opts.Timeout < 0 {
				return usageError{err: fmt.Errorf("--timeout must not be negative, got %s", opts.Timeout), usage: false}
			}
			// A misspelled port type would narrow the probe to nothing and read as an
			// empty link, so it is refused before a datagram is built. The error names
			// every accepted value, which the usage block would only bury.
			portTypes, err := parseTypes(types)
			if err != nil {
				return usageError{err: err, usage: false}
			}
			opts.PortTypes = portTypes
			if oasis {
				opts.Flavor = wsd.FlavorOASIS11
			}
			names, skipped, err := interfacesToPoll(args, allInterfaces, net.Interfaces)
			if err != nil {
				return err
			}
			if len(names) == 0 {
				reportNothingToPoll(cmd.ErrOrStderr(), skipped)
				return nil
			}
			reportSelection(cmd.ErrOrStderr(), names, skipped)
			return discover(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), names, opts, wsd.Discover)
		},
	}

	flags := cmd.Flags()
	flags.DurationVar(&opts.Timeout, "timeout", wsd.DefaultProbeTimeout,
		// Seconds rather than Duration.String(), which renders 1m30s: this is the one
		// place a reader meets the number, and README.md and wsd/doc.go say 90s.
		fmt.Sprintf("collection window for a probe, at most %ds", int(wsd.MaxProbeTimeout/time.Second)))
	flags.BoolVar(&oasis, "oasis11", false, "use OASIS WS-Discovery 1.1 instead of the ONVIF dialect")
	flags.BoolVar(&opts.IncludeNonOnvif, "all", false, "keep devices that do not advertise an ONVIF port type")
	flags.StringVar(&types, "types", "", "comma-separated port types to probe for; empty probes for every Target Service")
	// Not --all: that one is already taken, and means something else entirely.
	flags.BoolVar(&allInterfaces, "all-interfaces", false, allInterfacesHelp)
	return cmd
}

// probed is the outcome of probing one interface, kept so that the probes can run
// concurrently while the output stays in interface order. A result noun, unlike wsd's
// probe, which is the action.
type probed struct {
	iface   string
	devices []wsd.Device
	err     error
}

// discover probes every interface once, in parallel, and prints the devices that answered.
//
// In parallel because every probe waits out a whole collection window: in sequence the run
// would cost one window per interface, and on a host carrying a dozen virtual devices the
// context would expire before the real NIC had been reached. No channel and no mutex are
// needed for it — each goroutine writes only into its own probe, and wg.Wait is the edge
// before any of them is read.
func discover(ctx context.Context, out, diag io.Writer, names []string, opts wsd.ProbeOptions, send discoverer) error {
	// Never reached through RunE, which reports the empty set itself, but discover is a
	// function three tests call and probes[0] below would panic.
	if len(names) == 0 {
		return nil
	}
	probes := make([]*probed, 0, len(names))
	var wg sync.WaitGroup
	for _, name := range names {
		p := &probed{iface: name}
		probes = append(probes, p)
		wg.Go(func() { p.devices, p.err = send(ctx, p.iface, opts) })
	}
	wg.Wait()

	failed := 0
	for _, p := range probes {
		if p.err != nil {
			failed++
		}
	}
	// Nothing could be probed at all, and the reason decides whether that is a failure.
	// Interrupting is the normal way to end a probe early — wsd.Discover hands back what it
	// collected and reports ctx.Err() only when that is nothing — so a cancelled run is a
	// result with no device, the way it is for listen, and not a status 1.
	if failed == len(probes) && ctx.Err() == nil {
		// The first failure rather than a summary, so the single-interface case an
		// operator naming one interface is in still says exactly what went wrong. Named
		// when there was a choice: "no such network interface" does not say which.
		if len(probes) == 1 {
			return probes[0].err
		}
		return fmt.Errorf("%s: %w", probes[0].iface, probes[0].err)
	}

	found := 0
	for _, p := range probes {
		// One interface failing must not lose the others, the rule probe already applies
		// to the IP families of one interface. Said here rather than returned, so the rows
		// from the interfaces that answered still reach stdout.
		if p.err != nil {
			fmt.Fprintf(diag, "%s: %s\n", orDash(p.iface), graphicOnly(p.err.Error()))
			continue
		}
		for _, device := range p.devices {
			printDevice(out, p.iface, device)
			found++
		}
	}
	// Finding nothing is a result, not a failure: say so rather than exiting silently. Once
	// for the whole run, since one line per interface would be a dozen lines of noise on a
	// host running containers.
	if found == 0 {
		fmt.Fprintln(diag, "no device answered")
	}
	return nil
}

// printDevice writes one row of the discover table: the interface it answered on, then the
// two fields it advertised.
//
// The last two were advertised by an unauthenticated host, so both go through orDash: the
// row is tab-separated and a tab or a newline in either one forges a device that never
// answered. The interface takes the same route because it can come from argv.
func printDevice(out io.Writer, iface string, device wsd.Device) {
	fmt.Fprintf(out, "%s\t%s\t%s\n",
		orDash(iface), orDash(device.UUID), orDash(device.DeviceServiceURL))
}

// parseTypes turns the --types list into port types. An empty list is not an error: it is
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
