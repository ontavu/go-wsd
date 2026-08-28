// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// SPDX-License-Identifier: MIT

package wsd

import (
	"context"
	"net"
	"sync"

	"github.com/beevik/etree"
	"github.com/ontavu/go-wsd/wsd/transport"
)

// AnnouncementKind distinguishes the two announcements a Target Service multicasts.
type AnnouncementKind int

const (
	// KindHello is sent when a device joins the network or changes its metadata.
	KindHello AnnouncementKind = iota
	// KindBye is sent when a device prepares to leave the network.
	KindBye
)

func (k AnnouncementKind) String() string {
	if k == KindBye {
		return "Bye"
	}
	return "Hello"
}

// Announcement is a Hello or Bye multicast announcement.
//
// Bye carries only the endpoint reference on most devices, so Device.Xaddr and
// Device.DeviceServiceURL may be empty; Device.UUID is what identifies the departing
// device. [Device.From] carries the sender, which is the one thing here that was observed
// rather than claimed. Types is reported unfiltered so that the caller can decide what to
// keep.
type Announcement struct {
	Kind   AnnouncementKind
	Device Device
	Types  []string
}

// IsOnvif reports whether the announcement comes from an ONVIF device.
func (a Announcement) IsOnvif() bool { return isOnvifDevice(a.Types) }

// Listen reports the Hello and Bye announcements multicast on an interface, until ctx is
// done. The channel is closed when it is.
//
// This is the half of the WS-Discovery Client role that a Probe cannot provide: ONVIF
// section 7.2 has a discoverable device multicast Hello when it joins the network, and
// section 7.3.5 a Bye when it leaves. Probing alone can only poll.
//
// Unlike the ProbeMatches replies, which arrive unicast on an ephemeral port, the
// announcements are multicast to the discovery group: receiving them requires binding to
// the discovery port itself and joining the group.
//
// Both dialects are accepted at once. Only a sender has to pick one, and here we only
// listen.
//
// Cancelling ctx is the only way to stop a listener, and what it releases is not just
// goroutines: each call holds one socket and one group membership per IP family until the
// context ends. Abandoning the channel without cancelling keeps all of it, and twenty such
// calls cost a hundred goroutines and forty descriptors. Draining the channel to its close
// is not a substitute, since the channel only closes once the readers have stopped.
func Listen(ctx context.Context, interfaceName string) (<-chan Announcement, error) {
	iface, err := net.InterfaceByName(interfaceName)
	if err != nil {
		return nil, err
	}

	conns := listenerConns(iface)
	if len(conns) == 0 {
		return nil, ErrNoListener
	}

	out := make(chan Announcement)
	var wg sync.WaitGroup

	for _, conn := range conns {
		wg.Add(1)
		go func(conn *net.UDPConn) {
			defer wg.Done()
			defer conn.Close()
			// Closing the connection is what unblocks ReadFromUDP on cancellation. The
			// stop channel retires the watchdog when the reader returns for any other
			// reason: a read error on a live context used to leave it blocked forever,
			// and callers may well never cancel, since out is closed either way.
			stop := make(chan struct{})
			defer close(stop)
			go func() {
				select {
				case <-ctx.Done():
					_ = conn.Close()
				case <-stop:
				}
			}()
			readAnnouncements(ctx, conn, out)
		}(conn)
	}

	go func() {
		wg.Wait()
		close(out)
	}()

	return out, nil
}

// ErrNoListener is returned by Listen when no IP family could be joined on the
// interface. It is exported so that callers can distinguish it from the interface
// lookup failing, which is what net.InterfaceByName reports.
var ErrNoListener = errListen("no multicast listener could be opened on this interface")

type errListen string

func (e errListen) Error() string { return string(e) }

// listenerConns joins the discovery group on every family the interface supports.
// A family that cannot be joined is skipped: IPv6 multicast is commonly unavailable
// even where IPv6 addresses exist, and that must not cost the IPv4 announcements.
func listenerConns(iface *net.Interface) []*net.UDPConn {
	var out []*net.UDPConn
	for _, target := range transport.TargetsFor(iface) {
		conn, err := target.ListenMulticast(iface)
		if err != nil {
			continue
		}
		out = append(out, conn)
	}
	return out
}

// readAnnouncements forwards the announcements read from one connection.
func readAnnouncements(ctx context.Context, conn *net.UDPConn, out chan<- Announcement) {
	buf := make([]byte, bufSize)
	for {
		n, from, err := conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			// A closed connection is how cancellation reaches us.
			return
		}
		if n == len(buf) {
			// Possibly truncated into invalid XML; drop rather than misreport.
			continue
		}

		announcement, ok := parseAnnouncement(string(buf[:n]))
		if !ok {
			continue
		}
		announcement.Device.From = from
		select {
		case out <- announcement:
		case <-ctx.Done():
			return
		}
	}
}

// parseAnnouncement extracts a Hello or Bye from a datagram.
//
// As everywhere on this path the payload is unauthenticated input from the link, so
// anything unusable is discarded rather than trusted, per ONVIF section 7.3.6.
func parseAnnouncement(payload string) (Announcement, bool) {
	root := documentRoot(payload)
	if root == nil {
		return Announcement{}, false
	}

	for _, candidate := range []struct {
		path string
		kind AnnouncementKind
	}{
		{"./Body/Hello", KindHello},
		{"./Body/Bye", KindBye},
	} {
		element := root.FindElement(candidate.path)
		if element == nil {
			continue
		}
		announcement := announcementOf(element, candidate.kind)
		// An announcement that identifies nothing is not actionable: a truncated
		// datagram parses into an empty Hello, and reporting it would invent a device.
		if announcement.Device.UUID == "" && announcement.Device.DeviceServiceURL == "" {
			return Announcement{}, false
		}
		return announcement, true
	}
	return Announcement{}, false
}

// announcementOf builds an Announcement from a Hello or Bye element.
func announcementOf(element *etree.Element, kind AnnouncementKind) Announcement {
	var scopes nsScopes
	announcement := Announcement{
		Kind:  kind,
		Types: typesOf(&scopes, element),
		Device: Device{
			UUID: childText(element, "./EndpointReference/Address"),
		},
	}

	// Bye usually carries no address: the endpoint reference identifies the device.
	if addrs := parseXAddrs(elementsText(element, "./XAddrs")); len(addrs) > 0 {
		announcement.Device.DeviceServiceURL = addrs[0]
		if host, ok := hostOf(addrs[0]); ok {
			announcement.Device.Xaddr = host
		}
	}
	return announcement
}
