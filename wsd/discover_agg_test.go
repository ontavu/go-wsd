// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// SPDX-License-Identifier: MIT

package wsd

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

// probeMatchesEnvelope builds a ProbeMatches carrying one match per address given.
func probeMatchesEnvelope(messageID string, matches ...string) string {
	body := ""
	for _, m := range matches {
		body += m
	}
	return `<?xml version="1.0"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"
            xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing"
            xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery"
            xmlns:dn="http://www.onvif.org/ver10/network/wsdl">
  <s:Header><a:RelatesTo>` + messageID + `</a:RelatesTo></s:Header>
  <s:Body><d:ProbeMatches>` + body + `</d:ProbeMatches></s:Body>
</s:Envelope>`
}

// probeMatchElement is one ProbeMatch element.
func probeMatchElement(uuid, types, xaddrs string) string {
	return `<d:ProbeMatch>
      <a:EndpointReference><a:Address>` + uuid + `</a:Address></a:EndpointReference>
      <d:Types>` + types + `</d:Types>
      <d:XAddrs>` + xaddrs + `</d:XAddrs>
    </d:ProbeMatch>`
}

// fakeProber replays canned payloads under a fixed message identifier, all of them
// arriving from the same host.
func fakeProber(messageID string, payloads ...string) prober {
	return fakeProberFrom(messageID, testProbeSource, payloads...)
}

// fakeProberFrom replays canned payloads that arrived from a given address.
func fakeProberFrom(messageID string, from netip.AddrPort, payloads ...string) prober {
	replies := make([]reply, len(payloads))
	for i, payload := range payloads {
		replies[i] = reply{payload: payload, from: from}
	}
	return func(context.Context, string, ProbeOptions) (string, []reply, error) {
		return messageID, replies, nil
	}
}

// testProbeSource is the host the canned replies come from.
var testProbeSource = netip.MustParseAddrPort("10.0.0.1:3702")

const testProbeID = "urn:uuid:11111111-1111-1111-1111-111111111111"

// TestDiscoverDeduplicatesAcrossPayloads checks that the same device answering every
// retransmission, and on both IP families, is reported once. The endpoint reference is
// the identity, so the differing addresses must not split it into two devices.
func TestDiscoverDeduplicatesAcrossPayloads(t *testing.T) {
	one := probeMatchesEnvelope(testProbeID,
		probeMatchElement("urn:uuid:cam-1", "dn:NetworkVideoTransmitter", "http://10.0.0.1/onvif/device_service"))
	two := probeMatchesEnvelope(testProbeID,
		probeMatchElement("urn:uuid:cam-1", "dn:NetworkVideoTransmitter", "http://[fe80::1]/onvif/device_service"))

	// one is replayed, which is the repeated-probe case: three payloads, two distinct
	// claims. The replay collapses; the two addresses do not, because this function
	// cannot tell a dual-stack device from two hosts claiming one endpoint reference,
	// and silently keeping the earlier of the two is how a forger took a camera's place.
	got, err := discoverOnInterface(context.Background(), "eth0", ProbeOptions{},
		fakeProber(testProbeID, one, two, one))
	if err != nil {
		t.Fatalf("discoverOnInterface() = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d devices, want 2 — the replay of one must collapse and the two "+
			"addresses must both survive: %+v", len(got), got)
	}

	urls := map[string]bool{}
	for _, device := range got {
		if device.UUID != "urn:uuid:cam-1" {
			t.Errorf("UUID = %q", device.UUID)
		}
		urls[device.DeviceServiceURL] = true
	}
	for _, want := range []string{
		"http://10.0.0.1/onvif/device_service",
		"http://[fe80::1]/onvif/device_service",
	} {
		if !urls[want] {
			t.Errorf("%s was dropped; both claims on this endpoint reference must reach "+
				"the caller, which is what lets it see that they disagree", want)
		}
	}
}

