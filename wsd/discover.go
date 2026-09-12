// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// Portions of this file derive from the ws-discovery project,
// Copyright (C) 2018 Palanjyan Zhorzhik.
//
// SPDX-License-Identifier: MIT

package wsd

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"net"
	"net/netip"
	"net/url"
	"os"
	"time"

	"github.com/ontavu/go-wsd/wsd/transport"
)

// bufSize bounds a single datagram. Larger replies are dropped rather than silently
// truncated into invalid XML; see readReplies.
const bufSize = 65536

// The bounds on what one exchange retains. Replies are held in memory until the window
// closes and are parsed only afterwards, so without a cap an unauthenticated host on the
// link decides how much memory a probe costs and how long it runs: a two-second window
// against a flood of maximum-size datagrams retained 218 MiB, and parsing them took
// minutes, long after the window itself had expired.
//
// Reaching a cap is not an error and is not reported. A flood is indistinguishable from
// a busy link, and the caller gets what was collected, exactly as it does when the
// context ends mid-window.
const (
	// maxReplies bounds the datagrams one exchange retains. A link carrying more
	// answering Target Services than this is not a discovery problem; a flood is.
	maxReplies = 512

	// maxReplyBytes bounds the bytes one exchange retains, since maxReplies alone still
	// admits 512 datagrams of bufSize each.
	maxReplyBytes = 8 << 20

	// maxUnknownReplies bounds the datagrams retained that do not carry the identifier of
	// the Probe we sent. They get their own, much smaller allowance so that traffic we
	// never asked for cannot crowd out the answers we did: a flood of "<junk/>" used to
	// fill all of maxReplies within milliseconds, and a Target Service is required to wait
	// a random delay of up to appMaxDelay before answering, so it always arrived to find
	// the caps full.
	//
	// They are retained at all because the test here is a substring search over undecoded
	// XML, which a conformant reply could in principle escape, and because SendProbe hands
	// the raw datagrams back uncorrelated.
	//
	// This raises the cost of silencing discovery from "send bytes" to "read our Probe and
	// echo its identifier". It does not remove it: a host on the link receives the Probe,
	// since it is multicast, and no retention policy can tell that host's answers from a
	// device's. What the caps must never again decide is when the window ends.
	maxUnknownReplies = 64
)

// The retransmission schedule of SOAP-over-UDP 1.1 section 4, which WS-Discovery
// references for its transport. A multicast message is sent MULTICAST_UDP_REPEAT + 1
// times, separated by a random delay that doubles after each attempt and is capped.
//
// Sending the copies back to back, as an earlier version did, exposes them to the same
// congestion or collision event: it adds network load without adding reliability.
const (
	multicastUDPRepeat = 2
	udpMinDelay        = 50 * time.Millisecond
	udpMaxDelay        = 250 * time.Millisecond
	udpUpperDelay      = 500 * time.Millisecond
)

