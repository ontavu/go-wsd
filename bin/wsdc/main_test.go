// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// SPDX-License-Identifier: MIT

package main

import (
	"testing"

	"github.com/ontavu/go-wsd/wsd"
)

// TestOrDashKeepsHostileFieldsInTheirColumn is the regression guard for the row this
// command prints. Every field but the timestamp and the sender comes from an
// unauthenticated datagram, and the row is tab-separated, so a tab or a newline in a
// UUID forges an entry that never existed.
//
// XML parsing already refuses a raw escape byte and a character reference to one, which
// rules out colour and cursor sequences; what it lets through is ordinary character data,
// and that is enough.
func TestOrDashKeepsHostileFieldsInTheirColumn(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  string
	}{
		{"absent", "", "-"},
		{"blank", " \t\n ", "-"},
		{"ordinary uuid", "urn:uuid:cam-1", "urn:uuid:cam-1"},
		{"ordinary url", "http://10.0.0.1/onvif/device_service", "http://10.0.0.1/onvif/device_service"},
		{"spaces are kept", "urn:uuid:a b", "urn:uuid:a b"},

		{"forged column", "urn:uuid:a\tHello", "urn:uuid:a�Hello"},
		{"forged row", "urn:uuid:a\n2026-01-01T00:00:00Z", "urn:uuid:a�2026-01-01T00:00:00Z"},
		{"carriage return", "urn:uuid:a\rurn:uuid:b", "urn:uuid:a�urn:uuid:b"},
		{"escape byte", "urn:uuid:\x1b[31m", "urn:uuid:�[31m"},
		{"bell", "urn:uuid:a\x07", "urn:uuid:a�"},
		// A format character contributes no glyph but reorders the line around it, so a
		// hostile address can be made to read as a trusted one.
		{"right to left override", "http://10.0.0.1/‮gnp.eliforp", "http://10.0.0.1/�gnp.eliforp"},
		{"zero width space", "urn:uuid:a​b", "urn:uuid:a�b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := orDash(tc.value); got != tc.want {
				t.Errorf("orDash(%q) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

// TestParseTypes covers the -types flag. An empty list is the untyped probe, which is
// the default and reaches every Target Service; a wrong name has to be reported rather
// than silently narrowing the probe to nothing.
func TestParseTypes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		raw     string
		want    []wsd.TypeName
		wantErr bool
	}{
		{"empty is the untyped probe", "", nil, false},
		{"blank is the untyped probe", "  ", nil, false},
		{"one well-known name", "onvif-nvt",
			[]wsd.TypeName{wsd.TypeONVIFNetworkVideoTransmitter}, false},
		{"several", "onvif-nvt,printer",
			[]wsd.TypeName{wsd.TypeONVIFNetworkVideoTransmitter, wsd.TypeWindowsPrinter}, false},
		{"spaces around the commas", " onvif-nvt , onvif-device ",
			[]wsd.TypeName{wsd.TypeONVIFNetworkVideoTransmitter, wsd.TypeONVIFDevice}, false},
		{"trailing comma", "onvif-nvt,",
			[]wsd.TypeName{wsd.TypeONVIFNetworkVideoTransmitter}, false},
		{"explicit pair", "{urn:x}Thing", []wsd.TypeName{{Namespace: "urn:x", Local: "Thing"}}, false},
		{"mixed forms", "onvif-nvt,{urn:x}Thing",
			[]wsd.TypeName{wsd.TypeONVIFNetworkVideoTransmitter, {Namespace: "urn:x", Local: "Thing"}}, false},

		{"unknown name", "camera", nil, true},
		{"one bad among good", "onvif-nvt,camera", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseTypes(tc.raw)
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseTypes(%q) err = %v, wantErr %v", tc.raw, err, tc.wantErr)
			}
			if err != nil {
				return
			}
			if len(got) != len(tc.want) {
				t.Fatalf("parseTypes(%q) = %v, want %v", tc.raw, got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("parseTypes(%q)[%d] = %v, want %v", tc.raw, i, got[i], tc.want[i])
				}
			}
		})
	}
}
