// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// SPDX-License-Identifier: MIT

//go:build audit

// The CLI defects found by the 2026-09-11 whole-tree audit. See wsd/audit_test.go for why
// these sit behind a build tag, and .claude/AUDIT-2026-09-11.md for the evidence.

package main

import (
	"bytes"
	"net/netip"
	"strings"
	"testing"

	"github.com/ontavu/go-wsd/wsd"
)

// TestDiscoverRowCarriesTheObservedSource — audit A3.
//
// Device.From is the only field that was observed rather than claimed, and wsd/device.go
// keeps it end to end for exactly that reason. printAnnouncement prints it; printDevice
// does not, so on the probe path it never reaches the operator.
//
// That matters because of audit A2: a host answering first with a camera's endpoint
// reference displaces the camera, and the row that reaches an inventory then carries the
// attacker's URL with nothing to say who sent it.
func TestDiscoverRowCarriesTheObservedSource(t *testing.T) {
	var out bytes.Buffer
	device := wsd.Device{
		Xaddr:            "10.0.0.99",
		UUID:             "urn:uuid:cam-1",
		DeviceServiceURL: "http://10.0.0.99/onvif/device_service",
		From:             netip.MustParseAddrPort("10.0.0.99:51000"),
	}
	printDevice(&out, "eth0", device)

	row := strings.TrimRight(out.String(), "\n")
	if !strings.Contains(row, device.From.String()) {
		t.Errorf("the discover row is %q and carries no source address; listen prints From "+
			"and discover drops it, so a forged reply is recorded with no trace of who sent "+
			"it (want a column holding %s)", row, device.From)
	}
}
