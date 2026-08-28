// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// SPDX-License-Identifier: MIT

package wsd

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// probeMatchesReply wraps ProbeMatch fragments in a realistic ProbeMatches datagram,
// using the namespace prefixes a real device would.
func probeMatchesReply(relatesTo, matches string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<SOAP-ENV:Envelope xmlns:SOAP-ENV="http://www.w3.org/2003/05/soap-envelope"
                   xmlns:wsa="http://schemas.xmlsoap.org/ws/2004/08/addressing"
                   xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery"
                   xmlns:dn="http://www.onvif.org/ver10/network/wsdl"
                   xmlns:tds="http://www.onvif.org/ver10/device/wsdl">
 <SOAP-ENV:Header>
  <wsa:Action>http://schemas.xmlsoap.org/ws/2005/04/discovery/ProbeMatches</wsa:Action>
  <wsa:RelatesTo>` + relatesTo + `</wsa:RelatesTo>
 </SOAP-ENV:Header>
 <SOAP-ENV:Body><d:ProbeMatches>` + matches + `</d:ProbeMatches></SOAP-ENV:Body>
</SOAP-ENV:Envelope>`
}

func probeMatch(uuid, types, xaddrs string) string {
	return `<d:ProbeMatch>
	<wsa:EndpointReference><wsa:Address>` + uuid + `</wsa:Address></wsa:EndpointReference>
	<d:Types>` + types + `</d:Types>
	<d:XAddrs>` + xaddrs + `</d:XAddrs>
	</d:ProbeMatch>`
}

const testMessageID = "urn:uuid:11111111-2222-3333-4444-555555555555"

// TestParseProbeMatchesHostileInput is the regression guard for a panic any host on the
// link could trigger. etree never reports a parse error and leaves the root nil on
// non-XML input, so the previous code dereferenced nil while parsing a datagram that
// arrives unauthenticated over UDP.
func TestParseProbeMatchesHostileInput(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
	}{
		{"empty", ""},
		{"whitespace", "   \n\t "},
		{"plain text", "hello world"},
		{"binary junk", "\x00\x01\x02\xff\xfe"},
		{"truncated xml", `<?xml version="1.0"?><Envelope><Body>`},
		{"no root", `<?xml version="1.0"?>`},
		{"html", "<html><body>nope</body></html>"},
		{"envelope without body", `<Envelope><Header/></Envelope>`},
		{"hello announcement", `<Envelope><Header><RelatesTo>` + testMessageID +
			`</RelatesTo></Header><Body><Hello><XAddrs>http://10.0.0.1/onvif</XAddrs></Hello></Body></Envelope>`},
		{"probematch without address", probeMatchesReply(testMessageID,
			`<d:ProbeMatch><d:Types>tds:Device</d:Types></d:ProbeMatch>`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The requirement is simply that this returns, whatever the input.
			if got := parseProbeMatches(tc.payload, testMessageID); len(got) != 0 {
				t.Errorf("parseProbeMatches() = %+v, want none", got)
			}
		})
	}
}

// TestParseProbeMatchesCorrelation checks the wsa:RelatesTo correlation WS-Discovery
// requires. Without it, a stale reply to an earlier probe or a datagram forged by any
// host on the link is accepted as a device.
func TestParseProbeMatchesCorrelation(t *testing.T) {
	body := probeMatch("urn:uuid:cam-1", "dn:NetworkVideoTransmitter", "http://10.0.0.1/onvif/device_service")

	if got := parseProbeMatches(probeMatchesReply(testMessageID, body), testMessageID); len(got) != 1 {
		t.Fatalf("the reply to our own probe was rejected: %+v", got)
	}

	other := "urn:uuid:99999999-9999-9999-9999-999999999999"
	if got := parseProbeMatches(probeMatchesReply(other, body), testMessageID); len(got) != 0 {
		t.Errorf("a reply relating to another probe was accepted: %+v", got)
	}

	noRelatesTo := `<Envelope><Body><ProbeMatches>` + body + `</ProbeMatches></Body></Envelope>`
	if got := parseProbeMatches(noRelatesTo, testMessageID); len(got) != 0 {
		t.Errorf("a reply carrying no RelatesTo was accepted: %+v", got)
	}
}