const (
	// appMaxDelay is the window within which a Target Service picks a random delay
	// before answering a multicast Probe, spreading the replies to avoid a storm.
	appMaxDelay = 1 * time.Second

	// MatchTimeout is the shortest collection window that can be expected to work.
	// A device may wait up to appMaxDelay before it even starts answering, so a
	// shorter window systematically misses conformant equipment.
	MatchTimeout = appMaxDelay + 100*time.Millisecond

	// DefaultProbeTimeout bounds how long replies to a probe are collected.
	DefaultProbeTimeout = 3 * time.Second

	// MaxProbeTimeout is the longest collection window one exchange can ask for. A larger
	// Timeout is lowered to it.
	//
	// The number is arbitrary; the bound is not. A Timeout of 99999h, typed once or read
	// from a configuration file, left readReplies collecting for years. maxReplies and
	// maxReplyBytes cap what is retained, but they do not cap the reads: a datagram that
	// fills the buffer is dropped without advancing either, so a host answering every
	// Probe with maximum-size datagrams keeps the loop copying bufSize at a time for the
	// whole window and retains nothing. The window is the only thing that ends that, and
	// before this bound it did not end.
	//
	// Ninety seconds is thirty times DefaultProbeTimeout, and far beyond what a
	// conformant exchange needs at the defaults: the last of three copies leaves within
	// 750ms of the first, udpMinDelay doubling and capped at udpUpperDelay, and a Target
	// Service answers within appMaxDelay of receiving it.
	//
	// It bounds one exchange, not the call. probe runs one exchange per IP family in
	// sequence, so a dual-stack interface pays the window once per family, and transmit
	// finishes before readReplies opens it, so Attempts adds to it as well. Only ctx
	// bounds the whole call.
	MaxProbeTimeout = 90 * time.Second

	// DefaultProbeAttempts is how many probe messages are multicast, matching the
	// MULTICAST_UDP_REPEAT + 1 transmissions of SOAP-over-UDP.
	DefaultProbeAttempts = multicastUDPRepeat + 1

	// DefaultHopLimit is the multicast TTL. The discovery group is administratively
	// scoped and discovery is a link-local concern, so replies are not expected from
	// beyond the link.
	DefaultHopLimit = 1
)

// reply is one datagram and the address it arrived from. The two are kept together as
// far as the Device, since correlating a ProbeMatches with the Probe that asked for it
// says nothing about who sent it: the identifier is multicast to the whole link.
type reply struct {
	payload string
	from    netip.AddrPort
}

// sourceOf narrows the address a packet connection reports. Anything that is not a UDP
// address, which nothing on this path produces, yields the zero value rather than a
// guess.
func sourceOf(addr net.Addr) netip.AddrPort {
	udp, ok := addr.(*net.UDPAddr)
	if !ok {
		return netip.AddrPort{}
	}
	// An IPv4 reply read from a udp4 socket must not be reported as 4-in-6, which is
	// what ReadFromUDPAddrPort avoids on the announcement path.
	from := udp.AddrPort()
	return netip.AddrPortFrom(from.Addr().Unmap(), from.Port())
}

// ProbeOptions tunes a WS-Discovery probe. The zero value selects the defaults above.
type ProbeOptions struct {
	// Timeout is the collection window of one exchange, bounded at both ends. Values
	// below MatchTimeout are raised to it, since a shorter window would systematically
	// miss conformant devices; values above MaxProbeTimeout are lowered to it. One
	// exchange runs per IP family, in sequence, so this is not a bound on the call.
	Timeout time.Duration

	// Attempts is how many probe messages are multicast.
	Attempts int

	// HopLimit is the multicast TTL, or hop limit over IPv6.
	HopLimit int

	// PortTypes restricts the probe to Target Services implementing all of the listed
	// port types. Matching is conjunctive, so naming several narrows the result rather
	// than widening it: a probe for both ONVIF types selects only the devices that
	// implement both, which is why neither is asked for by default.
	//
	// Leave it empty to probe for every Target Service. The Probe then carries an empty
	// d:Types element rather than none at all, which is what equipment answers; see
	// probeBody. The well-known values are TypeONVIFNetworkVideoTransmitter and its
	// neighbours, and ParseTypeName turns a name or a "{namespace}local" pair into one.
	PortTypes []TypeName

	// Scopes restricts the probe to Target Services in all of the listed scopes.
	Scopes []string

	// IncludeNonOnvif keeps the Target Services that do not advertise an ONVIF port
	// type. Off by default: an untyped probe reaches every WS-Discovery device on the
	// link, printers and Windows hosts included.
	IncludeNonOnvif bool

	// Flavor selects the WS-Discovery dialect. The zero value is FlavorDraft2005, the
	// dialect ONVIF mandates, so the default probe is the one that finds cameras.
	Flavor Flavor
}

