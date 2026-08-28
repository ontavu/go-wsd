// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// SPDX-License-Identifier: MIT

package wsd

import (
	"context"
	"testing"
	"time"
)

// helloReply is a Hello announcement as an ONVIF camera multicasts it.
const helloReply = `<?xml version="1.0" encoding="UTF-8"?>
<SOAP-ENV:Envelope xmlns:SOAP-ENV="http://www.w3.org/2003/05/soap-envelope"
                   xmlns:wsa="http://schemas.xmlsoap.org/ws/2004/08/addressing"
                   xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery"
                   xmlns:tds="http://www.onvif.org/ver10/device/wsdl">
 <SOAP-ENV:Header>
  <wsa:Action>http://schemas.xmlsoap.org/ws/2005/04/discovery/Hello</wsa:Action>
 </SOAP-ENV:Header>
 <SOAP-ENV:Body><d:Hello>
  <wsa:EndpointReference><wsa:Address>urn:uuid:cam-7</wsa:Address></wsa:EndpointReference>
  <d:Types>tds:Device</d:Types>
  <d:Scopes>onvif://www.onvif.org/name/lobby</d:Scopes>
  <d:XAddrs>http://10.0.0.7/onvif/device_service</d:XAddrs>
  <d:MetadataVersion>1</d:MetadataVersion>
 </d:Hello></SOAP-ENV:Body>
</SOAP-ENV:Envelope>`

// byeReply is a Bye announcement, which normally carries no address.
const byeReply = `<?xml version="1.0" encoding="UTF-8"?>
<SOAP-ENV:Envelope xmlns:SOAP-ENV="http://www.w3.org/2003/05/soap-envelope"
                   xmlns:wsa="http://schemas.xmlsoap.org/ws/2004/08/addressing"
                   xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery">
 <SOAP-ENV:Body><d:Bye>
  <wsa:EndpointReference><wsa:Address>urn:uuid:cam-7</wsa:Address></wsa:EndpointReference>
 </d:Bye></SOAP-ENV:Body>
</SOAP-ENV:Envelope>`

// oasisHello is the same announcement in the OASIS 1.1 dialect, to check reception is
// dialect-agnostic: only a sender has to pick one.
const oasisHello = `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"
            xmlns:a="http://www.w3.org/2005/08/addressing"
            xmlns:d="http://docs.oasis-open.org/ws-dd/ns/discovery/2009/01"
            xmlns:tds="http://www.onvif.org/ver10/device/wsdl">
 <s:Body><d:Hello>
  <a:EndpointReference><a:Address>urn:uuid:cam-9</a:Address></a:EndpointReference>
  <d:Types>tds:Device</d:Types>
  <d:XAddrs>https://10.0.0.9/onvif</d:XAddrs>
 </d:Hello></s:Body>
</s:Envelope>`

func TestParseAnnouncementHello(t *testing.T) {
	got, ok := parseAnnouncement(helloReply)
	if !ok {
		t.Fatal("a Hello announcement was not recognised")
	}
	if got.Kind != KindHello {
		t.Errorf("Kind = %v, want Hello", got.Kind)
	}
	if got.Device.UUID != "urn:uuid:cam-7" {
		t.Errorf("Uuid = %q", got.Device.UUID)
	}
	if got.Device.DeviceServiceURL != "http://10.0.0.7/onvif/device_service" {
		t.Errorf("DeviceServiceURL = %q", got.Device.DeviceServiceURL)
	}
	if got.Device.Xaddr != "10.0.0.7" {
		t.Errorf("Xaddr = %q", got.Device.Xaddr)
	}
	if !got.IsOnvif() {
		t.Errorf("IsOnvif() = false for Types %v", got.Types)
	}
}

func TestParseAnnouncementBye(t *testing.T) {
	got, ok := parseAnnouncement(byeReply)
	if !ok {
		t.Fatal("a Bye announcement was not recognised")
	}
	if got.Kind != KindBye {
		t.Errorf("Kind = %v, want Bye", got.Kind)
	}
	if got.Device.UUID != "urn:uuid:cam-7" {
		t.Errorf("Uuid = %q", got.Device.UUID)
	}
	// A Bye carrying no address must still be reported: the endpoint reference is what
	// identifies the departing device.
	if got.Device.DeviceServiceURL != "" {
		t.Errorf("DeviceServiceURL = %q, want empty", got.Device.DeviceServiceURL)
	}
}