// TestParseProbeMatchesSeveralMatches is the regression guard for the UUID pairing: the
// previous code took the first endpoint reference of the datagram and attributed it to
// every ProbeMatch, which mislabels every device but one. A Discovery Proxy answers with
// several matches routinely.
func TestParseProbeMatchesSeveralMatches(t *testing.T) {
	payload := probeMatchesReply(testMessageID,
		probeMatch("urn:uuid:cam-1", "tds:Device", "http://10.0.0.1/onvif/device_service")+
			probeMatch("urn:uuid:cam-2", "tds:Device", "http://10.0.0.2/onvif/device_service"))

	got := parseProbeMatches(payload, testMessageID)
	if len(got) != 2 {
		t.Fatalf("got %d matches, want 2", len(got))
	}
	for i, want := range []struct{ uuid, xaddr string }{
		{"urn:uuid:cam-1", "http://10.0.0.1/onvif/device_service"},
		{"urn:uuid:cam-2", "http://10.0.0.2/onvif/device_service"},
	} {
		if got[i].UUID != want.uuid {
			t.Errorf("match %d UUID = %q, want %q", i, got[i].UUID, want.uuid)
		}
		if len(got[i].XAddrs) != 1 || got[i].XAddrs[0] != want.xaddr {
			t.Errorf("match %d XAddrs = %v, want [%s]", i, got[i].XAddrs, want.xaddr)
		}
	}
}

// TestParseProbeMatchesResolvesTypes checks the advertised port types are resolved
// against the declarations of the reply, since a prefix means nothing on its own.
func TestParseProbeMatchesResolvesTypes(t *testing.T) {
	payload := probeMatchesReply(testMessageID,
		probeMatch("urn:uuid:cam-1", "dn:NetworkVideoTransmitter tds:Device",
			"http://10.0.0.1/onvif/device_service"))

	got := parseProbeMatches(payload, testMessageID)
	if len(got) != 1 {
		t.Fatalf("got %d matches, want 1", len(got))
	}
	want := []string{
		"{http://www.onvif.org/ver10/network/wsdl}NetworkVideoTransmitter",
		"{http://www.onvif.org/ver10/device/wsdl}Device",
	}
	if len(got[0].Types) != len(want) {
		t.Fatalf("Types = %v, want %v", got[0].Types, want)
	}
	for i := range want {
		if got[0].Types[i] != want[i] {
			t.Errorf("Types[%d] = %q, want %q", i, got[0].Types[i], want[i])
		}
	}
}

// TestTypeResolutionScopesAndShadowing pins the namespace scoping rules: the nearest
// declaration wins and d:Types is the innermost scope, an xmlns="" style undeclaration is
// passed over rather than treated as a binding, an unprefixed QName takes the default
// declaration, and an unknown prefix resolves to nothing.
func TestTypeResolutionScopesAndShadowing(t *testing.T) {
	payload := `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"
            xmlns:wsa="http://schemas.xmlsoap.org/ws/2004/08/addressing"
            xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery"
            xmlns:t="urn:outer" xmlns:u="urn:kept" xmlns="urn:default">
 <s:Header><wsa:RelatesTo>` + testMessageID + `</wsa:RelatesTo></s:Header>
 <s:Body><d:ProbeMatches>
  <d:ProbeMatch xmlns:t="urn:inner" xmlns:u="">
   <wsa:EndpointReference><wsa:Address>urn:uuid:cam-1</wsa:Address></wsa:EndpointReference>
   <d:Types xmlns:t="urn:innermost" xmlns:w="urn:on-types">t:Shadowed w:OnTypes u:Undeclared Bare v:Unknown</d:Types>
   <d:XAddrs>http://10.0.0.1/onvif/device_service</d:XAddrs>
  </d:ProbeMatch>
 </d:ProbeMatches></s:Body>
</s:Envelope>`

	got := parseProbeMatches(payload, testMessageID)
	if len(got) != 1 {
		t.Fatalf("got %d matches, want 1", len(got))
	}
	want := []string{
		"{urn:innermost}Shadowed", // d:Types outranks the ProbeMatch and the envelope
		"{urn:on-types}OnTypes",   // declared on d:Types and nowhere else
		"{urn:kept}Undeclared",    // xmlns:u="" does not shadow the outer binding
		"{urn:default}Bare",       // no prefix, so the default declaration applies
		"{}Unknown",               // never declared anywhere
	}
	if len(got[0].Types) != len(want) {
		t.Fatalf("Types = %v, want %v", got[0].Types, want)
	}
	for i := range want {
		if got[0].Types[i] != want[i] {
			t.Errorf("Types[%d] = %q, want %q", i, got[0].Types[i], want[i])
		}
	}
}

