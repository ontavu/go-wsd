// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// SPDX-License-Identifier: MIT

// Package transport carries the WS-Discovery multicast plumbing: the discovery groups
// of both IP families, joining them on an interface, and one connection interface over
// the two families, whose PacketConn types differ only in an argument unused here.
package transport

import (
	"net"
	"time"

	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// discoveryPort is the WS-Discovery UDP port, identical in both published versions.
const discoveryPort = 3702

// PacketConn is the part of the ipv4 and ipv6 PacketConn API this package needs. The two
// concrete types differ only in their control-message argument, which is unused here.
//
// SetReadDeadline must be safe to call while another goroutine is inside ReadFrom, and
// after Close. That is how a collection window is cut short: the reader blocks in ReadFrom
// and a watchdog moves the deadline into the past to wake it, which is the only way to
// interrupt a read on a socket. Both types here satisfy it, as does net.UDPConn; an
// implementation that guards its deadline with an ordinary field will race.
type PacketConn interface {
	SetReadDeadline(time.Time) error
	WriteTo([]byte, net.Addr) (int, error)
	ReadFrom([]byte) (int, net.Addr, error)
	Close() error
}

// Target describes the discovery endpoint of one IP family.
type Target struct {
	// network is the network passed to net.ListenPacket.
	network string
	// listen is the local address to bind.
	listen string
	// group is the discovery multicast group.
	group net.IP
	// open joins the group and returns a usable connection. It takes the target as an
	// argument rather than closing over the package variable, which would make the
	// variable's initialiser depend on itself.
	open func(t Target, iface *net.Interface, hopLimit int) (PacketConn, error)
}

// Dial opens a connection for probing this target from an interface: an ephemeral port
// bound to every address, with the interface selected for multicast egress and the hop
// limit applied. Replies to a Probe arrive unicast on that port, so no group membership is
// involved; ListenMulticast is what joins the group.
func (t Target) Dial(iface *net.Interface, hopLimit int) (PacketConn, error) {
	return t.open(t, iface, hopLimit)
}

func (t Target) GroupAddr() net.Addr {
	return &net.UDPAddr{IP: t.group, Port: discoveryPort}
}

// ListenMulticast joins the discovery group of this target on an interface and returns a
// connection that receives the unsolicited Hello and Bye announcements.
func (t Target) ListenMulticast(iface *net.Interface) (*net.UDPConn, error) {
	conn, err := net.ListenMulticastUDP(t.network, iface, &net.UDPAddr{IP: t.group, Port: discoveryPort})
	if err != nil {
		return nil, err
	}
	// Without this the socket is delivered datagrams that arrived on other interfaces, so
	// a caller listening on two of them cannot tell which link an announcement came from.
	restrictToJoinedGroups(conn, t.group.To4() == nil)
	return conn, nil
}

var (
	// ipv4Target is the IPv4 discovery group, 239.255.255.250:3702.
	ipv4Target = Target{
		network: "udp4",
		listen:  "0.0.0.0:0",
		group:   net.IPv4(239, 255, 255, 250),
		open:    dialIPv4,
	}

	// ipv6Target is the IPv6 discovery group, [FF02::C]:3702. Both published versions
	// of WS-Discovery define it; only IPv4 used to be implemented here.
	ipv6Target = Target{
		network: "udp6",
		listen:  "[::]:0",
		group:   net.ParseIP("ff02::c"),
		open:    dialIPv6,
	}
)

// TargetsFor returns the discovery endpoints to attempt on an interface.
//
// IPv4 is always attempted: an interface can be multicast-capable and joinable while
// reporting no address at that instant, and gating on the address list turned a
// perfectly usable interface into an error.
//
// IPv6 is attempted only when the interface actually carries an IPv6 address, so that a
// host without IPv6 does not pay for a doomed attempt on every probe.
func TargetsFor(iface *net.Interface) []Target {
	out := []Target{ipv4Target}
	if hasIPv6(iface) {
		out = append(out, ipv6Target)
	}
	return out
}

// hasIPv6 reports whether an interface carries a non-IPv4 address.
func hasIPv6(iface *net.Interface) bool {
	addrs, err := iface.Addrs()
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		if ipNet.IP.To4() == nil && ipNet.IP.To16() != nil {
			return true
		}
	}
	return false
}

func dialIPv4(t Target, iface *net.Interface, hopLimit int) (PacketConn, error) {
	c, err := net.ListenPacket(t.network, t.listen)
	if err != nil {
		return nil, err
	}

	p := ipv4.NewPacketConn(c)
	if err := setupV4(p, iface, hopLimit); err != nil {
		_ = c.Close()
		return nil, err
	}
	return v4Conn{p}, nil
}

// setupV4 prepares a socket to send to the IPv4 discovery group. Sending needs the egress
// interface and a TTL, and nothing else.
//
// It used to join the group as well, on the reasoning that this is how the Hello and Bye
// announcements arrive. It is not: this socket is bound to an ephemeral port and the
// announcements go to the discovery port, so no membership could ever deliver one here.
// ListenMulticast binds that port and joins there. All the call did was cost an IGMP
// report and fail the probe on interfaces where sending would have worked.
func setupV4(p *ipv4.PacketConn, iface *net.Interface, hopLimit int) error {
	if err := p.SetMulticastInterface(iface); err != nil {
		return err
	}
	return p.SetMulticastTTL(hopLimit)
}

func dialIPv6(t Target, iface *net.Interface, hopLimit int) (PacketConn, error) {
	c, err := net.ListenPacket(t.network, t.listen)
	if err != nil {
		return nil, err
	}

	p := ipv6.NewPacketConn(c)
	if err := setupV6(p, iface, hopLimit); err != nil {
		_ = c.Close()
		return nil, err
	}
	return v6Conn{p}, nil
}

// setupV6 is setupV4 over IPv6, and joins nothing for the same reason.
func setupV6(p *ipv6.PacketConn, iface *net.Interface, hopLimit int) error {
	if err := p.SetMulticastInterface(iface); err != nil {
		return err
	}
	return p.SetMulticastHopLimit(hopLimit)
}

// v4Conn adapts ipv4.PacketConn to PacketConn.
type v4Conn struct{ *ipv4.PacketConn }

func (c v4Conn) WriteTo(b []byte, dst net.Addr) (int, error) {
	return c.PacketConn.WriteTo(b, nil, dst)
}

func (c v4Conn) ReadFrom(b []byte) (int, net.Addr, error) {
	n, _, src, err := c.PacketConn.ReadFrom(b)
	return n, src, err
}

// v6Conn adapts ipv6.PacketConn to PacketConn.
type v6Conn struct{ *ipv6.PacketConn }

func (c v6Conn) WriteTo(b []byte, dst net.Addr) (int, error) {
	return c.PacketConn.WriteTo(b, nil, dst)
}

func (c v6Conn) ReadFrom(b []byte) (int, net.Addr, error) {
	n, _, src, err := c.PacketConn.ReadFrom(b)
	return n, src, err
}
