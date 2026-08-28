// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// SPDX-License-Identifier: MIT

package wsd

import (
	"maps"
	"net/url"
	"strings"

	"github.com/beevik/etree"
	"github.com/gofrs/uuid"
)

// match is one ProbeMatch: a single Target Service that answered a Probe.
type match struct {
	// UUID is the endpoint reference address, the device's stable identifier across
	// its network interfaces.
	UUID string
	// Types are the port types the device advertised, resolved to "{namespace}local".
	// Prefixes are meaningless outside the document that declared them, so they are
	// resolved here rather than carried around.
	Types []string
	// XAddrs are the service addresses advertised, in the order given.
	XAddrs []string
}

// newMessageID returns a fresh message identifier.
//
// The form is urn:uuid, which RFC 4122 registers; "uuid:" is not a URI scheme. ONVIF
// Core section 7.1 makes the point explicitly, overriding the recommendation of
// WS-Discovery section 2.6 in favour of URN:UUID.
func newMessageID() string {
	return "urn:uuid:" + uuid.Must(uuid.NewV4()).String()
}

// parseProbeMatches extracts the Target Services listed in a datagram received while
// collecting replies to the Probe identified by wantMessageID.
//
// It returns nothing for anything it cannot use: a datagram that is not XML, that is
// not a ProbeMatches, or whose wsa:RelatesTo does not correlate with our Probe. This is
// deliberate and required. The payload arrives unauthenticated over UDP from any host
// on the link, and ONVIF Core section 7.3.6 asks that a malformed multicast packet be
// silently discarded rather than answered, to avoid packet storms.
func parseProbeMatches(payload, wantMessageID string) []match {
	root := documentRoot(payload)
	if root == nil {
		return nil
	}

	// Correlate. WS-Discovery requires the [relationship] property of ProbeMatches to
	// carry the [message id] of the Probe; without this check a stale reply to an
	// earlier Probe, or a forged datagram, would be accepted.
	if !relatesTo(root, wantMessageID) {
		return nil
	}

	// One scope cache for the whole datagram: every ProbeMatch shares the same ancestors,
	// so their attributes are scanned once between them rather than once per match.
	var scopes nsScopes

	var out []match
	for _, element := range root.FindElements("./Body/ProbeMatches/ProbeMatch") {
		// Pair the endpoint reference with the addresses of the *same* ProbeMatch. A
		// ProbeMatches may legitimately carry several, notably from a Discovery Proxy,
		// and attributing the first UUID to all of them mislabels every device but one.
		found := match{
			UUID:   childText(element, "./EndpointReference/Address"),
			Types:  typesOf(&scopes, element),
			XAddrs: parseXAddrs(elementsText(element, "./XAddrs")),
		}
		// Without an address the entry is unusable: recovering one would need the
		// Resolve exchange, which ONVIF section 7.3.4 deems unnecessary precisely
		// because ProbeMatch already carries the addresses.
		if len(found.XAddrs) == 0 {
			continue
		}
		out = append(out, found)
	}
	return out
}

// documentRoot parses a datagram and returns its root element, or nil.
//
// etree never reports a parse error and simply leaves the root nil when the payload is
// not XML at all, so testing the root is the only reliable check. Every caller must do
// it: dereferencing Root() on a non-XML datagram is a panic that any host on the link
// could trigger.
func documentRoot(payload string) *etree.Element {
	doc := etree.NewDocument()
	if err := doc.ReadFromString(payload); err != nil {
		return nil
	}
	return doc.Root()
}

// relatesTo reports whether a reply correlates with the message we sent.
func relatesTo(root *etree.Element, wantMessageID string) bool {
	if wantMessageID == "" {
		return true
	}
	for _, element := range root.FindElements("./Header/RelatesTo") {
		if strings.TrimSpace(element.Text()) == wantMessageID {
			return true
		}
	}
	return false
}