// TestTypeResolutionOnTypesElement is the regression guard for a camera that declares its
// prefixes the way this package itself does. probeBody puts the caller's namespace map on
// d:Types, because that is where the QNames it declares are used; resolution used to
// start at the enclosing ProbeMatch and so never saw those declarations. Types resolved
// to {}Device, isOnvifDevice rejected it, and the device disappeared from a default
// probe.
func TestTypeResolutionOnTypesElement(t *testing.T) {
	payload := `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"
            xmlns:wsa="http://schemas.xmlsoap.org/ws/2004/08/addressing"
            xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery">
 <s:Header><wsa:RelatesTo>` + testMessageID + `</wsa:RelatesTo></s:Header>
 <s:Body><d:ProbeMatches>
  <d:ProbeMatch>
   <wsa:EndpointReference><wsa:Address>urn:uuid:cam-1</wsa:Address></wsa:EndpointReference>
   <d:Types xmlns:tds="http://www.onvif.org/ver10/device/wsdl">tds:Device</d:Types>
   <d:XAddrs>http://10.0.0.1/onvif/device_service</d:XAddrs>
  </d:ProbeMatch>
 </d:ProbeMatches></s:Body>
</s:Envelope>`

	got := parseProbeMatches(payload, testMessageID)
	if len(got) != 1 {
		t.Fatalf("got %d matches, want 1", len(got))
	}
	want := "{http://www.onvif.org/ver10/device/wsdl}Device"
	if len(got[0].Types) != 1 || got[0].Types[0] != want {
		t.Fatalf("Types = %v, want [%s]", got[0].Types, want)
	}
	if !isOnvifDevice(got[0].Types) {
		t.Error("a device declaring tds on its own d:Types was not recognised as ONVIF")
	}
}

// craftedTypesReply builds a reply designed to maximise the cost of resolving its
// QNames: decoy attributes on the root that every lookup has to scan past, and as many
// prefixed QNames in d:Types as asked for.
func craftedTypesReply(attrs, qnames int) string {
	var b strings.Builder
	b.WriteString(`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"` +
		` xmlns:wsa="http://schemas.xmlsoap.org/ws/2004/08/addressing"` +
		` xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery"`)
	for i := 0; i < attrs; i++ {
		fmt.Fprintf(&b, ` z%d="1"`, i)
	}
	b.WriteString(`><s:Header><wsa:RelatesTo>` + testMessageID + `</wsa:RelatesTo></s:Header>`)
	b.WriteString(`<s:Body><d:ProbeMatches><d:ProbeMatch><d:Types>`)
	b.WriteString(strings.Repeat("q:Type ", qnames))
	b.WriteString(`</d:Types><d:XAddrs>http://10.0.0.1/onvif/device_service</d:XAddrs>`)
	b.WriteString(`</d:ProbeMatch></d:ProbeMatches></s:Body></s:Envelope>`)
	return b.String()
}