func (o ProbeOptions) timeout() time.Duration {
	if o.Timeout <= 0 {
		return DefaultProbeTimeout
	}
	if o.Timeout < MatchTimeout {
		return MatchTimeout
	}
	// Lowered rather than refused. Like maxReplies, this is a bound and reaching it is a
	// result: the caller gets what was collected, and a longer search is another probe.
	if o.Timeout > MaxProbeTimeout {
		return MaxProbeTimeout
	}
	return o.Timeout
}

func (o ProbeOptions) attempts() int {
	if o.Attempts <= 0 {
		return DefaultProbeAttempts
	}
	return o.Attempts
}

func (o ProbeOptions) hopLimit() int {
	if o.HopLimit <= 0 {
		return DefaultHopLimit
	}
	return o.HopLimit
}

// Discover probes one network interface and returns the devices that answered.
//
// The dialect is `opts.Flavor`, whose zero value is the one ONVIF mandates, so the zero
// ProbeOptions discovers cameras.
func Discover(ctx context.Context, interfaceName string, opts ProbeOptions) ([]Device, error) {
	return discoverOnInterface(ctx, interfaceName, opts, probe)
}

// prober collects the raw replies to one Probe, returning the message identifier that
// correlates them. probe is the only implementation; the indirection exists so that the
// aggregation below — correlation, ONVIF filtering and deduplication — can be tested
// without a socket, which is otherwise unreachable from a unit test.
type prober func(ctx context.Context, interfaceName string, opts ProbeOptions) (string, []reply, error)

// discoverOnInterface runs one probe exchange and turns the matches into devices.
func discoverOnInterface(ctx context.Context, interfaceName string, opts ProbeOptions, send prober) ([]Device, error) {
	messageID, replies, err := send(ctx, interfaceName, opts)
	if err != nil {
		return nil, err
	}

	devices := make([]Device, 0, len(replies))
	seen := make(map[string]bool)

	for _, r := range replies {
		// The collection window bounds the reading, not this loop. Parsing is where a
		// flood of crafted datagrams turns into CPU, so an expired context has to stop
		// it too: otherwise Timeout bounds nothing about how long Discover takes, and a
		// deadline of 100ms was observed returning after 17s.
		if ctx.Err() != nil {
			return devices, nil
		}
		for _, found := range parseProbeMatches(r.payload, messageID) {
			if !opts.IncludeNonOnvif && !isOnvifDevice(found.Types) {
				continue
			}
			device, ok := deviceOf(found, r.from)
			if !ok {
				continue
			}
			key := dedupKey(found, device)
			if seen[key] {
				continue
			}
			seen[key] = true
			devices = append(devices, device)
		}
	}
	return devices, nil
}

// deviceOf turns a match into the device parameters of its device service.
//
// The ONVIF Application Programmer's Guide advises sticking to the first working address
// of a camera, so the first advertised address is kept. Its scheme and path are
// preserved: a device is not required to serve on any particular path, and section
// 7.3.2.3 asks for one URI per protocol, https included.
func deviceOf(found match, from netip.AddrPort) (Device, bool) {
	if len(found.XAddrs) == 0 {
		return Device{}, false
	}

	first := found.XAddrs[0]
	host, ok := hostOf(first)
	if !ok {
		return Device{}, false
	}

	return Device{
		Xaddr:            host,
		UUID:             found.UUID,
		DeviceServiceURL: first,
		From:             from,
	}, true
}

