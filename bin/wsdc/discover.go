// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ontavu/go-wsd/wsd"
)

// newDiscoverCmd builds the probe verb. The four flags are declared here rather than on
// the root because they describe a Probe, and only this command sends one: on the root
// they would reach listen, which cannot honour any of them.
func newDiscoverCmd() *cobra.Command {
	var (
		opts  wsd.ProbeOptions
		oasis bool
		types string
	)
	cmd := &cobra.Command{
		Use:   "discover <interface>",
		Short: "Probe the link and print what answers",
		Long: "Probe one network interface and print, tab-separated, the endpoint reference and\n" +
			"the device service URL of every device that answers within the collection window.\n" +
			"A field the device did not advertise is printed as a dash.\n" +
			"\n" +
			"Finding nothing is a result rather than a failure, and the status stays 0.\n" +
			"\n" + portTypeHelp(),
		Example: "  wsdc discover eth0\n" +
			"  wsdc discover --types onvif-nvt eth0\n" +
			"  wsdc discover --types '{urn:x}Thing' eth0",
		Args: exactlyOneInterface,
		RunE: func(cmd *cobra.Command, args []string) error {
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
			return discover(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), args[0], opts)
		},
	}

	flags := cmd.Flags()
	flags.DurationVar(&opts.Timeout, "timeout", wsd.DefaultProbeTimeout, "collection window for a probe")
	flags.BoolVar(&oasis, "oasis11", false, "use OASIS WS-Discovery 1.1 instead of the ONVIF dialect")
	flags.BoolVar(&opts.IncludeNonOnvif, "all", false, "keep devices that do not advertise an ONVIF port type")
	flags.StringVar(&types, "types", "", "comma-separated port types to probe for; empty probes for every Target Service")
	return cmd
}

// discover probes once and prints the devices that answered.
func discover(ctx context.Context, out, diag io.Writer, iface string, opts wsd.ProbeOptions) error {
	devices, err := wsd.Discover(ctx, iface, opts)
	if err != nil {
		return err
	}
	// Finding nothing is a result, not a failure: say so rather than exiting silently.
	if len(devices) == 0 {
		fmt.Fprintln(diag, "no device answered")
		return nil
	}
	for _, device := range devices {
		printDevice(out, device)
	}
	return nil
}

// printDevice writes one row of the discover table. Both columns were advertised by an
// unauthenticated host, so both go through orDash: the row is tab-separated and a tab or a
// newline in either one forges a device that never answered.
func printDevice(out io.Writer, device wsd.Device) {
	fmt.Fprintf(out, "%s\t%s\n", orDash(device.UUID), orDash(device.DeviceServiceURL))
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