// TestTypeResolutionDoesNotBlowUpOnCraftedNamespaces guards the complexity, not the
// output. Resolution used to rescan every ancestor's attributes once per QName, so cost
// grew with the product of the two: a 59 KB datagram carrying 4000 decoy attributes and
// 5000 QNames took 45ms to parse, some 760 times an ordinary reply, and nothing on this
// path limits how many such datagrams a host on the link sends.
//
// The input here is ten times that in each dimension. The old resolver needs seconds for
// it; the memoised one takes tens of milliseconds, a few hundred under the race
// detector. The bound sits between the two with room on either side, so it fails on a
// return of the quadratic behaviour rather than on a slow machine.
func TestTypeResolutionDoesNotBlowUpOnCraftedNamespaces(t *testing.T) {
	payload := craftedTypesReply(40000, 50000)

	start := time.Now()
	got := parseProbeMatches(payload, testMessageID)
	elapsed := time.Since(start)

	if len(got) != 1 || len(got[0].Types) != 50000 {
		t.Fatalf("got %d matches, want 1 carrying 50000 types", len(got))
	}
	if elapsed > 2*time.Second {
		t.Errorf("parsing %d bytes took %v: namespace resolution is quadratic again",
			len(payload), elapsed)
	}
	t.Logf("parsed %d bytes in %v", len(payload), elapsed)
}

// BenchmarkParseProbeMatchesCrafted measures the datagram from the report above, sized
// to fit one UDP packet, so a regression shows up as a number rather than a timeout.
func BenchmarkParseProbeMatchesCrafted(b *testing.B) {
	payload := craftedTypesReply(4000, 5000)
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if len(parseProbeMatches(payload, testMessageID)) != 1 {
			b.Fatal("no match")
		}
	}
}

// TestIsOnvifDevice covers the type filter. ONVIF Core 19.12 section 7.3.2.1 mandates
// tds:Device; NetworkVideoTransmitter is the ONVIF 1.0 type that older cameras publish,
// sometimes exclusively. Types matching being conjunctive, a probe cannot ask for either
// one, so the selection happens here.
func TestIsOnvifDevice(t *testing.T) {
	for _, tc := range []struct {
		name  string
		types []string
		want  bool
	}{
		{"mandated device type", []string{"{http://www.onvif.org/ver10/device/wsdl}Device"}, true},
		{"legacy transmitter type", []string{"{http://www.onvif.org/ver10/network/wsdl}NetworkVideoTransmitter"}, true},
		{"both", []string{
			"{http://www.onvif.org/ver10/network/wsdl}NetworkVideoTransmitter",
			"{http://www.onvif.org/ver10/device/wsdl}Device"}, true},
		{"no type advertised", nil, true},
		{"unrelated namespace, same local name", []string{"{http://schemas.example.com/printer}Device"}, false},
		{"windows wsd", []string{"{http://schemas.microsoft.com/windows/pnpx/2005/10}Device"}, false},
		{"printer", []string{"{http://schemas.microsoft.com/windows/2006/08/wdp/print}PrintDeviceType"}, false},
		{"onvif namespace, unrelated local name", []string{"{http://www.onvif.org/ver10/network/wsdl}Something"}, false},
		// A substring test for "onvif.org" accepted every one of these. The namespace is
		// only a claim either way, but the filter has to mean what it says.
		{"onvif.org in a path", []string{"{http://evil.example/onvif.org/}Device"}, false},
		{"onvif.org as a prefix of the host", []string{"{http://notonvif.org.evil/}Device"}, false},
		{"onvif.org as a suffix of the host", []string{"{http://www.xonvif.org.evil/}Device"}, false},
		{"bare domain", []string{"{onvif.org}Device"}, false},
		{"trailing slash", []string{"{http://www.onvif.org/ver10/device/wsdl/}Device"}, false},
		{"one real type among decoys", []string{
			"{http://evil.example/onvif.org/}Device",
			"{http://www.onvif.org/ver10/device/wsdl}Device"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isOnvifDevice(tc.types); got != tc.want {
				t.Errorf("isOnvifDevice(%v) = %v, want %v", tc.types, got, tc.want)
			}
		})
	}
}