// dedupKey identifies one answer across the repeated probes and the two IP families.
//
// The key is the endpoint reference *and* the address it advertises, never the endpoint
// reference alone. An endpoint reference is a claim, not an identity: it is multicast in
// every Hello and in every ProbeMatches on the link, so any host can repeat one. Keyed on
// it alone, the first answer won — and appMaxDelay requires a conformant Target Service to
// wait before answering, so a host that replied at once with a camera's endpoint reference
// and its own XAddrs displaced the camera and the caller dialled the forger.
//
// The address is what the caller acts on, so that is what has to distinguish two answers.
// The observed source cannot: it is spoofable over UDP, and a device answering on both
// families reports two of them for one machine.
//
// The cost is that a device advertising a different address per IP family is now reported
// once per address rather than once. That is the honest result — there really are two
// service addresses, and this function cannot tell that case from two hosts claiming one
// identity. A caller that wants one entry per endpoint reference can group by UUID; a
// caller keyed on UUID alone could not see the conflict at all.
func dedupKey(found match, device Device) string {
	if found.UUID != "" {
		// NUL cannot occur in either half: an endpoint reference reaching here has been
		// through documentRoot, and the address through url.Parse.
		return "uuid:" + found.UUID + "\x00" + device.DeviceServiceURL
	}
	return "addr:" + device.Xaddr
}

// hostOf extracts the "host:port" of a URL.
func hostOf(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", false
	}
	return u.Host, true
}

// SendProbe multicasts a WS-Discovery Probe and returns the raw replies.
//
// The Types, Scopes and TypeNamespaces of opts shape the Probe; opts.Flavor picks the
// dialect. Callers parsing the replies themselves cannot correlate them with the Probe,
// since the message identifier is not returned; prefer Discover, which does correlate.
func SendProbe(ctx context.Context, interfaceName string, opts ProbeOptions) ([]string, error) {
	_, replies, err := probe(ctx, interfaceName, opts)
	if len(replies) == 0 {
		return nil, err
	}
	payloads := make([]string, len(replies))
	for i, r := range replies {
		payloads[i] = r.payload
	}
	return payloads, err
}

// probe builds a Probe, multicasts it over every available IP family and collects the
// replies. It returns the message identifier so that the caller can correlate.
func probe(ctx context.Context, interfaceName string, opts ProbeOptions) (string, []reply, error) {
	iface, err := net.InterfaceByName(interfaceName)
	if err != nil {
		return "", nil, err
	}

	messageID := newMessageID()
	message := buildProbeMessage(messageID, opts.Scopes, opts.PortTypes, opts.Flavor.dialect()).String()

	var payloads []reply
	var lastErr error
	exchanged := 0

	for _, target := range transport.TargetsFor(iface) {
		got, err := exchange(ctx, iface, target, messageID, message, opts)
		payloads = append(payloads, got...)
		if err != nil {
			// One family failing must not lose the other: IPv6 multicast is commonly
			// unavailable even where IPv6 addresses exist.
			lastErr = err
			continue
		}
		exchanged++
	}

	// Finding nothing is a result, not a failure. Only report an error when no family
	// could be probed at all.
	if exchanged == 0 && lastErr != nil {
		return messageID, nil, lastErr
	}
	// A context that ends during collection is how a caller stops early, and readReplies
	// hands back what it had when that happens. Reporting it as an error too made the
	// caller discard that work: discoverOnInterface returns nil on any error, so a
	// deadline expiring mid-window lost every device that had already answered.
	if len(payloads) == 0 {
		return messageID, nil, ctx.Err()
	}
	return messageID, payloads, nil
}

// exchange sends the probe on one IP family and collects the replies.
func exchange(ctx context.Context, iface *net.Interface, target transport.Target, messageID, message string, opts ProbeOptions) ([]reply, error) {
	conn, err := target.Dial(iface, opts.hopLimit())
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if err := transmit(ctx, conn, target.GroupAddr(), []byte(message), opts.attempts()); err != nil {
		return nil, err
	}

	return readReplies(ctx, conn, opts.timeout(), messageID)
}

// transmit applies the retransmission schedule of SOAP-over-UDP 1.1 section 4.
func transmit(ctx context.Context, conn transport.PacketConn, dst net.Addr, data []byte, attempts int) error {
	delay := randomDelay(udpMinDelay, udpMaxDelay)

	for i := 0; i < attempts; i++ {
		if _, err := conn.WriteTo(data, dst); err != nil {
			return err
		}
		if i == attempts-1 {
			break
		}
		if err := sleep(ctx, delay); err != nil {
			return err
		}
		if delay *= 2; delay > udpUpperDelay {
			delay = udpUpperDelay
		}
	}
	return nil
}

