// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// SPDX-License-Identifier: MIT

package wsd

import (
	"context"
	"net"
	"os"
	"sync"
	"testing"
	"time"
)

// fakeConn is a packetConn that records what was sent and serves canned replies.
// It is the seam that makes the retransmission schedule and the collection loop
// testable without a socket.
type fakeConn struct {
	mu      sync.Mutex
	writes  []time.Time
	replies [][]byte
	closed  bool

	// endless is served once replies runs out, standing in for a host flooding the
	// link. SetReadDeadline is a no-op here, so the retention caps are the only thing
	// that can end the collection loop; endlessLimit stops a broken cap from hanging
	// the test rather than failing it.
	endless []byte
	served  int
}

const endlessLimit = 4 * maxReplies

func (c *fakeConn) SetReadDeadline(time.Time) error { return nil }

func (c *fakeConn) WriteTo(b []byte, _ net.Addr) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writes = append(c.writes, time.Now())
	return len(b), nil
}

func (c *fakeConn) ReadFrom(b []byte) (int, net.Addr, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.replies) == 0 {
		if c.endless == nil || c.served >= endlessLimit {
			return 0, nil, os.ErrDeadlineExceeded
		}
		c.served++
		return copy(b, c.endless), &net.UDPAddr{IP: net.IPv4(10, 0, 0, 1), Port: 3702}, nil
	}
	reply := c.replies[0]
	c.replies = c.replies[1:]
	return copy(b, reply), &net.UDPAddr{IP: net.IPv4(10, 0, 0, 1), Port: 3702}, nil
}

func (c *fakeConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *fakeConn) gaps() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []time.Duration
	for i := 1; i < len(c.writes); i++ {
		out = append(out, c.writes[i].Sub(c.writes[i-1]))
	}
	return out
}

// TestTransmitFollowsSoapOverUdpSchedule checks the retransmission schedule of
// SOAP-over-UDP 1.1 section 4: MULTICAST_UDP_REPEAT + 1 transmissions, separated by a
// random delay that doubles and is capped at UDP_UPPER_DELAY.
//
// The previous implementation sent the copies back to back, which exposes them to the
// same congestion event: load without reliability.
func TestTransmitFollowsSoapOverUdpSchedule(t *testing.T) {
	conn := &fakeConn{}
	if err := transmit(context.Background(), conn, nil, []byte("probe"), 3); err != nil {
		t.Fatalf("transmit: %v", err)
	}

	if got := len(conn.writes); got != 3 {
		t.Fatalf("sent %d datagrams, want 3", got)
	}

	gaps := conn.gaps()
	if len(gaps) != 2 {
		t.Fatalf("got %d gaps, want 2", len(gaps))
	}
	if gaps[0] < udpMinDelay {
		t.Errorf("first gap %v is below UDP_MIN_DELAY %v", gaps[0], udpMinDelay)
	}
	if gaps[0] > udpMaxDelay+150*time.Millisecond {
		t.Errorf("first gap %v exceeds UDP_MAX_DELAY %v by more than scheduling slack", gaps[0], udpMaxDelay)
	}
	if gaps[1] <= gaps[0] {
		t.Errorf("the delay did not back off: %v then %v", gaps[0], gaps[1])
	}
	if gaps[1] > udpUpperDelay+150*time.Millisecond {
		t.Errorf("second gap %v exceeds UDP_UPPER_DELAY %v", gaps[1], udpUpperDelay)
	}
}

// TestTransmitSingleAttempt checks no delay is paid when only one datagram is sent.
func TestTransmitSingleAttempt(t *testing.T) {
	conn := &fakeConn{}
	start := time.Now()
	if err := transmit(context.Background(), conn, nil, []byte("probe"), 1); err != nil {
		t.Fatalf("transmit: %v", err)
	}
	if len(conn.writes) != 1 {
		t.Errorf("sent %d datagrams, want 1", len(conn.writes))
	}
	if elapsed := time.Since(start); elapsed > udpMinDelay {
		t.Errorf("a single attempt waited %v", elapsed)
	}
}

