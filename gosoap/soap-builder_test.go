// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// Portions of this file derive from github.com/jfsmig/onvif,
// originally distributed under the MIT License,
// Copyright (c) 2018 Yakovlev Dmitry, Zhorzh Palanjyan, Crazybber.
//
// SPDX-License-Identifier: MIT

package gosoap

import (
	"bytes"
	"log"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/beevik/etree"
)

// canonicalize re-serialises an XML document with the attributes of every element
// sorted. Namespace declarations are added by iterating a Go map, so their order in
// the output is randomised between runs; canonicalising makes envelope comparison
// stable without weakening it.
func canonicalize(t *testing.T, doc string) string {
	t.Helper()

	d := etree.NewDocument()
	if err := d.ReadFromString(doc); err != nil {
		t.Fatalf("cannot parse the produced envelope: %v\n%s", err, doc)
	}
	sortAttrs(d.Root())
	d.Indent(2)

	out, err := d.WriteToString()
	if err != nil {
		t.Fatalf("cannot serialise: %v", err)
	}
	return out
}

func sortAttrs(e *etree.Element) {
	if e == nil {
		return
	}
	sort.SliceStable(e.Attr, func(i, j int) bool {
		return e.Attr[i].FullKey() < e.Attr[j].FullKey()
	})
	for _, child := range e.ChildElements() {
		sortAttrs(child)
	}
}

// buildRepresentative assembles the envelope the way networking.Client.CallMethod does:
// an empty SOAP root, the marshalled request in the body, then the namespace block.
// It is the reference shape the builder must keep producing.
func buildRepresentative(t *testing.T) *SoapMessage {
	t.Helper()

	const body = `<tds:GetCapabilities><tds:Category>All</tds:Category></tds:GetCapabilities>`

	doc := etree.NewDocument()
	if err := doc.ReadFromString(body); err != nil {
		t.Fatalf("bad fixture: %v", err)
	}

	msg := NewEmptySOAP()
	msg.AddBodyContent(doc.Root())
	msg.AddRootNamespaces(map[string]string{
		"tds":   "http://www.onvif.org/ver10/device/wsdl",
		"onvif": "http://www.onvif.org/ver10/schema",
		"trt":   "http://www.onvif.org/ver10/media/wsdl",
	})
	return msg
}

