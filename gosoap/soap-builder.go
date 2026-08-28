// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// Portions of this file derive from github.com/jfsmig/onvif,
// originally distributed under the MIT License,
// Copyright (c) 2018 Yakovlev Dmitry, Zhorzh Palanjyan, Crazybber.
//
// SPDX-License-Identifier: MIT

package gosoap

import (
	"encoding/xml"
	"sort"
	"time"

	"github.com/beevik/etree"
)

// SoapMessage is a SOAP envelope under construction.
//
// It retains the parsed document. The previous representation was a string, so every
// mutator re-parsed the whole envelope with etree and re-serialised it: adding the
// fifteen root namespaces alone cost fifteen parse-and-serialise cycles, and a single
// request paid about nineteen in total. Now the document is built once and serialised
// only when the message is sent.
//
// A message is a builder and is handled by pointer throughout. It used to be passed by
// value while holding this pointer, so a copy was an alias: mutating either changed both,
// and two goroutines each holding what looked like their own message raced on one
// document. Sharing is now visible in the types.
//
// A message must not be used from several goroutines at once. To hand one to another
// goroutine, give it a Clone: a snapshot costs a fraction of what building the envelope
// did, and a lock on the shared path would cost every caller for a case most never meet.
type SoapMessage struct {
	doc *etree.Document
}

// NewEmptySOAP returns an envelope holding an empty Header and Body.
func NewEmptySOAP() *SoapMessage {
	return &SoapMessage{doc: buildSoapRoot()}
}

// NewSoapMessage parses an existing envelope. It reports an error only if the payload
// cannot be read at all; etree itself is permissive.
func NewSoapMessage(payload string) (*SoapMessage, error) {
	doc := etree.NewDocument()
	if err := doc.ReadFromString(payload); err != nil {
		return nil, err
	}
	return &SoapMessage{doc: doc}, nil
}

// Clone returns an independent copy of the message. The two share nothing, so one can be
// mutated, serialised or handed to another goroutine without touching the other.
//
// Note that the elements a caller passed to the Add methods were adopted, not copied, so
// a caller still holding one of those pointers can reach into the message it added them
// to. It cannot reach into a Clone.
func (msg *SoapMessage) Clone() *SoapMessage {
	if msg == nil || msg.doc == nil {
		return &SoapMessage{}
	}
	return &SoapMessage{doc: msg.doc.Copy()}
}

// document returns the retained document, allocating an envelope for the zero value so
// that a SoapMessage is always usable.
func (msg *SoapMessage) document() *etree.Document {
	if msg.doc == nil {
		msg.doc = buildSoapRoot()
	}
	return msg.doc
}

// String serialises the envelope.
//
// The error etree reports here can only come from the writer, and the writer is a
// bytes.Buffer, which does not fail. It is dropped rather than logged: a library that
// writes to the standard logger reports to whoever it was not built for.
func (msg *SoapMessage) String() string {
	if msg == nil || msg.doc == nil {
		return ""
	}
	res, _ := msg.doc.WriteToString()
	return res
}

// StringIndent serialises the envelope with tab indentation.
func (msg *SoapMessage) StringIndent() string {
	if msg == nil || msg.doc == nil {
		return ""
	}
	// Copy so that indenting a message does not alter what will be sent.
	doc := msg.doc.Copy()
	doc.IndentTabs()
	res, _ := doc.WriteToString()
	return res
}

// Body returns the first element of the envelope Body, serialised.
// It returns the empty string when the envelope carries no body content.
func (msg *SoapMessage) Body() string {
	if msg == nil || msg.doc == nil || msg.doc.Root() == nil {
		return ""
	}
	body := msg.doc.Root().SelectElement("Body")
	if body == nil {
		return ""
	}
	children := body.ChildElements()
	if len(children) == 0 {
		return ""
	}

	doc := etree.NewDocument()
	doc.SetRoot(children[0].Copy())
	doc.IndentTabs()
	res, _ := doc.WriteToString()
	return res
}

// section returns the named direct child of the envelope, creating nothing: a
// malformed envelope yields nil and the mutators become no-ops rather than panicking,
// which is what the previous string-based code did by accident.
func (msg *SoapMessage) section(name string) *etree.Element {
	root := msg.document().Root()
	if root == nil {
		return nil
	}
	return root.SelectElement(name)
}

// AddStringBodyContent parses a fragment and appends it to the Body.
//
// It reports a payload that will not parse, which its header twin already did. Swallowing
// it left the caller with an envelope silently missing its body, and a device answering a
// request that was never made is harder to diagnose than a returned error.
func (msg *SoapMessage) AddStringBodyContent(data string) error {
	element, err := parseFragment(data)
	if err != nil {
		return err
	}
	msg.AddBodyContent(element)
	return nil
}

