// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// SPDX-License-Identifier: MIT

package wsd

import (
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

const soapEnvelopeNS = "http://www.w3.org/2003/05/soap-envelope"

// assertNamespaceWellFormed fails if any element or attribute name in the document uses
// a prefix that is not declared in scope.
//
// encoding/xml does not report undeclared prefixes: it resolves a declared prefix to its
// URI in Name.Space, and leaves the bare prefix there when the declaration is missing.
// A Space that is not a URI is therefore exactly the symptom of an undeclared prefix,
// which is how the d:Scopes defect is detected.
func assertNamespaceWellFormed(t *testing.T, document string) {
	t.Helper()

	decoder := xml.NewDecoder(strings.NewReader(document))
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatalf("the probe is not well-formed XML: %v", err)
		}

		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		if !resolvedNamespace(start.Name.Space) {
			t.Errorf("element %q uses the undeclared prefix %q", start.Name.Local, start.Name.Space)
		}
		for _, attr := range start.Attr {
			if attr.Name.Space == "xmlns" || attr.Name.Local == "xmlns" {
				continue
			}
			if !resolvedNamespace(attr.Name.Space) {
				t.Errorf("attribute %q of %q uses the undeclared prefix %q",
					attr.Name.Local, start.Name.Local, attr.Name.Space)
			}
		}
	}
}

// resolvedNamespace reports whether a Name.Space is a namespace URI rather than a bare,
// unresolved prefix. An empty space is the absence of a namespace, which is fine.
func resolvedNamespace(space string) bool {
	return space == "" || strings.Contains(space, ":")
}

// TestProbeIsNamespaceWellFormed covers the envelope with and without scopes. The scoped
// form is the one that used to be broken: xmlns:d was declared on d:Types, leaving the
// sibling d:Scopes with an undeclared prefix.
func TestProbeIsNamespaceWellFormed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		scopes []string
		types  []TypeName
	}{
		{"untyped probe", nil, nil},
		{"types only", nil, []TypeName{TypeONVIFNetworkVideoTransmitter}},
		{"scopes only", []string{"onvif://www.onvif.org/location/country/france"}, nil},
		{"types and scopes", []string{"onvif://www.onvif.org"}, []TypeName{TypeONVIFNetworkVideoTransmitter}},
		{"two namespaces", nil, []TypeName{TypeONVIFNetworkVideoTransmitter, TypeONVIFDevice}},
		{"two types, one namespace", nil, []TypeName{TypeONVIFNetworkVideoTransmitter, TypeONVIFNetworkVideoStorage}},
		// A caller can build a TypeName by hand; an incomplete one must not be able to
		// make the Probe ill-formed for everyone on the link.
		{"incomplete type", nil, []TypeName{{Local: "NoNamespace"}, {Namespace: "urn:x"}}},
		{"incomplete among usable", nil, []TypeName{{Local: "NoNamespace"}, TypeONVIFDevice}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, fl := range []dialect{draft2005, oasis11} {
				msg := buildProbeMessage(testMessageID, tc.scopes, tc.types, fl).String()
				assertNamespaceWellFormed(t, msg)
			}
		})
	}
}

// TestProbeMustUnderstandIsQualified checks the attribute carries the SOAP envelope
// namespace. SOAP 1.2 Part 1 section 5.2.3 gives mustUnderstand a [namespace name] of
// the envelope namespace, so an unqualified one is a foreign attribute that a conformant
// receiver ignores, silently making the header optional to understand.
func TestProbeMustUnderstandIsQualified(t *testing.T) {
	msg := buildProbeMessage(testMessageID, nil, []TypeName{TypeONVIFDevice}, draft2005).String()

	marked := map[string]bool{}
	decoder := xml.NewDecoder(strings.NewReader(msg))
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("probe is not well-formed: %v", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		for _, attr := range start.Attr {
			if attr.Name.Local != "mustUnderstand" {
				continue
			}
			if attr.Name.Space != soapEnvelopeNS {
				t.Errorf("mustUnderstand on %q has namespace %q, want the SOAP envelope namespace",
					start.Name.Local, attr.Name.Space)
			}
			marked[start.Name.Local] = true
		}
	}

	for _, header := range []string{"Action", "To"} {
		if !marked[header] {
			t.Errorf("%s is not marked mustUnderstand", header)
		}
	}
}