// TestAForgerDoesNotDisplaceADeviceItImpersonates pins the reason dedupKey carries the
// advertised address as well as the endpoint reference.
//
// An endpoint reference is multicast in every Hello and in every ProbeMatches on the link,
// so it is not a secret and any host can repeat one. Deduplication used to key on it alone
// and keep the first answer, while appMaxDelay requires a conformant Target Service to wait
// before answering — so a host that replied at once with a camera's endpoint reference and
// its own XAddrs took the camera's place, and the caller went on to dial it.
func TestAForgerDoesNotDisplaceADeviceItImpersonates(t *testing.T) {
	const uuid = "urn:uuid:cam-1"
	const cameraURL = "http://10.0.0.7/onvif/device_service"
	const forgedURL = "http://10.0.0.99/onvif/device_service"

	forger := netip.MustParseAddrPort("10.0.0.99:51000")
	camera := netip.MustParseAddrPort("10.0.0.7:3702")

	// The forger answers first, as it always can: it is under no obligation to wait.
	send := func(context.Context, string, ProbeOptions) (string, []reply, error) {
		return testProbeID, []reply{
			{payload: probeMatchesEnvelope(testProbeID,
				probeMatchElement(uuid, "dn:NetworkVideoTransmitter", forgedURL)), from: forger},
			{payload: probeMatchesEnvelope(testProbeID,
				probeMatchElement(uuid, "dn:NetworkVideoTransmitter", cameraURL)), from: camera},
		}, nil
	}

	got, err := discoverOnInterface(context.Background(), "eth0", ProbeOptions{}, send)
	if err != nil {
		t.Fatalf("discoverOnInterface() = %v", err)
	}

	var sawCamera bool
	for _, device := range got {
		if device.DeviceServiceURL == cameraURL && device.From == camera {
			sawCamera = true
		}
	}
	if !sawCamera {
		t.Errorf("the genuine answer from %v was discarded as a duplicate of the forged one: "+
			"a host answering first must not be able to take an endpoint reference from a "+
			"device that is obeying appMaxDelay (got %+v)", camera, got)
	}
}

// TestDiscoverFiltersNonOnvif checks the default keeps cameras and drops the printers
// and Windows hosts that answer an untyped Probe, and that IncludeNonOnvif keeps both.
func TestDiscoverFiltersNonOnvif(t *testing.T) {
	payload := probeMatchesEnvelope(testProbeID,
		probeMatchElement("urn:uuid:cam-1", "dn:NetworkVideoTransmitter", "http://10.0.0.1/onvif/device_service"),
		probeMatchElement("urn:uuid:printer-1", "p:PrintDeviceType", "http://10.0.0.2/print"))

	got, err := discoverOnInterface(context.Background(), "eth0", ProbeOptions{},
		fakeProber(testProbeID, payload))
	if err != nil {
		t.Fatalf("discoverOnInterface() = %v", err)
	}
	if len(got) != 1 || got[0].UUID != "urn:uuid:cam-1" {
		t.Fatalf("the printer must be dropped by default, got %+v", got)
	}

	got, err = discoverOnInterface(context.Background(), "eth0",
		ProbeOptions{IncludeNonOnvif: true}, fakeProber(testProbeID, payload))
	if err != nil {
		t.Fatalf("discoverOnInterface() = %v", err)
	}
	if len(got) != 2 {
		t.Errorf("IncludeNonOnvif must keep both, got %d: %+v", len(got), got)
	}
}

