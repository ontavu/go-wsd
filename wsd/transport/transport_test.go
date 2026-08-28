// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// SPDX-License-Identifier: MIT

package transport

import (
	"net"
	"testing"
)

// loopback returns the loopback interface, skipping the test when there is none.
func loopback(t *testing.T) *net.Interface {
	t.Helper()
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Skipf("cannot list interfaces: %v", err)
	}
	for i := range ifaces {
		if ifaces[i].Flags&net.FlagLoopback != 0 {
			return &ifaces[i]
		}
	}
	t.Skip("no loopback interface available")
	return nil
}

// TestTargetsForLoopback checks a real interface yields at least one family.
func TestTargetsForLoopback(t *testing.T) {
	iface := loopback(t)
	targets := TargetsFor(iface)
	if len(targets) == 0 {
		t.Fatal("no discovery target for the loopback interface")
	}
	for _, target := range targets {
		if target.group == nil || target.open == nil {
			t.Errorf("target %q is incomplete", target.network)
		}
		if addr, ok := target.GroupAddr().(*net.UDPAddr); !ok || addr.Port != discoveryPort {
			t.Errorf("target %q does not use port %d", target.network, discoveryPort)
		}
	}
}

// TestDiscoveryGroups pins the multicast groups, identical in both published versions.
func TestDiscoveryGroups(t *testing.T) {
	if got := ipv4Target.group.String(); got != "239.255.255.250" {
		t.Errorf("IPv4 group = %q", got)
	}
	if got := ipv6Target.group.String(); got != "ff02::c" {
		t.Errorf("IPv6 group = %q", got)
	}
	if discoveryPort != 3702 {
		t.Errorf("discovery port = %d", discoveryPort)
	}
}

// TestTargetsForAlwaysAttemptsIPv4 is the regression guard for gating on the address
// list: an interface can be up and multicast-capable while reporting no address at that
// instant, such as a wifi interface that is not associated. Refusing to probe it turned
// a usable interface into an error.
func TestTargetsForAlwaysAttemptsIPv4(t *testing.T) {
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Skipf("cannot list interfaces: %v", err)
	}

	for i := range ifaces {
		targets := TargetsFor(&ifaces[i])
		if len(targets) == 0 {
			t.Errorf("interface %q yielded no discovery target", ifaces[i].Name)
			continue
		}
		if targets[0].network != "udp4" {
			t.Errorf("interface %q does not attempt IPv4 first, got %q",
				ifaces[i].Name, targets[0].network)
		}
	}
}

// TestTargetsForAddsIPv6OnlyWithAddress checks the second family is attempted only where
// it can work, so an IPv4-only host does not pay for a doomed attempt on every probe.
func TestTargetsForAddsIPv6OnlyWithAddress(t *testing.T) {
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Skipf("cannot list interfaces: %v", err)
	}

	for i := range ifaces {
		iface := &ifaces[i]
		hasV6 := hasIPv6(iface)
		var attemptsV6 bool
		for _, target := range TargetsFor(iface) {
			if target.network == "udp6" {
				attemptsV6 = true
			}
		}
		if attemptsV6 != hasV6 {
			t.Errorf("interface %q: attempts IPv6 = %v, has an IPv6 address = %v",
				iface.Name, attemptsV6, hasV6)
		}
	}
}
