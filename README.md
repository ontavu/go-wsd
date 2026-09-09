# Web Service Discovery

Golang Web Service Discovery utilities.

The standard is described in https://www.oasis-open.org/standard/ws-discovery/

The current package offers two flavors, the draft of 2005 and the stabilized `v1.1` of 2009.
They are not interoperable: the `wsa:To` and the action URI differ, so a device  listening for
one ignores the other outright.

Initially written to serve [use-go/onvif](https://github.com/use-go/onvif), a golang [Onvif](https://www.onvif.org/) client library.


## Two halves

Discovery is not just polling. A **Probe** is multicast and collects whatever answers
inside a time window; **Hello** and **Bye** are what a device sends unprompted when it
joins or leaves the network. A Probe alone can never observe a departure, so both are
implemented.

```go
import "github.com/jfsmig/go-wsd/wsd"

// Poll the link. The zero ProbeOptions speaks the dialect ONVIF mandates.
devices, err := wsd.Discover(ctx, "eth0", wsd.ProbeOptions{})
for _, d := range devices {
    fmt.Println(d.UUID, d.DeviceServiceURL)
}
```

```go
// Watch the link until ctx is done; the channel is closed when it is.
announcements, err := wsd.Listen(ctx, "eth0")
for a := range announcements {
    fmt.Println(a.Kind, a.Device.From, a.Device.UUID) // Hello or Bye
}
```

`Discover` returns a `wsd.Device`: the `Xaddr` as `host:port`, the `UUID` endpoint
reference, and the `DeviceServiceURL` exactly as advertised, scheme and path intact —
ONVIF fixes neither, and §7.3.2.3 asks for one URI per protocol, `https` included.


## Flavors

| `ProbeOptions.Flavor` | Version | Namespace | Answered by |
| --- | --- | --- | --- |
| `FlavorDraft2005` (zero value) | April 2005 XMLSOAP draft | `schemas.xmlsoap.org/ws/2005/04/discovery` | ONVIF cameras |
| `FlavorOASIS11` | OASIS WS-Discovery 1.1, 2009 | `docs.oasis-open.org/ws-dd/ns/discovery/2009/01` | printers, Windows hosts (WSD) |

ONVIF Core references the 2005 draft normatively, which is why it is the default: a
camera ignores a `v1.1` Probe. `Listen` needs no flavor — replies are matched on local
element names, so one parser serves both.


## Probe options

The zero value works. Everything below is a refinement.

| Field | Default | Purpose |
| --- | --- | --- |
| `Timeout` | 3s | Collection window. Raised to `MatchTimeout` (1.1s) if lower: a device may wait up to 1s before it even starts answering. |
| `Attempts` | 3 | Multicast transmissions, per `MULTICAST_UDP_REPEAT + 1` of SOAP-over-UDP §4, spaced by a randomised backoff. |
| `HopLimit` | 1 | Multicast TTL. Discovery is a link-local concern. |
| `PortTypes`, `Scopes` | empty | Narrow the Probe. Matching is **conjunctive**, so listing several selects fewer devices, not more. |
| `IncludeNonOnvif` | `false` | Keep Target Services advertising no ONVIF port type. |
| `Flavor` | `FlavorDraft2005` | See above. |


## Port types

`PortTypes` takes `TypeName` values, which carry the namespace with the local name so a
caller never has to keep a prefix map in step with a QName list. The well-known ones are
exported — `TypeONVIFNetworkVideoTransmitter`, `TypeONVIFDevice`, `TypeWindowsPrinter` and
their neighbours — and `ParseTypeName` accepts either a short name or the explicit
`{namespace}LocalName` form, which is how a discovered type is printed:

```go
devices, err := wsd.Discover(ctx, "eth0", wsd.ProbeOptions{
    PortTypes: []wsd.TypeName{wsd.TypeONVIFNetworkVideoTransmitter},
})
```

`wsdc` exposes the same thing as `-types`, comma-separated:

```
wsdc discover eth0                     # every Target Service on the link
wsdc -types onvif-nvt discover eth0    # only ONVIF video transmitters
wsdc -types '{urn:x}Thing' discover eth0
wsdc -h                                # lists the well-known names
```

**An untyped Probe still carries an empty `d:Types` element.** WS-Discovery says an absent
`d:Types` matches every Target Service, and this package used to omit it on that reading.
Equipment disagrees. Measured on one link with three ONVIF cameras and one non-ONVIF
device:

| Probe body | devices that answered |
| --- | --- |
| `<d:Probe/>` — no `d:Types` | 1 of 4 |
| `<d:Probe><d:Types/></d:Probe>` | **4 of 4** |
| `<d:Probe><d:Types> </d:Types></d:Probe>` | 3 of 4 |

The cameras answer whenever the element is present, whatever it contains. The element has
to be genuinely empty: the blank variant is parsed by the non-ONVIF device as a type list
matching nothing.


## Untrusted input

Every datagram arrives unauthenticated over UDP multicast from any host on the link.
Replies that do not parse, that do not correlate with the Probe that was sent, or that
carry no usable address are discarded rather than reported — ONVIF Core §7.3.6 asks that
malformed multicast be dropped silently rather than answered, to avoid packet storms.

An advertised address survives only if it is `http` or `https` and carries no credentials,
because the caller is the one who will dial it. It still names wherever its sender chose,
so treat `DeviceServiceURL` as an address you were given, not one you trust.

`Device.From` carries the sender, on both halves alike, because the endpoint reference
*inside* a datagram is only a claim: any host on the link can assert any identity. It is
the one field that was observed rather than told.

Volume is bounded as well as content: a probe retains a limited number of datagrams and a
limited number of bytes. The timeout bounds the window in which replies are collected;
they are parsed once it closes, and that stops when the context is done — so give `ctx` a
deadline if you want a bound on the whole call.


## Code organization

* `wsd` is a golang library doing the very basic job of web-service discovery: building
  and multicasting Probes, collecting and correlating the replies, and reporting the
  Hello and Bye announcements.

* `wsd/transport` is the multicast plumbing, IPv4 and IPv6, behind one connection
  interface.

* `gosoap` builds the SOAP envelopes. Vendored from
  [jfsmig/onvif](https://github.com/jfsmig/onvif).

* `bin/wsdc` a CLI tool to wrap `wsd`:

  ```
  wsdc discover eth0    # probe the link, print what answers
  wsdc listen eth0      # print Hello and Bye until interrupted
  ```

  Flags: `-timeout` (collection window), `-oasis11` (use the `v1.1` flavor), `-all`
  (keep devices advertising no ONVIF port type), `-types` (comma-separated port types to
  probe for, see above).


## License

MIT, see [LICENSE](LICENSE). `gosoap/` derives from `jfsmig/onvif`, also MIT;
the file headers carry both copyright notices.
