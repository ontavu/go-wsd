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

	"github.com/jfsmig/go-wsd/wsd"
)

// The seams that keep this command testable without a network, following prober in
// wsd/discover.go literally: a function type passed as an argument, not a package variable
// a test has to save and restore. That matters here because the fan-out reads its seam from
// several goroutines, so a mutable global would be a data race the moment one of these
// tests grew a t.Parallel.
//
// Production passes wsd.Discover, wsd.Listen and net.Interfaces; a test passes a fake and
// nothing it does can reach another test.
type (
	discoverer func(context.Context, string, wsd.ProbeOptions) ([]wsd.Device, error)
	watcher    func(context.Context, string) (<-chan wsd.Announcement, error)
	enumerator func() ([]net.Interface, error)
)

// allInterfacesHelp is shared by both verbs so the two cannot drift. It says when the flag
// matters because it does nothing at all when an interface is named, and because the tool
// already has an unrelated --all.
const allInterfacesHelp = "when no interface is named, poll the container and VM interfaces too"

// interfacesToPoll resolves the positional arguments to the interfaces to work on, and
// reports the ones policy dropped so the caller can say what it passed over.
//
// A name given explicitly is used exactly as given, filter and all: an operator who types
// an interface has already decided, and wsd.ProbeableInterfaceNames drops the loopback by
// policy, so "wsdc discover lo" would otherwise stop working. Only the empty case consults
// the policy. Duplicates collapse, so naming one interface twice does not poll it twice.
func interfacesToPoll(args []string, all bool, enumerate enumerator) (names, skipped []string, err error) {
	if len(args) > 0 {
		seen := make(map[string]bool, len(args))
		for _, name := range args {
			// Refused rather than passed on: net.InterfaceByName cannot resolve it, so it
			// could only become a runtime failure, and the interface column prints an
			// absent field as the dash that means "the device did not advertise it".
			if strings.TrimSpace(name) == "" {
				return nil, nil, usagef("an interface name cannot be empty")
			}
			if seen[name] {
				continue
			}
			seen[name] = true
			names = append(names, name)
		}
		return names, nil, nil
	}

	interfaces, err := enumerate()
	if err != nil {
		return nil, nil, err
	}
	names, skipped = wsd.ProbeableInterfaceNames(interfaces, all)
	return names, skipped, nil
}

// reportSelection says what policy chose, on the runs where policy did the choosing and
// left something out. A caller who named interfaces needs no explanation, and a host with
// no virtual devices has nothing to explain — but on a host running containers this line is
// the only way to learn that the camera on docker0 was never probed at all.
func reportSelection(diag io.Writer, names, skipped []string) {
	if len(skipped) == 0 {
		return
	}
	fmt.Fprintf(diag, "polling %s; skipped %d, pass --all-interfaces to include them\n",
		orDash(strings.Join(names, " ")), len(skipped))
}

// reportNothingToPoll tells the operator which of the two empty cases happened, because
// only the filtered one has a way around it: recommending --all-interfaces to someone whose
// only interface is the loopback would be advice that cannot work.
//
// Deliberately not a fallback to polling everything. The filter has to mean what it says,
// and falling open would make the set depend on the host's state — one interface coming up
// would silently switch the run from every virtual device on the host to that one. It is a
// result rather than a failure, like finding no device, so the status stays 0.
func reportNothingToPoll(diag io.Writer, skipped []string) {
	if len(skipped) == 0 {
		fmt.Fprintln(diag, "no interface to poll: none is both up and non-loopback")
		return
	}
	fmt.Fprintf(diag, "no interface left to poll after filtering %d (%s); "+
		"pass --all-interfaces to poll them too\n", len(skipped), orDash(strings.Join(skipped, " ")))
}