// readReplies collects datagrams until the window closes or the context is done.
//
// wantMessageID is the identifier of the Probe these are answers to. It decides which
// retention allowance a datagram draws on, never whether it is read; correlation proper
// happens in parseProbeMatches, over decoded XML.
func readReplies(ctx context.Context, conn transport.PacketConn, window time.Duration, wantMessageID string) ([]reply, error) {
	// The ceiling again, at the one place the window becomes a socket deadline.
	// ProbeOptions.timeout() already applies it, but nothing pins that its only caller
	// keeps calling it: replacing opts.timeout() with opts.Timeout in exchange leaves the
	// whole suite green, and a window that bypassed the normalisation would hold this
	// socket and its group membership for as long as it asked.
	if window > MaxProbeTimeout {
		window = MaxProbeTimeout
	}
	deadline := time.Now().Add(window)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := conn.SetReadDeadline(deadline); err != nil {
		return nil, err
	}

	// A cancellation during collection unblocks the reader by moving the deadline into
	// the past. The watchdog starts only once the collection deadline is in place:
	// starting it earlier let that assignment overwrite the deadline it had just set,
	// so an already-cancelled context waited out the whole window.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.SetReadDeadline(time.Now())
		case <-stop:
		}
	}()

	var result []reply
	matched, unknown, retained := 0, 0, 0
	wanted := []byte(wantMessageID)
	buf := make([]byte, bufSize)
	// The window ends collection; the caps end retention. Ending collection at a cap, as
	// this used to, let any host on the link switch discovery off: the caps count every
	// datagram read, junk included, and a Target Service answering a multicast Probe is
	// required to wait a random delay of up to appMaxDelay first. So 512 copies of
	// "<junk/>" closed a three-second window after 2.99ms, before a single conformant
	// device had spoken, and Discover returned no devices and no error.
	//
	// Reading on and dropping costs a syscall and a copy per datagram for the rest of the
	// window. That is the right price: the flood is arriving whether or not this loop
	// reads it, the sender pays the same cost to produce it, and the alternative is that
	// the cheapest possible attack defeats the function of the library.
	for {
		n, src, err := conn.ReadFrom(buf)
		if err != nil {
			// A deadline is how collection ends, whether it expired naturally or was
			// brought forward by the context.
			if errors.Is(err, os.ErrDeadlineExceeded) || ctx.Err() != nil {
				break
			}
			return result, err
		}
		if n == len(buf) {
			// The datagram filled the buffer, so it may have been truncated into
			// invalid XML. Dropping it is better than reporting a mangled device.
			continue
		}
		if retained >= maxReplyBytes {
			// Full. Keep draining to the deadline so that the window still belongs to
			// the clock rather than to whoever fills these first.
			continue
		}
		// An answer to our own Probe draws on maxReplies; anything else draws on the
		// much smaller maxUnknownReplies, so that a flood cannot take the room a device
		// needs when it finally answers.
		if len(wanted) != 0 && bytes.Contains(buf[:n], wanted) {
			if matched >= maxReplies {
				continue
			}
			matched++
		} else {
			if unknown >= maxUnknownReplies {
				continue
			}
			unknown++
		}
		result = append(result, reply{payload: string(buf[:n]), from: sourceOf(src)})
		retained += n
	}
	return result, nil
}

// sleep waits for d, or returns early if the context is done.
func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// randomDelay draws a delay in [lo, hi]. The spread is what keeps a fleet of clients
// from retransmitting in lockstep.
func randomDelay(lo, hi time.Duration) time.Duration {
	span := int64(hi - lo)
	if span <= 0 {
		return lo
	}
	n, err := rand.Int(rand.Reader, big.NewInt(span+1))
	if err != nil {
		return lo
	}
	return lo + time.Duration(n.Int64())
}