// childText returns the trimmed text of the first element matching path, or "".
func childText(element *etree.Element, path string) string {
	if found := element.FindElement(path); found != nil {
		return strings.TrimSpace(found.Text())
	}
	return ""
}

// elementsText joins the text of every element matching path. XAddrs may appear more
// than once, and each occurrence is itself a space-delimited list.
func elementsText(element *etree.Element, path string) string {
	var parts []string
	for _, found := range element.FindElements(path) {
		parts = append(parts, found.Text())
	}
	return strings.Join(parts, " ")
}

// typesOf resolves the d:Types of a ProbeMatch, Hello or Bye element.
//
// The QNames live in the content of d:Types, so their prefixes resolve against the
// declarations in scope at that element, its own included. Resolving against the
// enclosing element instead, as this used to, missed a declaration carried on d:Types
// itself, which is precisely where a sender puts it: probeBody declares the prefixes of
// the Types it emits on that element, so a device answering the way we ask resolved to
// nothing and was filtered out as non-ONVIF.
func typesOf(scopes *nsScopes, element *etree.Element) []string {
	types := element.FindElement("./Types")
	if types == nil {
		return nil
	}
	return resolveTypes(scopes.of(types), strings.TrimSpace(types.Text()))
}

// resolveTypes turns the space-delimited QName list of a d:Types element into
// "{namespace}local" names, resolving each prefix against the declarations in scope.
// An unresolvable prefix yields an empty namespace, which isOnvifDevice rejects.
func resolveTypes(scope map[string]string, raw string) []string {
	var out []string
	for _, qname := range strings.Fields(raw) {
		prefix, local := splitQName(qname)
		out = append(out, "{"+scope[prefix]+"}"+local)
	}
	return out
}

// parseXAddrs extracts the service addresses listed in an XAddrs element, which holds a
// space-separated list of URLs.
//
// The full URL is kept, scheme and path included: ONVIF devices are not required to
// serve the device service on any particular path, and section 7.3.2.3 asks for one URI
// per protocol, https included. Reducing the value to a host loses both.
//
// The input arrives unauthenticated over UDP multicast, so anything that does not parse
// into an absolute URL with a host is dropped rather than trusted, and so is anything a
// caller has no business dialling.
func parseXAddrs(raw string) []string {
	out := make([]string, 0, 1)
	for _, field := range strings.Fields(raw) {
		u, err := url.Parse(field)
		if err != nil || u.Host == "" {
			continue
		}
		// The scheme decides what the caller does with the address, and the address was
		// chosen by whoever sent the datagram. Section 7.3.2.3 asks for one URI per
		// protocol and names http and https; a gopher:// or file://host/ address in an
		// XAddrs list is not a device service, it is a way to steer a caller somewhere it
		// never meant to go. url.Parse has already lowercased the scheme.
		if u.Scheme != "http" && u.Scheme != "https" {
			continue
		}
		// Credentials in an advertised address are never legitimate: a device cannot know
		// what its client should authenticate as. They are how http://trusted.example@evil/
		// is made to read as trusted, and they follow the address into whatever the caller
		// logs or stores.
		if u.User != nil {
			continue
		}
		out = append(out, u.String())
	}
	return out
}

// onvifTypeNames are the local names an ONVIF device publishes in d:Types.
//
// ONVIF Core v19.12 section 7.3.2.1 mandates the device management port type, tds:Device.
// NetworkVideoTransmitter is the ONVIF 1.0 type, absent from that specification but
// still what many cameras advertise, sometimes exclusively. A device is accepted on
// either, because Types matching is conjunctive: probing for both at once would select
// only the devices implementing both.
var onvifTypeNames = map[string]bool{
	"Device":                  true,
	"NetworkVideoTransmitter": true,
}

// onvifNamespaces are the namespaces those port types belong to.
//
// They are matched exactly. A substring test for "onvif.org", which is what this used to
// do, also accepted http://evil.example/onvif.org/ and http://notonvif.org.evil/. Exact
// matching is not authentication either — a namespace is a string the sender picks, and
// any host on the link can claim this one — but it is the difference between a filter
// that means what it says and one that matches by accident.
var onvifNamespaces = map[string]bool{
	onvifDeviceNamespace:  true,
	onvifNetworkNamespace: true,
}