// TestDiscoverRejectsUncorrelatedReplies checks a reply relating to somebody else's
// Probe is discarded. Without this a stale or forged datagram would be accepted.
func TestDiscoverRejectsUncorrelatedReplies(t *testing.T) {
	payload := probeMatchesEnvelope("urn:uuid:someone-elses-probe",
		probeMatchElement("urn:uuid:cam-1", "dn:NetworkVideoTransmitter", "http://10.0.0.1/onvif/device_service"))

	got, err := discoverOnInterface(context.Background(), "eth0", ProbeOptions{},
		fakeProber(testProbeID, payload))
	if err != nil {
		t.Fatalf("discoverOnInterface() = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("an uncorrelated reply must be dropped, got %+v", got)
	}
}

// TestDiscoverDropsAddresslessMatches checks a match with no usable XAddrs is skipped
// rather than reported as a device with an empty address.
func TestDiscoverDropsAddresslessMatches(t *testing.T) {
	payload := probeMatchesEnvelope(testProbeID,
		probeMatchElement("urn:uuid:cam-1", "dn:NetworkVideoTransmitter", "not-a-url"))

	got, err := discoverOnInterface(context.Background(), "eth0", ProbeOptions{},
		fakeProber(testProbeID, payload))
	if err != nil {
		t.Fatalf("discoverOnInterface() = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("a match without a usable address must be dropped, got %+v", got)
	}
}

// TestDiscoverRecordsReplySource checks each device carries the address its reply came
// from, which Discover used to throw away while Listen kept it. Correlating a
// ProbeMatches with our Probe proves nothing about the sender: the Probe is multicast, so
// its message identifier is known to the whole link and any host there can answer with
// it. The source is the only thing about a reply that was observed rather than claimed.
func TestDiscoverRecordsReplySource(t *testing.T) {
	genuine := netip.MustParseAddrPort("10.0.0.1:3702")
	forger := netip.MustParseAddrPort("10.0.0.99:51000")

	send := func(ctx context.Context, iface string, opts ProbeOptions) (string, []reply, error) {
		return testProbeID, []reply{
			{payload: probeMatchesEnvelope(testProbeID, probeMatchElement("urn:uuid:cam-1",
				"dn:NetworkVideoTransmitter", "http://10.0.0.1/onvif/device_service")), from: genuine},
			{payload: probeMatchesEnvelope(testProbeID, probeMatchElement("urn:uuid:cam-2",
				"dn:NetworkVideoTransmitter", "http://10.0.0.2/onvif/device_service")), from: forger},
		}, nil
	}

	devices, err := discoverOnInterface(context.Background(), "eth0", ProbeOptions{}, send)
	if err != nil {
		t.Fatalf("discoverOnInterface: %v", err)
	}
	if len(devices) != 2 {
		t.Fatalf("got %d devices, want 2", len(devices))
	}
	want := map[string]netip.AddrPort{
		"urn:uuid:cam-1": genuine,
		"urn:uuid:cam-2": forger,
	}
	for _, device := range devices {
		if device.From != want[device.UUID] {
			t.Errorf("%s came From %v, want %v", device.UUID, device.From, want[device.UUID])
		}
	}

	// The second device advertises 10.0.0.2 while answering from 10.0.0.99. Telling the
	// two apart is the whole point of keeping the source.
	for _, device := range devices {
		if device.UUID == "urn:uuid:cam-2" && device.Xaddr == device.From.Addr().String() {
			t.Error("the advertised address and the sender must not be conflated")
		}
	}
}

// TestDiscoverStopsParsingWhenContextEnds checks that the collection window is not the
// only bound on how long a probe takes. Parsing runs after the window closes and used to
// ignore the context entirely, so a flood of crafted datagrams kept Discover busy long
// after its deadline: a 100ms deadline was observed returning after 17s.
//
// A context already over yields no devices and no error, matching what probe does with a
// context that ends mid-window: what was collected is a result, not a failure.
func TestDiscoverStopsParsingWhenContextEnds(t *testing.T) {
	payload := probeMatchesEnvelope(testProbeID,
		probeMatchElement("urn:uuid:cam-1", "dn:NetworkVideoTransmitter",
			"http://10.0.0.1/onvif/device_service"))
	payloads := make([]string, 64)
	for i := range payloads {
		payloads[i] = payload
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	devices, err := discoverOnInterface(ctx, "eth0", ProbeOptions{},
		fakeProber(testProbeID, payloads...))
	if err != nil {
		t.Fatalf("discoverOnInterface: %v", err)
	}
	if len(devices) != 0 {
		t.Errorf("parsed %d devices under a context that had already ended", len(devices))
	}

	// The same payloads under a live context must still yield the device, so the check
	// above cannot pass by accident.
	devices, err = discoverOnInterface(context.Background(), "eth0", ProbeOptions{},
		fakeProber(testProbeID, payloads...))
	if err != nil || len(devices) != 1 {
		t.Fatalf("got %d devices, err %v, want 1 device", len(devices), err)
	}
}

// TestDiscoverPropagatesProbeError checks a failure to probe is reported rather than
// reported as an empty result.
func TestDiscoverPropagatesProbeError(t *testing.T) {
	want := errors.New("no such interface")
	failing := func(context.Context, string, ProbeOptions) (string, []reply, error) {
		return "", nil, want
	}
	if _, err := discoverOnInterface(context.Background(), "eth0", ProbeOptions{}, failing); !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
}