// TestParseXAddrs covers the values a device may put in an XAddrs element, and checks
// the scheme and path survive: ONVIF does not fix the device service path, and section
// 7.3.2.3 asks for one URI per protocol, https included.
func TestParseXAddrs(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want []string
	}{
		{"empty", "", nil},
		{"blank", "   \n\t ", nil},
		{"conventional", "http://192.168.1.10/onvif/device_service", []string{"http://192.168.1.10/onvif/device_service"}},
		{"non standard path", "http://192.168.0.10/onvif", []string{"http://192.168.0.10/onvif"}},
		{"https preserved", "https://10.0.0.3:443/onvif/device_service", []string{"https://10.0.0.3:443/onvif/device_service"}},
		{"with port", "http://192.168.1.10:8080/onvif/device_service", []string{"http://192.168.1.10:8080/onvif/device_service"}},
		{"several protocols", "http://10.0.0.1/onvif https://10.0.0.1/onvif",
			[]string{"http://10.0.0.1/onvif", "https://10.0.0.1/onvif"}},
		{"ipv6", "http://[fe80::1]:8080/onvif", []string{"http://[fe80::1]:8080/onvif"}},
		// Values that used to panic or produce garbage.
		{"no scheme", "192.168.1.10", nil},
		{"single slash", "http:/192.168.1.10", nil},
		{"garbage", "!!!", nil},
		{"scheme only", "http://", nil},
		{"mixed valid and garbage", "garbage http://10.0.0.4/onvif", []string{"http://10.0.0.4/onvif"}},
		// Addresses chosen to steer the caller. The list is what a caller dials, so a
		// scheme it has no reason to speak, or credentials it never agreed to send, is
		// dropped rather than handed on.
		{"gopher", "gopher://10.0.0.1:70/_x", nil},
		{"file with host", "file://10.0.0.1/etc/passwd", nil},
		{"jar", "jar:http://10.0.0.1/x!/y", nil},
		{"credentials", "http://admin:hunter2@10.0.0.1/onvif", nil},
		{"empty credentials", "http://@10.0.0.1/onvif", nil},
		{"credentials disguising the host", "http://trusted.example@10.0.0.1/onvif", nil},
		{"hostile among usable", "gopher://10.0.0.1/x http://10.0.0.1/onvif",
			[]string{"http://10.0.0.1/onvif"}},
		// The scheme is compared lowercased, which is what url.Parse produces; the host
		// is left exactly as advertised.
		{"uppercase scheme", "HTTP://10.0.0.1/onvif", []string{"http://10.0.0.1/onvif"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := parseXAddrs(tc.raw)
			if len(got) != len(tc.want) {
				t.Fatalf("parseXAddrs(%q) = %v, want %v", tc.raw, got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("parseXAddrs(%q)[%d] = %q, want %q", tc.raw, i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestNewMessageID checks the identifier is a URN, as RFC 4122 registers and ONVIF
// section 7.1 requires; "uuid:" is not a URI scheme.
func TestNewMessageID(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 50; i++ {
		got := newMessageID()
		if !strings.HasPrefix(got, "urn:uuid:") {
			t.Fatalf("newMessageID() = %q, want a urn:uuid prefix", got)
		}
		if seen[got] {
			t.Fatalf("message identifier %q reused", got)
		}
		seen[got] = true
	}
}

func TestSplitQNameAndResolved(t *testing.T) {
	if prefix, local := splitQName("tds:Device"); prefix != "tds" || local != "Device" {
		t.Errorf(`splitQName("tds:Device") = %q, %q`, prefix, local)
	}
	if prefix, local := splitQName("Device"); prefix != "" || local != "Device" {
		t.Errorf(`splitQName("Device") = %q, %q`, prefix, local)
	}
	if ns, local := splitResolved("{urn:x}Device"); ns != "urn:x" || local != "Device" {
		t.Errorf(`splitResolved("{urn:x}Device") = %q, %q`, ns, local)
	}
	if ns, local := splitResolved("Device"); ns != "" || local != "Device" {
		t.Errorf(`splitResolved("Device") = %q, %q`, ns, local)
	}
}