// TestProbeCarriesFlavorURIs checks each dialect emits its own four identifying URIs.
// This is what makes the dual stack meaningful: a device listening for one dialect
// ignores the other outright.
func TestProbeCarriesFlavorURIs(t *testing.T) {
	for _, fl := range []dialect{draft2005, oasis11} {
		t.Run(fl.name, func(t *testing.T) {
			msg := buildProbeMessage(testMessageID, nil, nil, fl).String()

			for _, want := range []string{fl.probeAction, fl.to, fl.addressing, fl.anonymous, fl.discovery} {
				if !strings.Contains(msg, want) {
					t.Errorf("probe is missing %q\ngot: %s", want, msg)
				}
			}

			// And it must not leak the other dialect's identity.
			other := oasis11
			if fl.name == oasis11.name {
				other = draft2005
			}
			for _, unwanted := range []string{other.probeAction, other.to, other.anonymous} {
				if strings.Contains(msg, unwanted) {
					t.Errorf("probe leaks the %s URI %q", other.name, unwanted)
				}
			}
		})
	}
}

// TestProbeCarriesEmptyTypesWhenUntyped is the regression guard for the reason cameras
// went unfound. WS-Discovery says an absent d:Types matches every Target Service, so the
// element used to be omitted; equipment reads it differently. Measured on one link: a
// Probe with no d:Types drew one reply out of four devices, the same Probe carrying an
// empty one drew all four, including a non-ONVIF host that a typed Probe excludes.
//
// The element must be empty, not blank: <d:Types> </d:Types> lost the non-ONVIF device,
// which parses the single space as a type list matching nothing.
func TestProbeCarriesEmptyTypesWhenUntyped(t *testing.T) {
	msg := buildProbeMessage(testMessageID, nil, nil, draft2005).String()

	if !strings.Contains(msg, "<d:Types/>") {
		t.Errorf("an untyped probe must carry an empty d:Types element\ngot: %s", msg)
	}
	if strings.Contains(msg, "Scopes") {
		t.Errorf("an untyped probe must not carry a Scopes element\ngot: %s", msg)
	}
	if !strings.Contains(msg, "Probe") {
		t.Errorf("the probe body is missing\ngot: %s", msg)
	}
}

// TestProbeCarriesMessageID checks the identifier reaches the wire, since correlation of
// the replies depends on it.
func TestProbeCarriesMessageID(t *testing.T) {
	msg := buildProbeMessage(testMessageID, nil, nil, draft2005).String()
	if !strings.Contains(msg, testMessageID) {
		t.Errorf("the message identifier is missing\ngot: %s", msg)
	}
}

// TestProbeTypesAndScopesContent checks the QName and URI lists are emitted as
// space-delimited element content.
func TestProbeTypesAndScopesContent(t *testing.T) {
	msg := buildProbeMessage(testMessageID,
		[]string{"onvif://www.onvif.org/a", "onvif://www.onvif.org/b"},
		[]TypeName{TypeONVIFNetworkVideoTransmitter, TypeONVIFDevice},
		draft2005).String()

	for _, want := range []string{
		// one prefix per distinct namespace, invented here and declared on d:Types
		`xmlns:t0="http://www.onvif.org/ver10/network/wsdl"`,
		`xmlns:t1="http://www.onvif.org/ver10/device/wsdl"`,
		"t0:NetworkVideoTransmitter t1:Device",
		"onvif://www.onvif.org/a onvif://www.onvif.org/b",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("probe is missing %q\ngot: %s", want, msg)
		}
	}
}
