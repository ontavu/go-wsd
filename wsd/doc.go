// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// SPDX-License-Identifier: MIT

// Package wsd implements the Client role of WS-Discovery, the protocol ONVIF cameras
// use to announce themselves on a local link.
//
// Two dialects exist and are not interoperable. Discover and SendProbe pick one through
// [ProbeOptions.Flavor]; the zero value is [FlavorDraft2005], the April 2005 XMLSOAP
// draft that ONVIF Core references normatively and the only one cameras answer.
// [FlavorOASIS11] reaches the standardised version that network printers and Windows
// hosts speak. Listen needs no dialect: replies are matched on local element names, so
// one parser serves both.
//
// # Port types
//
// A Probe carries a d:Types element listing the port types it asks for, and matching is
// conjunctive: naming several narrows the result to the Target Services implementing all
// of them. [ProbeOptions.PortTypes] takes [TypeName] values, which carry the namespace
// with the local name; the well-known ones are [TypeONVIFNetworkVideoTransmitter] and its
// neighbours, and [ParseTypeName] turns a short name or a "{namespace}local" pair into
// one.
//
// Leave it empty to reach every Target Service. The Probe then carries an *empty* d:Types
// element rather than none: WS-Discovery says an absent d:Types matches everything, but
// equipment reads it differently. Measured against three ONVIF cameras and one non-ONVIF
// device on one link, a Probe with no d:Types drew a single reply while the same Probe
// carrying an empty one drew all four.
//
// Discovery has two halves, and a Probe is only one of them:
//
//	devices, err := wsd.Discover(ctx, "eth0", wsd.ProbeOptions{})
//
// polls the link and returns what answers within the collection window, while
//
//	announcements, err := wsd.Listen(ctx, "eth0")
//
// reports the Hello and Bye a device multicasts when it joins or leaves. A Probe alone
// cannot observe a departure.
//
// # Untrusted input
//
// Every datagram on this path arrives unauthenticated over UDP multicast from any host
// on the link. Anything that does not parse, does not correlate with the Probe that was
// sent, or carries no usable address is discarded rather than reported: ONVIF Core
// section 7.3.6 asks that malformed multicast be dropped silently rather than answered,
// to avoid packet storms. An advertised address survives only if it is http or https and
// carries no credentials, since the caller is the one who will dial it.
//
// [Device.From] carries the address the datagram came from, on both halves alike, so that
// a caller can attribute or rate-limit what it receives. It is the only field that was
// observed rather than claimed: the endpoint reference and the addresses inside a
// datagram are whatever its sender chose to write.
//
// Volume is bounded as well as content. A probe retains a limited number of datagrams and
// a limited number of bytes, and stops collecting once either is reached. Reaching a
// limit is a result and not an error: what was collected is returned.
//
// [ProbeOptions.Timeout] bounds the collection window and nothing else. The replies are
// parsed once it closes, and that phase ends when ctx is done, so a caller wanting a
// bound on the whole call has to give ctx a deadline: parsing everything the caps admit
// takes seconds when the link is full of crafted replies.
package wsd