// TestParseAnnouncementIsDialectAgnostic checks reception handles both versions, which
// is what lets Listen avoid a per-flavor split.
func TestParseAnnouncementIsDialectAgnostic(t *testing.T) {
	got, ok := parseAnnouncement(oasisHello)
	if !ok {
		t.Fatal("an OASIS 1.1 Hello was not recognised")
	}
	if got.Device.UUID != "urn:uuid:cam-9" {
		t.Errorf("Uuid = %q", got.Device.UUID)
	}
	if got.Device.DeviceServiceURL != "https://10.0.0.9/onvif" {
		t.Errorf("DeviceServiceURL = %q, scheme and path must survive", got.Device.DeviceServiceURL)
	}
	if !got.IsOnvif() {
		t.Error("IsOnvif() = false")
	}
}

// TestParseAnnouncementHostileInput is the same guard as for ProbeMatches: the payload
// is unauthenticated input from the link.
func TestParseAnnouncementHostileInput(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
	}{
		{"empty", ""},
		{"plain text", "hello world"},
		{"binary junk", "\x00\xff\xfe"},
		{"truncated", `<Envelope><Body><Hello>`},
		{"no announcement", `<Envelope><Body><ProbeMatches/></Body></Envelope>`},
		{"probematches", probeMatchesReply(testMessageID,
			probeMatch("urn:uuid:x", "tds:Device", "http://10.0.0.1/onvif"))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := parseAnnouncement(tc.payload); ok {
				t.Errorf("parseAnnouncement accepted %q", tc.name)
			}
		})
	}
}

// TestParseAnnouncementNonOnvif checks a non-ONVIF announcement is reported but flagged,
// leaving the policy to the caller.
func TestParseAnnouncementNonOnvif(t *testing.T) {
	printer := `<Envelope xmlns:d="http://docs.oasis-open.org/ws-dd/ns/discovery/2009/01"
	                  xmlns:p="http://schemas.microsoft.com/windows/2006/08/wdp/print">
	 <Body><d:Hello>
	  <EndpointReference><Address>urn:uuid:printer-1</Address></EndpointReference>
	  <d:Types>p:PrintDeviceType</d:Types>
	  <d:XAddrs>http://10.0.0.50/print</d:XAddrs>
	 </d:Hello></Body></Envelope>`

	got, ok := parseAnnouncement(printer)
	if !ok {
		t.Fatal("the announcement was not recognised")
	}
	if got.IsOnvif() {
		t.Errorf("IsOnvif() = true for Types %v", got.Types)
	}
}

// TestParseAnnouncementResolvesTypesOnTypesElement checks the announcement path resolves
// QNames against the d:Types element itself. A device that declares its prefix there,
// which is where the declaration belongs and where this package puts its own, used to
// resolve to {}Device and be reported as non-ONVIF.
func TestParseAnnouncementResolvesTypesOnTypesElement(t *testing.T) {
	hello := `<Envelope xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery">
	 <Body><d:Hello>
	  <EndpointReference><Address>urn:uuid:cam-9</Address></EndpointReference>
	  <d:Types xmlns:tds="http://www.onvif.org/ver10/device/wsdl">tds:Device</d:Types>
	  <d:XAddrs>http://10.0.0.9/onvif/device_service</d:XAddrs>
	 </d:Hello></Body></Envelope>`

	got, ok := parseAnnouncement(hello)
	if !ok {
		t.Fatal("the announcement was not recognised")
	}
	want := "{http://www.onvif.org/ver10/device/wsdl}Device"
	if len(got.Types) != 1 || got.Types[0] != want {
		t.Fatalf("Types = %v, want [%s]", got.Types, want)
	}
	if !got.IsOnvif() {
		t.Errorf("IsOnvif() = false for Types %v", got.Types)
	}
}

// TestListenOnUnknownInterface checks a bad interface name is an error, not a panic.
func TestListenOnUnknownInterface(t *testing.T) {
	if _, err := Listen(context.Background(), "definitely-not-an-interface"); err == nil {
		t.Error("an unknown interface must be reported")
	}
}

// TestListenStopsOnContextCancel checks the channel closes when the caller is done, so
// that a listener does not leak a goroutine per call.
func TestListenStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	announcements, err := Listen(ctx, loopback(t).Name)
	if err != nil {
		t.Skipf("no multicast listener available in this environment: %v", err)
	}

	cancel()

	select {
	case _, open := <-announcements:
		if open {
			// A stray announcement is fine; the channel must still close.
			select {
			case _, open := <-announcements:
				if open {
					t.Error("the channel did not close after cancellation")
				}
			case <-time.After(5 * time.Second):
				t.Error("the channel did not close after cancellation")
			}
		}
	case <-time.After(5 * time.Second):
		t.Error("the channel did not close after cancellation")
	}
}
