// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// Portions of this file derive from the ws-discovery project,
// Copyright (C) 2018 Palanjyan Zhorzhik.
//
// SPDX-License-Identifier: MIT

package wsd

import (
	"fmt"
	"strings"

	"github.com/beevik/etree"
	"github.com/ontavu/go-wsd/gosoap"
)

// soapEnvelope is the SOAP 1.2 envelope namespace. gosoap declares it at the root under
// the soap-env prefix, which is what qualifies the mustUnderstand attribute below.
const soapEnvPrefix = "soap-env"

// buildProbeMessage assembles a WS-Discovery Probe for the given dialect.
//
// messageID must be an absolute URI; use newMessageID. types and scopes are
// space-delimited QName and URI lists respectively, and either may be empty: a Probe
// carrying neither matches every Target Service, which is how the ONVIF device types
// are covered without relying on the conjunctive Types matching rule.
func buildProbeMessage(messageID string, scopes []string, types []TypeName, fl dialect) *gosoap.SoapMessage {
	probeMessage := gosoap.NewEmptySOAP()
	probeMessage.AddRootNamespaces(map[string]string{"a": fl.addressing})

	probeMessage.AddHeaderContents(probeHeader(messageID, fl))
	probeMessage.AddBodyContent(probeBody(scopes, types, fl))

	return probeMessage
}

// probeHeader builds the WS-Addressing header block of a Probe.
//
// Action and To are marked mustUnderstand, and the attribute is qualified with the SOAP
// envelope namespace: SOAP 1.2 Part 1 section 5.2.3 gives the attribute a [namespace
// name] of the envelope namespace, so an unqualified "mustUnderstand" is a foreign
// attribute that every conformant receiver ignores.
func probeHeader(messageID string, fl dialect) []*etree.Element {
	action := etree.NewElement("a:Action")
	action.SetText(fl.probeAction)
	mustUnderstand(action)

	msgID := etree.NewElement("a:MessageID")
	msgID.SetText(messageID)

	replyTo := etree.NewElement("a:ReplyTo")
	replyTo.CreateElement("a:Address").SetText(fl.anonymous)

	to := etree.NewElement("a:To")
	to.SetText(fl.to)
	mustUnderstand(to)

	return []*etree.Element{action, msgID, replyTo, to}
}

// mustUnderstand marks a header block as mandatory to understand.
func mustUnderstand(element *etree.Element) {
	element.CreateAttr(soapEnvPrefix+":mustUnderstand", "1")
}

// probeBody builds the d:Probe element.
//
// The discovery prefix is declared once on Probe itself so that it is in scope for both
// children. Declaring it on d:Types, as an earlier version did, left the sibling
// d:Scopes with an undeclared prefix and produced namespace-ill-formed XML.
//
// The d:Types element is always emitted, empty when no type was asked for. WS-Discovery
// says an absent Types matches every Target Service, and it was omitted on that reading,
// but equipment disagrees: measured against three ONVIF cameras and one non-ONVIF device
// on the same link, a Probe with no d:Types drew one reply out of four while the same
// Probe carrying an empty one drew all four. Firmware keys on the element being there.
func probeBody(scopes []string, types []TypeName, fl dialect) *etree.Element {
	probe := etree.NewElement("d:Probe")
	probe.CreateAttr("xmlns:d", fl.discovery)

	typesTag := probe.CreateElement("d:Types")
	if len(types) != 0 {
		// The QNames live in the element's content, so the namespace each one denotes
		// has to be declared here. The prefixes are ours to invent: a caller carries
		// whole TypeName values and never has to keep a prefix map in step.
		prefixes := make(map[string]string, len(types))
		qnames := make([]string, 0, len(types))
		for _, t := range types {
			// A prefix cannot be bound to an empty namespace, and emitting an unbound
			// one would make the Probe namespace-ill-formed for every device on the
			// link, not just for the entry that is wrong.
			if t.Namespace == "" || t.Local == "" {
				continue
			}
			prefix, seen := prefixes[t.Namespace]
			if !seen {
				prefix = fmt.Sprintf("t%d", len(prefixes))
				prefixes[t.Namespace] = prefix
				typesTag.CreateAttr("xmlns:"+prefix, t.Namespace)
			}
			qnames = append(qnames, prefix+":"+t.Local)
		}
		typesTag.SetText(strings.Join(qnames, " "))
	}

	if len(scopes) != 0 {
		probe.CreateElement("d:Scopes").SetText(strings.Join(scopes, " "))
	}

	return probe
}
