// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// SPDX-License-Identifier: MIT

//go:build linux

package transport

import (
	"net"
	"syscall"
)

// IP_MULTICAST_ALL and IPV6_MULTICAST_ALL, from the Linux uapi headers. The syscall
// package does not define either.
const (
	ipMulticastAll   = 49
	ipv6MulticastAll = 29
)

// restrictToJoinedGroups stops a listener from receiving datagrams that arrived on an
// interface it did not join the group on.
//
// Linux leaves IP_MULTICAST_ALL at 1, and a socket with it set is delivered every datagram
// for a group joined anywhere on the host, whatever interface it arrived on. One listener
// never noticed: with the group joined nowhere else the kernel drops the datagram at the IP
// layer. A second listener is what supplies the precondition, so watching two interfaces
// turned one announcement into one row per interface, each naming an interface the datagram
// may never have touched — and the interface is printed as an observed field, beside
// Device.From, precisely because the rest of a row is a claim.
//
// Measured on two bridges: one listener received only its own interface's datagram, two
// listeners each received both, and clearing this restored the first result.
//
// Best effort. It narrows what a listener sees, so failing to clear it is the behaviour
// that was there before; a kernel too old to know the option is not a reason to refuse to
// listen at all.
func restrictToJoinedGroups(conn *net.UDPConn, v6 bool) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return
	}
	level, option := syscall.IPPROTO_IP, ipMulticastAll
	if v6 {
		level, option = syscall.IPPROTO_IPV6, ipv6MulticastAll
	}
	_ = raw.Control(func(fd uintptr) {
		_ = syscall.SetsockoptInt(int(fd), level, option, 0)
	})
}
