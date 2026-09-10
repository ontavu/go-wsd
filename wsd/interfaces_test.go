// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// SPDX-License-Identifier: MIT

package wsd

import (
	"net"
	"slices"
	"testing"
)

// up is a synthetic interface carrying the flags a probeable link has. The whole policy is
// pure over its argument precisely so that these cases need no hardware: the machine
// running the test decides nothing.
func up(name string) net.Interface {
	return net.Interface{Name: name, Flags: net.FlagUp | net.FlagMulticast}
}

// TestIsVirtualInterfaceNameSparesRealNICs is the half of the policy that costs a device
// when it is wrong. A name wrongly called virtual loses whatever sits on that link, and
// the operator has no way to tell a filtered interface from an empty one.
//
// The three shapes worth pinning: prefixes match at the start only, "tun" and "tap" are
// deliberately absent so an OpenVPN tun0 survives ("tunl" is the kernel's IPIP stub), and
// the Docker bridge is matched by its exact form, twelve hex digits, so that br0, bridge0
// and OpenWrt's br-lan are not taken with it.
func TestIsVirtualInterfaceNameSparesRealNICs(t *testing.T) {
	for _, name := range []string{
		"eth0", "enp4s0", "eno1", "ens33", "enx244bfedfea3e",
		"wlp3s0", "wlan0", "bond0", "team0", "vlan10", "eth0.100",
		"br0", "bridge0", "br-lan",
		"br-e967edea9dc",   // eleven digits: not Docker's shape
		"br-e967edea9dccf", // thirteen: nor is this
		"tun0", "tap0", "wg0", "tailscale0",
	} {
		if isVirtualInterfaceName(name) {
			t.Errorf("%q was called virtual; a device on that link would be lost", name)
		}
	}

	for _, name := range []string{
		"docker0", "docker_gwbridge", "veth1f1a98b", "vethwe-bridge",
		"virbr0", "virbr0-nic", "vnet0", "vmnet1", "vboxnet0",
		"lxdbr0", "lxcbr0", "incusbr0", "fwbr0", "fwln0", "fwpr0",
		"podman1", "nerdctl0", "cni0", "cni-podman0",
		"flannel.1", "cali123abc", "cilium_host", "weave", "datapath", "kube-ipvs0",
		"vxlan.calico", "genev_sys_6081", "vxlan_sys_4789", "ovs-system",
		"dummy0", "ifb0", "nlmon0", "teql0",
		"gre0", "tunl0", "sit0", "erspan0", "ip_vti0", "ip6tnl0", "ip6gre0", "zt0",
		"br-e967edea9dcc", // exactly twelve hex digits: Docker's shape
	} {
		if !isVirtualInterfaceName(name) {
			t.Errorf("%q was not called virtual; probing it is noise", name)
		}
	}
}

// TestProbeableInterfaceNames pins the split, including which rejections the caller is told
// about. Down and loopback were never candidates and appear in neither result; an interface
// dropped by the filter appears in skipped, because that is the only rejection an operator
// can do something about — passing all.
func TestProbeableInterfaceNames(t *testing.T) {
	for _, tc := range []struct {
		name          string
		interfaces    []net.Interface
		all           bool
		wantProbeable []string
		wantSkipped   []string
	}{
		{"nothing at all", nil, false, nil, nil},

		{"a plain NIC", []net.Interface{up("eth0")}, false, []string{"eth0"}, nil},

		// Neither is a candidate, so neither is reported: an operator cannot bring an
		// interface up by passing a flag, and recommending one would be advice that
		// cannot work.
		{"a down NIC", []net.Interface{{Name: "eth0", Flags: net.FlagMulticast}}, false, nil, nil},
		{"the loopback", []net.Interface{{Name: "lo", Flags: net.FlagUp | net.FlagLoopback | net.FlagMulticast}}, false, nil, nil},
		// all is an escape hatch from the filter, not from being a candidate at all.
		{"the loopback, even with all", []net.Interface{{Name: "lo", Flags: net.FlagUp | net.FlagLoopback | net.FlagMulticast}}, true, nil, nil},

		// WS-Discovery is multicast-only, so a link without IFF_MULTICAST could only make
		// Discover fail its group join. This one the caller can override, so it is said.
		{"no multicast", []net.Interface{{Name: "wg0", Flags: net.FlagUp}}, false, nil, []string{"wg0"}},
		{"no multicast, with all", []net.Interface{{Name: "wg0", Flags: net.FlagUp}}, true, []string{"wg0"}, nil},

		{"a virtual name", []net.Interface{up("docker0")}, false, nil, []string{"docker0"}},
		{"a virtual name, with all", []net.Interface{up("docker0")}, true, []string{"docker0"}, nil},

		{
			"a host running containers",
			[]net.Interface{
				{Name: "lo", Flags: net.FlagUp | net.FlagLoopback | net.FlagMulticast},
				up("enp4s0"), up("docker0"), up("veth1f1a98b"), up("br-lan"),
				{Name: "eth1", Flags: net.FlagMulticast},
			},
			false,
			[]string{"enp4s0", "br-lan"},
			[]string{"docker0", "veth1f1a98b"},
		},
		{
			// Order is preserved because the caller prints one group of rows per
			// interface in this order.
			"order is preserved",
			[]net.Interface{up("wlan0"), up("eth0"), up("bond0")},
			false,
			[]string{"wlan0", "eth0", "bond0"},
			nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probeable, skipped := ProbeableInterfaceNames(tc.interfaces, tc.all)
			if !slices.Equal(probeable, tc.wantProbeable) {
				t.Errorf("probeable = %v, want %v", probeable, tc.wantProbeable)
			}
			if !slices.Equal(skipped, tc.wantSkipped) {
				t.Errorf("skipped = %v, want %v", skipped, tc.wantSkipped)
			}
		})
	}
}
