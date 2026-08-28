// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// SPDX-License-Identifier: MIT

package wsd

// dialect gathers the URIs that distinguish the two published versions of WS-Discovery.
// Nothing else about the exchange differs: the socket handling, the retransmission
// schedule, the collection window and the parsing of the replies are all common.
//
// Only the sender picks a dialect. Replies are matched on local element names, so one
// parser serves both.
type dialect struct {
	// name identifies the dialect in diagnostics.
	name string
	// discovery is the WS-Discovery namespace.
	discovery string
	// to is the wsa:To of a multicast Probe.
	to string
	// probeAction is the [action] property of a Probe.
	probeAction string
	// addressing is the WS-Addressing namespace.
	addressing string
	// anonymous is the anonymous ReplyTo address.
	anonymous string
	// matchByRFC3986 is the RFC 3986 scope matching rule, kept for reference: the
	// default rule is used, so MatchBy is not emitted.
	matchByRFC3986 string
}

var (
	// draft2005 is the April 2005 XMLSOAP draft of WS-Discovery.
	//
	// This is the dialect ONVIF speaks. ONVIF Core Specification v19.12 cites it as a
	// normative reference, requires its rfc3986 scope matching rule in section 7.3.3
	// and its fault action in section 7.3.6. Cameras listening for this dialect ignore
	// an OASIS 1.1 Probe outright, because neither the wsa:To nor the action matches.
	//
	// It pairs with the August 2004 WS-Addressing submission, not WS-Addressing 1.0.
	draft2005 = dialect{
		name:           "draft2005",
		discovery:      "http://schemas.xmlsoap.org/ws/2005/04/discovery",
		to:             "urn:schemas-xmlsoap-org:ws:2005:04:discovery",
		probeAction:    "http://schemas.xmlsoap.org/ws/2005/04/discovery/Probe",
		addressing:     "http://schemas.xmlsoap.org/ws/2004/08/addressing",
		anonymous:      "http://schemas.xmlsoap.org/ws/2004/08/addressing/role/anonymous",
		matchByRFC3986: "http://schemas.xmlsoap.org/ws/2005/04/discovery/rfc3986",
	}

	// oasis11 is OASIS WS-Discovery 1.1, the standardised version.
	//
	// ONVIF cameras do not answer it, since ONVIF pins the 2005 draft. Other
	// WS-Discovery equipment does: network printers, and Windows hosts through WSD.
	//
	// It pairs with WS-Addressing 1.0.
	oasis11 = dialect{
		name:           "oasis11",
		discovery:      "http://docs.oasis-open.org/ws-dd/ns/discovery/2009/01",
		to:             "urn:docs-oasis-open-org:ws-dd:ns:discovery:2009:01",
		probeAction:    "http://docs.oasis-open.org/ws-dd/ns/discovery/2009/01/Probe",
		addressing:     "http://www.w3.org/2005/08/addressing",
		anonymous:      "http://www.w3.org/2005/08/addressing/anonymous",
		matchByRFC3986: "http://docs.oasis-open.org/ws-dd/ns/discovery/2009/01/rfc3986",
	}
)

// Flavor selects the WS-Discovery dialect a Probe speaks. Only the sender picks one:
// replies are matched on local element names, so one parser serves both.
type Flavor int

const (
	// FlavorDraft2005 is the April 2005 XMLSOAP draft, the dialect ONVIF mandates and
	// the one that discovers cameras. It is the zero value, because probing for ONVIF
	// equipment is what this package exists for.
	FlavorDraft2005 Flavor = iota

	// FlavorOASIS11 is OASIS WS-Discovery 1.1. ONVIF cameras do not answer it, since
	// ONVIF pins the 2005 draft; other WS-Discovery equipment does, such as network
	// printers and Windows hosts speaking WSD.
	FlavorOASIS11
)

func (f Flavor) String() string { return f.dialect().name }

// dialect resolves the flavor to the URIs that distinguish it on the wire. An
// unrecognised value falls back to the ONVIF dialect rather than emitting a Probe with
// empty namespaces, which no device would answer.
func (f Flavor) dialect() dialect {
	if f == FlavorOASIS11 {
		return oasis11
	}
	return draft2005
}
