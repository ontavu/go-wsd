// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// SPDX-License-Identifier: MIT

package wsd

import (
	"sort"
	"strings"
	"testing"
)

// TestParseTypeName covers both forms a caller can write: a short name for equipment
// this package knows, and the explicit "{namespace}local" pair, which is the form a
// discovered type is reported in and so can be pasted back into a narrower probe.
func TestParseTypeName(t *testing.T) {
	for _, tc := range []struct {
		name    string
		raw     string
		want    TypeName
		wantErr bool
	}{
		{"well known", "onvif-nvt", TypeONVIFNetworkVideoTransmitter, false},
		{"well known, device", "onvif-device", TypeONVIFDevice, false},
		{"well known, not onvif", "printer", TypeWindowsPrinter, false},
		{"case insensitive", "ONVIF-NVT", TypeONVIFNetworkVideoTransmitter, false},
		{"surrounding space", "  onvif-nvt  ", TypeONVIFNetworkVideoTransmitter, false},
		{"explicit pair", "{urn:x}Thing", TypeName{"urn:x", "Thing"}, false},
		{"explicit pair, onvif", "{" + onvifDeviceNamespace + "}Device", TypeONVIFDevice, false},

		{"unknown name", "camera", TypeName{}, true},
		{"empty", "", TypeName{}, true},
		{"unclosed brace", "{urn:x", TypeName{}, true},
		{"no local name", "{urn:x}", TypeName{}, true},
		{"no namespace", "{}Thing", TypeName{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseTypeName(tc.raw)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ParseTypeName(%q) err = %v, wantErr %v", tc.raw, err, tc.wantErr)
			}
			if err == nil && got != tc.want {
				t.Errorf("ParseTypeName(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

// TestParseTypeNameErrorLists checks the error names the alternatives, since a wrong
// type silently narrows a probe to nothing and the user has no other way to find out
// what is spelled how.
func TestParseTypeNameErrorLists(t *testing.T) {
	_, err := ParseTypeName("nope")
	if err == nil {
		t.Fatal("an unknown type must be reported")
	}
	for _, want := range []string{"onvif-nvt", "{namespace}LocalName"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q: %v", want, err)
		}
	}
}

// TestWellKnownTypeNamesAreSortedAndParse checks the list a command line prints is
// stable and that every entry round-trips.
func TestWellKnownTypeNamesAreSortedAndParse(t *testing.T) {
	names := WellKnownTypeNames()
	if len(names) == 0 {
		t.Fatal("no well-known types")
	}
	if !sort.StringsAreSorted(names) {
		t.Errorf("WellKnownTypeNames() is unsorted: %v", names)
	}
	for _, name := range names {
		parsed, err := ParseTypeName(name)
		if err != nil {
			t.Errorf("ParseTypeName(%q): %v", name, err)
			continue
		}
		if parsed.Namespace == "" || parsed.Local == "" {
			t.Errorf("%q resolves to an incomplete type %v", name, parsed)
		}
	}
}

// TestTypeNameStringMatchesReportedTypes checks a TypeName renders exactly as the
// resolved types of a match do, which is what makes the explicit form paste-able.
func TestTypeNameStringMatchesReportedTypes(t *testing.T) {
	for _, want := range []TypeName{TypeONVIFDevice, TypeWindowsPrinter, {"urn:x", "Y"}} {
		namespace, local := splitResolved(want.String())
		if namespace != want.Namespace || local != want.Local {
			t.Errorf("%q split to %q, %q", want, namespace, local)
		}
		got, err := ParseTypeName(want.String())
		if err != nil || got != want {
			t.Errorf("ParseTypeName(%q) = %v, %v", want, got, err)
		}
	}
}

// TestONVIFFilterAgreesWithTheWellKnownTypes checks the two lists cannot drift: a probe
// asking for an ONVIF type must accept the reply it draws.
func TestONVIFFilterAgreesWithTheWellKnownTypes(t *testing.T) {
	for _, onvif := range []TypeName{TypeONVIFDevice, TypeONVIFNetworkVideoTransmitter} {
		if !isOnvifDevice([]string{onvif.String()}) {
			t.Errorf("%v is probed for but not accepted as ONVIF", onvif)
		}
	}
	for _, other := range []TypeName{TypeWindowsPrinter, TypeDPWSDevice, TypeWindowsDevice} {
		if isOnvifDevice([]string{other.String()}) {
			t.Errorf("%v is not an ONVIF port type but passes the filter", other)
		}
	}
}