// AddBodyContent appends an element to the Body.
func (msg *SoapMessage) AddBodyContent(element *etree.Element) {
	if body := msg.section("Body"); body != nil && element != nil {
		body.AddChild(element)
	}
}

// AddBodyContents appends several elements to the Body.
func (msg *SoapMessage) AddBodyContents(elements []*etree.Element) {
	body := msg.section("Body")
	if body == nil {
		return
	}
	for _, element := range elements {
		if element != nil {
			body.AddChild(element)
		}
	}
}

// AddStringHeaderContent parses a fragment and appends it to the Header.
func (msg *SoapMessage) AddStringHeaderContent(data string) error {
	element, err := parseFragment(data)
	if err != nil {
		return err
	}
	msg.AddHeaderContent(element)
	return nil
}

// AddHeaderContent appends an element to the Header.
func (msg *SoapMessage) AddHeaderContent(element *etree.Element) {
	if header := msg.section("Header"); header != nil && element != nil {
		header.AddChild(element)
	}
}

// AddHeaderContents appends several elements to the Header.
func (msg *SoapMessage) AddHeaderContents(elements []*etree.Element) {
	header := msg.section("Header")
	if header == nil {
		return
	}
	for _, element := range elements {
		if element != nil {
			header.AddChild(element)
		}
	}
}

// AddRootNamespace declares a namespace prefix on the envelope element.
func (msg *SoapMessage) AddRootNamespace(key, value string) {
	if root := msg.document().Root(); root != nil {
		root.CreateAttr("xmlns:"+key, value)
	}
}

// AddRootNamespaces declares several namespace prefixes on the envelope element.
// The prefixes are emitted in sorted order so that the same request always produces
// byte-identical output, which Go's randomised map iteration otherwise prevents.
func (msg *SoapMessage) AddRootNamespaces(namespaces map[string]string) {
	root := msg.document().Root()
	if root == nil {
		return
	}
	keys := make([]string, 0, len(namespaces))
	for key := range namespaces {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		root.CreateAttr("xmlns:"+key, namespaces[key])
	}
}

// parseFragment reads a standalone XML fragment and returns its root element.
func parseFragment(data string) (*etree.Element, error) {
	doc := etree.NewDocument()
	if err := doc.ReadFromString(data); err != nil {
		return nil, err
	}
	return doc.Root(), nil
}

func buildSoapRoot() *etree.Document {
	doc := etree.NewDocument()

	doc.CreateProcInst("xml", `version="1.0" encoding="UTF-8"`)

	env := doc.CreateElement("soap-env:Envelope")
	env.CreateElement("soap-env:Header")
	env.CreateElement("soap-env:Body")

	env.CreateAttr("xmlns:soap-env", "http://www.w3.org/2003/05/soap-envelope")
	env.CreateAttr("xmlns:soap-enc", "http://www.w3.org/2003/05/soap-encoding")

	return doc
}

// AddWSSecurity Header for soapMessage, stamped with the current local time.
func (msg *SoapMessage) AddWSSecurity(username, password string) {
	msg.AddWSSecurityAt(username, password, time.Now().UTC())
}

// AddWSSecurityAt adds the WS-Security header stamped at the given time.
// OnVif devices reject a UsernameToken whose Created timestamp sits more than a few
// seconds from their own clock, and camera clocks drift, so callers that know the
// device clock offset must stamp the header in device time rather than local time.
func (msg *SoapMessage) AddWSSecurityAt(username, password string, created time.Time) {
	auth := NewSecurityAt(username, password, created)

	// Security is a fixed struct of strings, so marshalling it cannot fail, and what it
	// produces is well-formed, so parsing it back cannot fail either. Neither error is
	// reachable; both are dropped rather than logged or promoted into this signature,
	// which would make every caller handle something that cannot happen.
	soapReq, err := xml.MarshalIndent(auth, "", "  ")
	if err != nil {
		return
	}
	_ = msg.AddStringHeaderContent(string(soapReq))
}

// AddAction adds the WS-Addressing action header identifying the operation.
//
// The previous implementation of this method had an empty body, so no wsa:Action was
// ever emitted even though every request went through it. The header is deliberately
// not marked mustUnderstand: devices that ignore WS-Addressing must keep working.
func (msg *SoapMessage) AddAction(action string) {
	if action == "" {
		return
	}
	element := etree.NewElement("wsa:Action")
	element.SetText(action)
	msg.AddHeaderContent(element)
}
