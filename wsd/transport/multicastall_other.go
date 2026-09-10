// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// SPDX-License-Identifier: MIT

//go:build !linux

package transport

import "net"

// restrictToJoinedGroups is a no-op away from Linux, where IP_MULTICAST_ALL does not exist:
// a socket is delivered only what arrives on the interface it joined the group on, which is
// what the Linux version has to ask for. See multicastall_linux.go for what it buys.
func restrictToJoinedGroups(conn *net.UDPConn, v6 bool) {}
