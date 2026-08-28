// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// SPDX-License-Identifier: MIT

package wsd

import "net/netip"

// Device is a Target Service that answered discovery.
//
// The type is defined here rather than reused from an ONVIF client package on purpose:
// discovery runs before any client exists, so depending on one would invert the
// relationship and drag a SOAP stack into the build of anyone who only wants to find
// devices on the link. Callers that go on to talk ONVIF convert these three fields at
// their own boundary.
type Device struct {
	// Xaddr is the "host:port" of the first advertised address, the form an ONVIF
	// client expects.
	Xaddr string

	// UUID is the endpoint reference of the device, when discovery provided one. It is
	// the identifier that stays stable across the device's network interfaces, and the
	// only thing a Bye announcement usually carries.
	UUID string

	// DeviceServiceURL is the device service address exactly as advertised, scheme and
	// path included. Neither is predictable: ONVIF does not fix the path, and Core
	// section 7.3.2.3 asks for one URI per protocol, https included.
	//
	// Only http and https addresses are reported, and never one carrying credentials.
	// The value still comes from an unauthenticated datagram, so it names wherever its
	// sender chose, the caller's own network included.
	DeviceServiceURL string

	// From is the address the datagram arrived from, which is not the same thing as the
	// device it describes: probe replies and announcements alike are unauthenticated, so
	// any host on the link can claim any endpoint reference and advertise any address.
	// Everything above is that claim; this is the one thing observed rather than told.
	// Keeping it lets a caller attribute or rate-limit what it receives.
	//
	// A device that answered several times is reported once, so this is the source of
	// the first datagram that carried it and not of every one.
	From netip.AddrPort
}