// TestTransmitHonoursContext checks the back-off is interruptible, so a cancelled
// discovery does not keep multicasting.
func TestTransmitHonoursContext(t *testing.T) {
	conn := &fakeConn{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	if err := transmit(ctx, conn, nil, []byte("probe"), 3); err == nil {
		t.Error("a cancelled context must stop the retransmission")
	}
	if elapsed := time.Since(start); elapsed > udpMinDelay {
		t.Errorf("cancellation took %v to take effect", elapsed)
	}
	if got := len(conn.writes); got != 1 {
		t.Errorf("sent %d datagrams after cancellation, want 1", got)
	}
}

// TestReadRepliesCollects checks every queued datagram is returned.
func TestReadRepliesCollects(t *testing.T) {
	conn := &fakeConn{replies: [][]byte{[]byte("one"), []byte("two"), []byte("three")}}

	got, err := readReplies(context.Background(), conn, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("readReplies: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("collected %d datagrams, want 3", len(got))
	}
	if got[0].payload != "one" || got[2].payload != "three" {
		t.Errorf("collected %v", got)
	}
	// The source is recorded alongside the payload: it is the one thing about a reply
	// that was observed rather than claimed, and fakeConn answers from 10.0.0.1:3702.
	if from := got[0].from; from.String() != "10.0.0.1:3702" {
		t.Errorf("from = %v, want 10.0.0.1:3702", from)
	}
}

// TestReadRepliesDropsTruncated checks a datagram that filled the buffer is dropped
// rather than handed on as mangled XML, which would lose the device with no trace.
func TestReadRepliesDropsTruncated(t *testing.T) {
	oversized := make([]byte, bufSize)
	for i := range oversized {
		oversized[i] = 'x'
	}
	conn := &fakeConn{replies: [][]byte{oversized, []byte("small")}}

	got, err := readReplies(context.Background(), conn, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("readReplies: %v", err)
	}
	if len(got) != 1 || got[0].payload != "small" {
		t.Errorf("collected %d datagrams (%v), want only the small one", len(got), got)
	}
}

// TestReadRepliesBoundsRetainedDatagrams checks that a flood cannot decide how many
// replies a probe holds. The datagrams are kept until the window closes and parsed only
// afterwards, so without the cap an unauthenticated host on the link sets both the
// memory a probe costs and how long it runs.
func TestReadRepliesBoundsRetainedDatagrams(t *testing.T) {
	conn := &fakeConn{endless: []byte("<flood/>")}

	got, err := readReplies(context.Background(), conn, 30*time.Second)
	if err != nil {
		t.Fatalf("readReplies: %v", err)
	}
	if len(got) != maxReplies {
		t.Fatalf("collected %d datagrams, want the cap of %d", len(got), maxReplies)
	}
}

// TestReadRepliesBoundsRetainedBytes checks the byte cap, which is what maxReplies alone
// does not give: 512 maximum-size datagrams are 32 MiB. A two-second window against a
// flood of them used to retain 218 MiB, on one IP family of two.
func TestReadRepliesBoundsRetainedBytes(t *testing.T) {
	large := make([]byte, 60000)
	for i := range large {
		large[i] = 'x'
	}
	conn := &fakeConn{endless: large}

	got, err := readReplies(context.Background(), conn, 30*time.Second)
	if err != nil {
		t.Fatalf("readReplies: %v", err)
	}

	retained := 0
	for _, got := range got {
		retained += len(got.payload)
	}
	// The caps are tested before each read, so one datagram may cross the line.
	if retained > maxReplyBytes+bufSize {
		t.Errorf("retained %d bytes, want at most %d", retained, maxReplyBytes+bufSize)
	}
	if len(got) >= maxReplies {
		t.Errorf("collected %d datagrams: the byte cap should have bitten first", len(got))
	}
}

// TestReadRepliesHonoursCancelledContext checks collection stops promptly.
func TestReadRepliesHonoursCancelledContext(t *testing.T) {
	conn := &fakeConn{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	if _, err := readReplies(ctx, conn, 30*time.Second); err != nil {
		t.Fatalf("readReplies: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("cancellation took %v to take effect", elapsed)
	}
}

// TestProbeOptionsDefaults checks the zero value selects sane values.
func TestProbeOptionsDefaults(t *testing.T) {
	var zero ProbeOptions
	if got := zero.timeout(); got != DefaultProbeTimeout {
		t.Errorf("timeout() = %v, want %v", got, DefaultProbeTimeout)
	}
	if got := zero.attempts(); got != DefaultProbeAttempts {
		t.Errorf("attempts() = %v, want %v", got, DefaultProbeAttempts)
	}
	if got := zero.hopLimit(); got != DefaultHopLimit {
		t.Errorf("hopLimit() = %v, want %v", got, DefaultHopLimit)
	}
	if DefaultProbeAttempts != multicastUDPRepeat+1 {
		t.Errorf("DefaultProbeAttempts = %d, want MULTICAST_UDP_REPEAT + 1", DefaultProbeAttempts)
	}
}

// TestProbeOptionsTimeoutFloor is the guard for the collection window. A Target Service
// may wait up to APP_MAX_DELAY before answering, so a shorter window would
// systematically miss conformant devices.
func TestProbeOptionsTimeoutFloor(t *testing.T) {
	for _, given := range []time.Duration{time.Nanosecond, time.Millisecond, 100 * time.Millisecond, MatchTimeout - 1} {
		if got := (ProbeOptions{Timeout: given}).timeout(); got < MatchTimeout {
			t.Errorf("timeout() = %v for %v, want at least MatchTimeout %v", got, given, MatchTimeout)
		}
	}
	if got := (ProbeOptions{Timeout: 5 * time.Second}).timeout(); got != 5*time.Second {
		t.Errorf("timeout() = %v, a generous window must be honoured", got)
	}
	if MatchTimeout <= appMaxDelay {
		t.Errorf("MatchTimeout %v must exceed APP_MAX_DELAY %v", MatchTimeout, appMaxDelay)
	}
}

// TestDeviceOfPreservesSchemeAndPath is the regression guard for the discovered address:
// reducing XAddrs to a host forced every device onto http and /onvif/device_service.
func TestDeviceOfPreservesSchemeAndPath(t *testing.T) {
	for _, tc := range []struct {
		name     string
		xaddrs   []string
		wantURL  string
		wantHost string
	}{
		{"conventional", []string{"http://192.168.1.10/onvif/device_service"},
			"http://192.168.1.10/onvif/device_service", "192.168.1.10"},
		{"non standard path", []string{"http://192.168.0.10/onvif"},
			"http://192.168.0.10/onvif", "192.168.0.10"},
		{"https", []string{"https://10.0.0.3:443/onvif/device_service"},
			"https://10.0.0.3:443/onvif/device_service", "10.0.0.3:443"},
		{"first of several", []string{"http://10.0.0.1/onvif", "https://10.0.0.1/onvif"},
			"http://10.0.0.1/onvif", "10.0.0.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := deviceOf(match{UUID: "urn:uuid:x", XAddrs: tc.xaddrs}, testProbeSource)
			if !ok {
				t.Fatal("clientOf rejected a usable match")
			}
			if got.DeviceServiceURL != tc.wantURL {
				t.Errorf("DeviceServiceURL = %q, want %q", got.DeviceServiceURL, tc.wantURL)
			}
			if got.Xaddr != tc.wantHost {
				t.Errorf("Xaddr = %q, want %q", got.Xaddr, tc.wantHost)
			}
			// The advertised address and the address the reply came from are two
			// different things, and both are kept.
			if got.From != testProbeSource {
				t.Errorf("From = %v, want %v", got.From, testProbeSource)
			}
		})
	}

	if _, ok := deviceOf(match{UUID: "urn:uuid:x"}, testProbeSource); ok {
		t.Error("a match without any address must be rejected")
	}
}

// TestDedupKeyPrefersEndpointReference checks a device reached over both IP families, or
// answering several probes, is reported once.
func TestDedupKeyPrefersEndpointReference(t *testing.T) {
	withUUID := match{UUID: "urn:uuid:cam-1", XAddrs: []string{"http://10.0.0.1/onvif"}}
	sameDevice := match{UUID: "urn:uuid:cam-1", XAddrs: []string{"http://[fe80::1]/onvif"}}

	first, _ := deviceOf(withUUID, testProbeSource)
	second, _ := deviceOf(sameDevice, testProbeSource)
	if dedupKey(withUUID, first) != dedupKey(sameDevice, second) {
		t.Error("the same endpoint reference must dedupe across addresses")
	}

	anon := match{XAddrs: []string{"http://10.0.0.9/onvif"}}
	client, _ := deviceOf(anon, testProbeSource)
	if got := dedupKey(anon, client); got != "addr:10.0.0.9" {
		t.Errorf("dedupKey() = %q, want the address fallback", got)
	}
}

// TestDiscoverOnUnknownInterface checks a bad interface name is an error rather than an
// empty result, in both dialects.
func TestDiscoverOnUnknownInterface(t *testing.T) {
	for _, fl := range []Flavor{FlavorDraft2005, FlavorOASIS11} {
		t.Run(fl.String(), func(t *testing.T) {
			if _, err := Discover(context.Background(), "definitely-not-an-interface",
				ProbeOptions{Flavor: fl}); err == nil {
				t.Error("an unknown interface must be reported")
			}
		})
	}
}

// TestSendProbeHonoursCancelledContext checks an already-cancelled context returns
// promptly instead of waiting out the collection window.
func TestSendProbeHonoursCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	_, err := SendProbe(ctx, loopback(t).Name, ProbeOptions{Timeout: 30 * time.Second})
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("a cancelled context took %v to take effect", elapsed)
	}
	t.Logf("err = %v", err)
}

// loopback returns an interface usable for socket-level tests.
func loopback(t *testing.T) *net.Interface {
	t.Helper()
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Skipf("cannot list interfaces: %v", err)
	}
	for i := range ifaces {
		if ifaces[i].Flags&net.FlagLoopback != 0 {
			return &ifaces[i]
		}
	}
	t.Skip("no loopback interface available")
	return nil
}
