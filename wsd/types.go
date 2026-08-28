// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// SPDX-License-Identifier: MIT

package wsd

import (
	"fmt"
	"sort"
	"strings"
)

// The namespaces the well-known port types below belong to.
const (
	onvifDeviceNamespace  = "http://www.onvif.org/ver10/device/wsdl"
	onvifNetworkNamespace = "http://www.onvif.org/ver10/network/wsdl"

	dpwsNamespace   = "http://schemas.xmlsoap.org/ws/2006/02/devprof"
	dpws11Namespace = "http://docs.oasis-open.org/ws-dd/ns/dpws/2009/01"

	pnpxNamespace    = "http://schemas.microsoft.com/windows/pnpx/2005/10"
	wdpPrintNamespc  = "http://schemas.microsoft.com/windows/2006/08/wdp/print"
	wdpScanNamespace = "http://schemas.microsoft.com/windows/2006/08/wdp/scan"
)

// TypeName is a port type a Target Service implements: a namespace and a local name.
//
// It is the pair a d:Types QName denotes, carried whole rather than as a prefix and a
// separate declaration map. A prefix means nothing outside the document that declares it,
// so making the caller supply both was a way to get them out of step; the Probe builder
// invents its own prefixes from these.
type TypeName struct {
	// Namespace is the namespace the port type belongs to. It must not be empty: a
	// prefix cannot be bound to an empty namespace, so such a type cannot be put on the
	// wire and the Probe builder passes over it.
	Namespace string

	// Local is the name within that namespace.
	Local string
}

// String renders the type the way a resolved d:Types entry is reported, so that a value
// here and a value in [Device] compare by eye.
func (t TypeName) String() string { return "{" + t.Namespace + "}" + t.Local }

// The port types ONVIF equipment advertises. Section 7.3.2.1 of ONVIF Core mandates
// TypeONVIFDevice; TypeONVIFNetworkVideoTransmitter is the ONVIF 1.0 type that cameras
// still publish, sometimes as the only one.
var (
	TypeONVIFDevice                  = TypeName{onvifDeviceNamespace, "Device"}
	TypeONVIFNetworkVideoTransmitter = TypeName{onvifNetworkNamespace, "NetworkVideoTransmitter"}
	TypeONVIFNetworkVideoDisplay     = TypeName{onvifNetworkNamespace, "NetworkVideoDisplay"}
	TypeONVIFNetworkVideoStorage     = TypeName{onvifNetworkNamespace, "NetworkVideoStorage"}
	TypeONVIFNetworkVideoAnalytics   = TypeName{onvifNetworkNamespace, "NetworkVideoAnalytics"}
)

// The port types the rest of the WS-Discovery world advertises. A Devices Profile for
// Web Services host publishes TypeDPWSDevice or its 1.1 successor; Windows adds the PnP-X
// type to everything it exposes, and its print and scan profiles on top of that.
var (
	TypeDPWSDevice     = TypeName{dpwsNamespace, "Device"}
	TypeDPWS11Device   = TypeName{dpws11Namespace, "Device"}
	TypeWindowsDevice  = TypeName{pnpxNamespace, "Device"}
	TypeWindowsPrinter = TypeName{wdpPrintNamespc, "PrintDeviceType"}
	TypeWindowsScanner = TypeName{wdpScanNamespace, "ScanDeviceType"}
)

// wellKnownTypes names the types above for a command line or a configuration file, so
// that neither has to carry a namespace URI to ask for a camera.
var wellKnownTypes = map[string]TypeName{
	"onvif-device":  TypeONVIFDevice,
	"onvif-nvt":     TypeONVIFNetworkVideoTransmitter,
	"onvif-nvd":     TypeONVIFNetworkVideoDisplay,
	"onvif-nvs":     TypeONVIFNetworkVideoStorage,
	"onvif-nva":     TypeONVIFNetworkVideoAnalytics,
	"dpws-device":   TypeDPWSDevice,
	"dpws11-device": TypeDPWS11Device,
	"windows":       TypeWindowsDevice,
	"printer":       TypeWindowsPrinter,
	"scanner":       TypeWindowsScanner,
}

// WellKnownTypeNames returns the names ParseTypeName accepts, in sorted order.
func WellKnownTypeNames() []string {
	names := make([]string, 0, len(wellKnownTypes))
	for name := range wellKnownTypes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ParseTypeName resolves one of the names from WellKnownTypeNames, or an explicit
// "{namespace}local" pair for a port type this package does not know about.
//
// The explicit form is the one Device and Announcement report, so a type observed on the
// link can be pasted straight back into a narrower probe.
func ParseTypeName(raw string) (TypeName, error) {
	raw = strings.TrimSpace(raw)
	if known, ok := wellKnownTypes[strings.ToLower(raw)]; ok {
		return known, nil
	}
	if strings.HasPrefix(raw, "{") {
		// Both halves have to be there. A prefix cannot be bound to an empty namespace
		// in XML, so "{}Local" would put an illegal xmlns:t0="" on the Probe.
		if i := strings.Index(raw, "}"); i > 1 && i+1 < len(raw) {
			return TypeName{Namespace: raw[1:i], Local: raw[i+1:]}, nil
		}
	}
	return TypeName{}, fmt.Errorf(
		"unknown port type %q: use {namespace}LocalName, or one of %s",
		raw, strings.Join(WellKnownTypeNames(), ", "))
}
