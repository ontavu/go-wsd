// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// SPDX-License-Identifier: MIT

package transport

import (
	"net"
	"testing"
	"time"

	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// TestFamilyConnsAdaptTheControlMessageAway pins what PacketConn exists for: v4Conn and
// v6Conn drop the control-message argument of the x/net types and change nothing else.
//
// The 2026-09-11 audit found this package at 21.1% coverage with all four adapter methods
// at 0%. They are pure argument shuffling, which is where a silent transposition lives:
// ReadFrom returns (n, cm, src, err), and an adapter handing cm back as the address
// compiles exactly as readily as the correct version. wsd.sourceOf then type-asserts
// *net.UDPAddr on that value and yields the zero AddrPort for every device, so Device.From
// — the one field observed rather than claimed — would silently become empty with no test
// in either package failing.
//
// No multicast is involved, so this runs wherever loopback does.
func TestFamilyConnsAdaptTheControlMessageAway(t *testing.T) {
	for _, tc := range []struct {
		name    string
		network string
		address string
		wrap    func(net.PacketConn) PacketConn
	}{
		{"ipv4", "udp4", "127.0.0.1:0", func(c net.PacketConn) PacketConn {
			return v4Conn{ipv4.NewPacketConn(c)}
		}},
		{"ipv6", "udp6", "[::1]:0", func(c net.PacketConn) PacketConn {
			return v6Conn{ipv6.NewPacketConn(c)}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := net.ListenPacket(tc.network, tc.address)
			if err != nil {
				t.Skipf("no %s loopback here: %v", tc.network, err)
			}
			self := raw.LocalAddr()
			conn := tc.wrap(raw)
			defer conn.Close()

			const payload = "<d:Probe/>"
			if _, err := conn.WriteTo([]byte(payload), self); err != nil {
				t.Fatalf("WriteTo: %v", err)
			}
			if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
				t.Fatalf("SetReadDeadline: %v", err)
			}

			buf := make([]byte, 64)
			n, src, err := conn.ReadFrom(buf)
			if err != nil {
				t.Fatalf("ReadFrom: %v", err)
			}
			if got := string(buf[:n]); got != payload {
				t.Errorf("read %q, want %q: the adapter returned the wrong length or buffer", got, payload)
			}
			// This is the assertion that matters: wsd.sourceOf reports the zero AddrPort
			// for anything that is not a *net.UDPAddr, and reports it silently.
			if _, ok := src.(*net.UDPAddr); !ok {
				t.Errorf("ReadFrom returned a %T, not a *net.UDPAddr: wsd.sourceOf would "+
					"report the zero AddrPort for every device on this family", src)
			}
		})
	}
}