// TestEnvelopeGolden is the oracle for any rewrite of the builder's internals: the
// canonical envelope must stay exactly this shape.
func TestEnvelopeGolden(t *testing.T) {
	const want = `<?xml version="1.0" encoding="UTF-8"?>
<soap-env:Envelope xmlns:onvif="http://www.onvif.org/ver10/schema" xmlns:soap-enc="http://www.w3.org/2003/05/soap-encoding" xmlns:soap-env="http://www.w3.org/2003/05/soap-envelope" xmlns:tds="http://www.onvif.org/ver10/device/wsdl" xmlns:trt="http://www.onvif.org/ver10/media/wsdl">
  <soap-env:Header/>
  <soap-env:Body>
    <tds:GetCapabilities>
      <tds:Category>All</tds:Category>
    </tds:GetCapabilities>
  </soap-env:Body>
</soap-env:Envelope>
`

	got := canonicalize(t, buildRepresentative(t).String())
	if got != want {
		t.Errorf("envelope changed shape.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestEnvelopeIsStableAcrossBuilds catches ordering non-determinism leaking into the
// canonical form: the same inputs must always canonicalise identically.
func TestEnvelopeIsStableAcrossBuilds(t *testing.T) {
	first := canonicalize(t, buildRepresentative(t).String())
	for i := 0; i < 20; i++ {
		if got := canonicalize(t, buildRepresentative(t).String()); got != first {
			t.Fatalf("envelope is not stable across builds (iteration %d)\n%s", i, got)
		}
	}
}

// TestNewEmptySOAPStructure checks the envelope skeleton the whole package relies on:
// every mutator does doc.Root().SelectElement("Header"/"Body") and would nil-panic
// without them.
func TestNewEmptySOAPStructure(t *testing.T) {
	doc := etree.NewDocument()
	if err := doc.ReadFromString(NewEmptySOAP().String()); err != nil {
		t.Fatalf("empty SOAP is not valid XML: %v", err)
	}
	root := doc.Root()
	if root.Tag != "Envelope" {
		t.Errorf("root tag = %q, want Envelope", root.Tag)
	}
	for _, tag := range []string{"Header", "Body"} {
		if root.SelectElement(tag) == nil {
			t.Errorf("the envelope has no %s element", tag)
		}
	}
}

// TestAddRootNamespacesAll checks every declaration survives, whatever the map order.
func TestAddRootNamespacesAll(t *testing.T) {
	ns := map[string]string{
		"a": "urn:a", "b": "urn:b", "c": "urn:c", "d": "urn:d", "e": "urn:e",
	}
	msg := NewEmptySOAP()
	msg.AddRootNamespaces(ns)

	got := msg.String()
	for key, value := range ns {
		if !strings.Contains(got, `xmlns:`+key+`="`+value+`"`) {
			t.Errorf("namespace %s=%s was dropped\ngot: %s", key, value, got)
		}
	}
}

// TestBodyAccessor exercises SoapMessage.Body, which indexes ChildElements()[0].
func TestBodyAccessor(t *testing.T) {
	if got := buildRepresentative(t).Body(); !strings.Contains(got, "GetCapabilities") {
		t.Errorf("Body() = %q", got)
	}
}

// TestCloneIsIndependent is the regression guard for the aliasing this type used to have.
// A SoapMessage was passed by value while holding a *etree.Document, so a copy shared the
// document with its original: mutating either changed both. Sharing is now explicit, and
// Clone is the sanctioned way to hand a message to someone else.
func TestCloneIsIndependent(t *testing.T) {
	original := buildRepresentative(t)
	snapshot := original.Clone()

	before := snapshot.String()
	original.AddAction("http://www.onvif.org/ver10/device/wsdl/GetCapabilities")
	original.AddRootNamespace("late", "urn:added-after-the-clone")

	if snapshot.String() != before {
		t.Error("mutating the original reached the clone")
	}
	if !strings.Contains(original.String(), "urn:added-after-the-clone") {
		t.Fatal("the mutation did not reach the original either: the test proves nothing")
	}

	// and the other way round
	snapshot.AddRootNamespace("other", "urn:added-to-the-clone")
	if strings.Contains(original.String(), "urn:added-to-the-clone") {
		t.Error("mutating the clone reached the original")
	}
}

// TestCloneDetachesAdoptedElements checks the escape route a caller keeps by holding on
// to an element it added. AddBodyContent adopts rather than copies, so that pointer still
// reaches the message it was added to; it must not reach a Clone.
func TestCloneDetachesAdoptedElements(t *testing.T) {
	msg := NewEmptySOAP()
	adopted := etree.NewElement("tds:GetCapabilities")
	msg.AddBodyContent(adopted)

	snapshot := msg.Clone()
	adopted.CreateAttr("mutated", "afterwards")

	if !strings.Contains(msg.String(), "mutated") {
		t.Error("an adopted element no longer reaches the message that adopted it")
	}
	if strings.Contains(snapshot.String(), "mutated") {
		t.Error("an adopted element reaches a clone taken before the mutation")
	}
}

// TestCloneOfEmptyMessages checks the degenerate receivers, since Clone is what a caller
// reaches for precisely when it is unsure what it holds.
func TestCloneOfEmptyMessages(t *testing.T) {
	var nilMsg *SoapMessage
	if got := nilMsg.Clone(); got == nil || got.String() != "" {
		t.Errorf("cloning a nil message = %v", got)
	}
	zero := &SoapMessage{}
	clone := zero.Clone()
	clone.AddBodyContent(etree.NewElement("tds:X"))
	if zero.String() != "" {
		t.Error("mutating the clone of a zero message reached the original")
	}
	if !strings.Contains(clone.String(), "tds:X") {
		t.Error("the clone of a zero message is not usable")
	}
}

// TestAddStringBodyContentReportsBadXML checks the parse failure reaches the caller. It
// used to go to the standard logger and the envelope was returned silently short of its
// body, which is a request no device can answer and nothing explains.
func TestAddStringBodyContentReportsBadXML(t *testing.T) {
	msg := NewEmptySOAP()

	if err := msg.AddStringBodyContent(`<tds:GetCapabilities/>`); err != nil {
		t.Fatalf("a well-formed fragment was rejected: %v", err)
	}
	if !strings.Contains(msg.String(), "tds:GetCapabilities") {
		t.Error("the fragment did not reach the body")
	}

	// Fragments etree rejects outright. Each must come back as an error, not as a
	// quietly unchanged envelope.
	for _, bad := range []string{"<unclosed", "<a></b>", "<a>&undefined;</a>"} {
		before := msg.String()
		err := msg.AddStringBodyContent(bad)
		if err == nil {
			t.Errorf("AddStringBodyContent(%q) reported nothing", bad)
		}
		if msg.String() != before {
			t.Errorf("AddStringBodyContent(%q) changed the body anyway", bad)
		}
	}

	// Fragments etree accepts by leaving the root nil. There is no error to report, so
	// the contract is only that the body is left alone rather than given a nil child.
	for _, empty := range []string{"", "   ", "not xml at all", `<?xml version="1.0"?>`} {
		before := msg.String()
		if err := msg.AddStringBodyContent(empty); err != nil {
			t.Errorf("AddStringBodyContent(%q) = %v, want no error", empty, err)
		}
		if msg.String() != before {
			t.Errorf("AddStringBodyContent(%q) changed the body", empty)
		}
	}
}

// TestNoStandardLogger keeps the package from reporting to a logger its caller never
// chose. The errors it used to log are unreachable; the one that was not now returns.
func TestNoStandardLogger(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	msg := buildRepresentative(t)
	_ = msg.String()
	_ = msg.StringIndent()
	_ = msg.Body()
	_ = msg.AddStringBodyContent("<not xml")
	_ = msg.AddStringHeaderContent("<not xml")
	msg.AddWSSecurity("admin", "hunter2")
	msg.Clone()

	if buf.Len() != 0 {
		t.Errorf("the package wrote to the standard logger:\n%s", buf.String())
	}
}
