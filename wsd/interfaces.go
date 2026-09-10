// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// SPDX-License-Identifier: MIT

package wsd

import (
	"net"
	"strings"
)

// Name prefixes of the devices a container runtime, a hypervisor or a CNI plugin creates.
// A prefix suffices for all of them, and is unambiguous: a NIC a device can be reached
// through is named en*, eth*, wl*, ww*, bond*, team* or vlan*, and none of those collide
// with an entry below. "tunl" is the kernel's IPIP stub and is deliberately not "tun",
// which would take an OpenVPN tun0 with it.
//
// These are prefixes and not globs because net.Interfaces() reports "veth1f1a98b": the
// "@if2" suffix ip-link displays is its rendering of IFLA_LINK, not part of the name.
//
// This is a denylist on purpose, and it will age: a new CNI plugin ships a new prefix, its
// interface gets probed, and the result is noise rather than a wrong answer. The inverse
// design, an allowlist of en*/eth*/wl*, would be silently wrong on br0, bond0 and
// vendor-renamed NICs — and that is the failure that actually loses a device.
var virtualInterfacePrefixes = []string{
	"docker",        // docker0, docker_gwbridge
	"veth",          // the host end of a container's pair, and weave's vethwe-*
	"virbr", "vnet", // libvirt bridges (incl. virbr0-nic) and guest taps
	"vmnet", "vboxnet", // VMware, VirtualBox
	"lxdbr", "lxcbr", "incusbr", // LXD, LXC, Incus
	"fwbr", "fwln", "fwpr", // Proxmox firewall bridges
	"podman", "nerdctl", "cni", // podman1, nerdctl0, cni0, cni-podman0
	"flannel.", "cali", "cilium_", // Kubernetes CNIs
	"weave", "datapath", "kube-", // weave, kube-ipvs0, kube-bridge
	"vxlan.", "genev_sys_", "vxlan_sys_", "ovs-", // overlays and Open vSwitch
	"dummy", "ifb", "nlmon", "teql", // kernel pseudo devices
	"gre", "tunl", "sit", "erspan", "ip_vti", "ip6tnl", "ip6gre", // kernel tunnel stubs
	"zt", // ZeroTier
}

// isDockerBridge matches the name Docker gives a user-defined bridge: "br-" followed by
// the first 12 hex digits of the network id. The shape is the whole point: br0, bridge0 and
// OpenWrt's br-lan are legitimate LAN bridges that a device sits on, and a bare "br-"
// prefix would take br-lan with it.
//
// Spelled out rather than as a regexp. This is the only pattern in the library, and regexp
// with regexp/syntax measured 371 KiB of a 4.0 MB binary importing nothing else of wsd —
// a cost every consumer of a discovery library would pay for one anchored fixed-length
// shape.
func isDockerBridge(name string) bool {
	id, ok := strings.CutPrefix(name, "br-")
	if !ok || len(id) != 12 {
		return false
	}
	for i := 0; i < len(id); i++ {
		if c := id[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// isVirtualInterfaceName reports whether name designates a container, VM or overlay device
// rather than a link a device can answer on.
//
// The criterion is the name and nothing else, on purpose. Inside a container the probeable
// NIC is the far end of a veth pair and every mainstream runtime names it eth0, so a
// structural criterion that works on a host — no backing device under
// /sys/class/net/<name>/device, no permanent MAC, a driver of "veth" — refuses to probe
// from within a container, which is the one case that has to keep working. A name is
// relative to the network namespace, and eth0 is in neither table.
func isVirtualInterfaceName(name string) bool {
	if isDockerBridge(name) {
		return true
	}
	for _, prefix := range virtualInterfacePrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// ProbeableInterfaceNames splits the interfaces into those worth a WS-Discovery probe and
// those dropped.
//
// It does not enumerate anything itself. A library must not choose which interfaces a
// caller means: the list may come from a network namespace, from netlink, or from an
// inventory, and the caller may already hold it. That is also what lets this signature have
// no error in it — there is no syscall here to fail — and it leaves the whole policy
// testable from a synthetic list rather than from whatever the machine running the test
// happens to be plugged into. Pass it net.Interfaces().
//
// It yields names because that is what [Discover] and [Listen] take, and yields the dropped
// ones so a caller can report what it dropped. Input order is preserved.
//
// Up and non-loopback are required either way. On top of that the default mode wants an
// interface that can carry the probe at all — WS-Discovery is multicast-only, so a link
// without IFF_MULTICAST can only make Discover fail its group join — and a name that is
// not tooling's. all lifts both of those, so that it stays a complete escape hatch, but it
// does not lift up and non-loopback: the loopback is never selected by policy, only by
// being named.
//
// A down or loopback interface is in neither result. It was never a candidate, and
// reporting it would bury the interfaces a caller can actually do something about.
//
// It decides nothing about trust, the way isOnvifDevice does not: it only narrows what is
// polled. A container naming its NIC eth0 evades the denylist and is probed, which is the
// case that has to keep working, and an interface name is not link input in any case — but
// it is not typed-by-a-human input either, since dev_valid_name rejects whitespace and
// slashes while admitting the C0 controls, and an unprivileged user can name an interface
// inside their own network namespace. Whatever reaches a terminal from here goes through
// the caller's own filter for that reason.
func ProbeableInterfaceNames(interfaces []net.Interface, all bool) (probeable, skipped []string) {
	for _, itf := range interfaces {
		// An empty name cannot come from net.Interfaces(), but this is pure over a list a
		// caller may have built, and Discover would only fail on it.
		if itf.Name == "" || itf.Flags&net.FlagUp == 0 || itf.Flags&net.FlagLoopback != 0 {
			continue
		}
		if !all && (itf.Flags&net.FlagMulticast == 0 || isVirtualInterfaceName(itf.Name)) {
			skipped = append(skipped, itf.Name)
			continue
		}
		probeable = append(probeable, itf.Name)
	}
	return probeable, skipped
}
