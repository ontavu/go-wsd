// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// SPDX-License-Identifier: MIT

//go:build audit

// The defects found by the 2026-09-11 whole-tree audit, one test each. They are behind a
// build tag so that `go test ./...` and ci.yml stay green while the production code is
// untouched; `go test -tags audit ./...` is the defect list, and it executes, so an entry
// cannot stay on it after the defect is fixed.
//
// .claude/AUDIT-2026-09-11.md carries the evidence and the reasoning for each.

package wsd

import (
	"context"
	"net"
	"strings"
	"testing"
)

// TestDiscoverKeepsWhatAnsweredWhenTheProbeAlsoErrored — audit B2.
//
// discoverOnInterface returns nil on any error (wsd/discover.go:211-213), and probe drops
// the replies it had collected when every family ended in an error (:335). The comment at
// :338-344 argues the opposite rule for the context case — "Reporting it as an error too
// made the caller discard that work" — and the socket-error case has the same shape.
func TestDiscoverKeepsWhatAnsweredWhenTheProbeAlsoErrored(t *testing.T) {
	answered := probeMatchesEnvelope(testProbeID,
		probeMatchElement("urn:uuid:cam-1", "", "http://10.0.0.7/onvif/device_service"))

	send := func(context.Context, string, ProbeOptions) (string, []reply, error) {
		// What a family that collected replies and then hit a read error hands back.
		return testProbeID, []reply{{payload: answered, from: testProbeSource}}, net.ErrClosed
	}

	devices, err := discoverOnInterface(context.Background(), "eth0", ProbeOptions{}, send)
	if len(devices) == 0 {
		t.Errorf("a device answered and was discarded because the exchange also reported "+
			"%v: an error on the socket loses work the caller had already paid for, which "+
			"is the rule the context case was fixed to avoid (err = %v)", net.ErrClosed, err)
	}
}

// TestProbeOptionsAttemptsHasACeiling — audit B5.
//
// Timeout is clamped at both ends and MaxProbeTimeout exists because "a Timeout of 99999h,
// typed once or read from a configuration file, left readReplies collecting for years"
// (wsd/discover.go:76-82). Attempts is the same mistake with a worse consequence: it holds
// the socket for as long AND multicasts every one of those datagrams at the whole link.
func TestProbeOptionsAttemptsHasACeiling(t *testing.T) {
	const absurd = 1_000_000
	if got := (ProbeOptions{Attempts: absurd}).attempts(); got == absurd {
		t.Errorf("attempts() returned %d unchanged; one mistyped value multicasts %d Probes "+
			"per family per interface, bounded only by ctx, while the identical mistake in "+
			"Timeout is lowered to MaxProbeTimeout (%v)", got, absurd, MaxProbeTimeout)
	}
}

// TestProbeOptionsHopLimitHasACeiling — audit B5.
//
// hopLimit() passes any positive value to SetMulticastTTL, which rejects anything above
// 255 with "setsockopt: invalid argument". Measured: that failure propagates through
// setupV4, dialIPv4 and exchange, so every family fails and Discover reports a syscall
// error for the whole interface — an opaque message for a value that is simply too large.
func TestProbeOptionsHopLimitHasACeiling(t *testing.T) {
	const overTTL = 256
	if got := (ProbeOptions{HopLimit: overTTL}).hopLimit(); got > 255 {
		t.Errorf("hopLimit() returned %d; SetMulticastTTL rejects anything above 255, and "+
			"that failure fails every IP family, so Discover returns "+
			"\"setsockopt: invalid argument\" for the whole interface", got)
	}
}

// TestBufferLetsTheTruncationGuardFire — audit B6.
//
// readReplies and readAnnouncements drop a datagram that exactly fills the buffer, on the
// grounds that it may have been truncated into invalid XML. Measured on both families: the
// kernel refuses to send more than 65507 bytes over udp4 or 65527 over udp6 ("message too
// long"), and a maximum-size datagram reads back at exactly those sizes. bufSize is 65536,
// so n == len(buf) is unreachable by nine bytes and the guard is dead.
//
// The bound it justifies at discover.go:77-82 is right; the reason given for it is not, and
// the real reason is the flood in A1. Lowering bufSize without noticing this would silently
// discard every legitimate reply above the new size rather than truncate one.
func TestBufferLetsTheTruncationGuardFire(t *testing.T) {
	const maxUDPPayloadIPv6 = 65527 // 65535 minus the 8-byte UDP header
	if bufSize > maxUDPPayloadIPv6 {
		t.Errorf("bufSize is %d and the largest UDP datagram either family can carry is %d, "+
			"so the n == len(buf) guard in readReplies and readAnnouncements can never fire; "+
			"the two tests that cover it drive a fake conn and pin the code, not the situation",
			bufSize, maxUDPPayloadIPv6)
	}
}

// TestRelatesToRejectsAnEmptyWantedID — audit B7.
//
// relatesTo returns true for an empty wanted identifier (wsd/parse.go:100-102), which turns
// correlation off entirely: every datagram on the link is then accepted as a reply to us.
// Unreachable today, because the only caller passes newMessageID(). It is the trapdoor a
// new entry point falls through — SendProbe deliberately does not return the identifier, so
// a helper parsing its output has nothing but "" to pass.
func TestRelatesToRejectsAnEmptyWantedID(t *testing.T) {
	root := documentRoot(probeMatchesReply("urn:uuid:someone-elses-probe",
		probeMatch("urn:uuid:cam-1", "", "http://10.0.0.7/onvif/device_service")))
	if root == nil {
		t.Fatal("the fixture did not parse")
	}

	if relatesTo(root, "") {
		t.Error("relatesTo accepted a reply to somebody else's Probe because the wanted " +
			"message identifier was empty: the one function whose job is correlation has an " +
			"accept-everything default")
	}
}

// TestProbeWithScopesDeclaresItsMatchingRule — audit B8.
//
// The April 2005 draft's implied value for @MatchBy, when it is omitted, is the rfc2396
// rule. ONVIF Core section 7.3.3 overrides that with rfc3986, which is the URI the dialect
// stores in matchByRFC3986 (wsd/flavor.go:47) — and never emits. MatchBy appears nowhere in
// the tree but that comment.
//
// ProbeOptions.Scopes is exported and documented, so this is a live path: a caller who sets
// it gets rfc2396 prefix matching from a device that is only required to support rfc3986.
func TestProbeWithScopesDeclaresItsMatchingRule(t *testing.T) {
	message := buildProbeMessage(testMessageID,
		[]string{"onvif://www.onvif.org/location/Front"}, nil, draft2005).String()

	if !strings.Contains(message, "Scopes") {
		t.Fatal("the Probe carries no d:Scopes; this test is checking nothing")
	}
	if !strings.Contains(message, "MatchBy") {
		t.Errorf("a Probe carrying d:Scopes declares no MatchBy, so the receiver applies its "+
			"dialect default — rfc2396 for draft2005 — while ONVIF Core section 7.3.3 "+
			"requires rfc3986, which is the URI flavor.go stores and never sends:\n%s", message)
	}
}