// isOnvifDevice reports whether the resolved types of a match describe an ONVIF device.
//
// The namespace is checked, not just the local name, so that a non-ONVIF Target Service
// advertising an unrelated type whose local name happens to be "Device" is not mistaken
// for a camera. This narrows what a probe reports; it decides nothing about trust, since
// the types are a claim the sender makes about itself.
//
// A match advertising no type at all is accepted: it answered a Probe of ours, and
// rejecting it would lose devices that omit the optional element.
func isOnvifDevice(types []string) bool {
	if len(types) == 0 {
		return true
	}
	for _, resolved := range types {
		namespace, local := splitResolved(resolved)
		if onvifTypeNames[local] && onvifNamespaces[namespace] {
			return true
		}
	}
	return false
}

// splitResolved splits a "{namespace}local" name.
func splitResolved(resolved string) (namespace, local string) {
	if !strings.HasPrefix(resolved, "{") {
		return "", resolved
	}
	if i := strings.Index(resolved, "}"); i >= 0 {
		return resolved[1:i], resolved[i+1:]
	}
	return "", resolved
}

// splitQName splits "prefix:local" into its parts. An unprefixed name has an empty
// prefix, which resolves against the default namespace declaration.
func splitQName(qname string) (prefix, local string) {
	if i := strings.LastIndex(qname, ":"); i >= 0 {
		return qname[:i], qname[i+1:]
	}
	return "", qname
}

// nsScopes memoises the namespace declarations in scope at each element of one document.
// Its zero value is ready to use, and it must not outlive the document it was built for,
// since it holds the elements it has seen.
//
// The previous implementation walked up from the element and rescanned every ancestor's
// attributes once per QName, which is quadratic in the size of a single datagram. A reply
// carrying decoy attributes on its root cost 45ms of CPU to parse, some 760 times an
// ordinary one, on a path where the caller controls neither how many datagrams arrive nor
// what is in them.
type nsScopes struct {
	byElement map[*etree.Element]map[string]string
}

// of returns the prefixes in scope at element, mapped to their namespace. The default
// declaration is held under the empty prefix.
//
// An element that declares nothing shares its parent's map rather than copying it, so a
// document costs one pass over its attributes however many QNames refer to them. The
// recursion is bounded by the parse depth limit etree applies while reading.
func (s *nsScopes) of(element *etree.Element) map[string]string {
	if scope, ok := s.byElement[element]; ok {
		return scope
	}

	var scope map[string]string
	if parent := element.Parent(); parent != nil {
		scope = s.of(parent)
	}

	cloned := false
	for _, attr := range element.Attr {
		prefix, ok := declaredPrefix(attr)
		// An empty declaration is passed over rather than recorded, which is what the
		// ancestor walk did: it could not tell an absent attribute from an xmlns=""
		// undeclaration, and kept looking further up in both cases.
		if !ok || attr.Value == "" {
			continue
		}
		if !cloned {
			if scope = maps.Clone(scope); scope == nil {
				scope = make(map[string]string)
			}
			cloned = true
		}
		scope[prefix] = attr.Value
	}

	if s.byElement == nil {
		s.byElement = make(map[*etree.Element]map[string]string)
	}
	s.byElement[element] = scope
	return scope
}

// declaredPrefix reports which prefix an attribute declares, if it declares one. etree
// splits xmlns:d="..." into the space "xmlns" and the key "d", and the default
// declaration xmlns="..." into an empty space and the key "xmlns".
func declaredPrefix(attr etree.Attr) (string, bool) {
	switch {
	case attr.Space == "xmlns":
		return attr.Key, true
	case attr.Key == "xmlns":
		return "", true
	default:
		return "", false
	}
}
