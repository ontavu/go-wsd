// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// Portions of this file derive from github.com/jfsmig/onvif,
// originally distributed under the MIT License,
// Copyright (c) 2018 Yakovlev Dmitry, Zhorzh Palanjyan, Crazybber.
//
// SPDX-License-Identifier: MIT

package gosoap

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/xml"
	"time"
)

const (
	passwordType = "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordDigest"
	encodingType = "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-soap-message-security-1.0#Base64Binary"
)

// Security type :XMLName xml.Name `xml:"http://purl.org/rss/1.0/modules/content/ encoded"`
type Security struct {
	//XMLName xml.Name  `xml:"wsse:Security"`
	XMLName xml.Name `xml:"http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd Security"`
	Auth    wsAuth
}

type password struct {
	//XMLName xml.Name `xml:"wsse:Password"`
	Type     string `xml:"Type,attr"`
	Password string `xml:",chardata"`
}

type nonce struct {
	//XMLName xml.Name `xml:"wsse:Nonce"`
	Type  string `xml:"EncodingType,attr"`
	Nonce string `xml:",chardata"`
}

type wsAuth struct {
	XMLName  xml.Name `xml:"UsernameToken"`
	Username string   `xml:"Username"`
	Password password `xml:"Password"`
	Nonce    nonce    `xml:"Nonce"`
	Created  string   `xml:"http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd Created"`
}

/*
   <Security s:mustUnderstand="1" xmlns="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd">
       <UsernameToken>
           <Username>admin</Username>
           <Password Type="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordDigest">edBuG+qVavQKLoWuGWQdPab4IBE=</Password>
           <Nonce EncodingType="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-soap-message-security-1.0#Base64Binary">S7wO1ZFTh0KXv2CR7bd2ZXkLAAAAAA==</Nonce>
           <Created xmlns="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd">2018-04-10T18:04:25.836Z</Created>
       </UsernameToken>
   </Security>
*/

// NewSecurity builds a WS-Security UsernameToken header for the given credentials,
// with a fresh nonce and the current UTC time.
func NewSecurity(username, passwd string) Security {
	return newSecurityAt(username, passwd, newNonce(), time.Now().UTC())
}

// NewSecurityAt builds a UsernameToken header stamped at the given time, with a fresh
// nonce. Use it when the device clock is known to differ from the local one.
func NewSecurityAt(username, passwd string, created time.Time) Security {
	return newSecurityAt(username, passwd, newNonce(), created)
}

// nonceBytes is the length of the random nonce, matching the 24 bytes that the
// previous 32-character encoding happened to carry.
const nonceBytes = 24

// newNonce draws a cryptographically random nonce and encodes it as the Base64Binary
// the header declares.
//
// The nonce is announced with EncodingType=...#Base64Binary and generateToken decodes
// it before hashing, so it has to be genuine base64: the previous implementation
// generated 32 lowercase alphanumeric characters, which decoded only because that
// alphabet is a valid base64 subset and 32 is a multiple of 4.
func newNonce() string {
	buf := make([]byte, nonceBytes)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand does not fail in practice, and a predictable nonce would weaken
		// the replay protection the field exists for.
		panic("gosoap: cannot read random bytes for the WS-Security nonce: " + err.Error())
	}
	return base64.StdEncoding.EncodeToString(buf)
}

// newSecurityAt is the deterministic core of the constructors above: the two sources of
// non-determinism, the nonce and the clock, are injected by the caller so that the
// digest can be tested and so that a device clock offset can be applied.
func newSecurityAt(username, passwd, nonceSeq string, created time.Time) Security {
	stamp := created.Format(time.RFC3339Nano)
	return Security{
		Auth: wsAuth{
			Username: username,
			Password: password{
				Type:     passwordType,
				Password: generateToken(username, nonceSeq, stamp, passwd),
			},
			Nonce: nonce{
				Type:  encodingType,
				Nonce: nonceSeq,
			},
			Created: stamp,
		},
	}
}

// generateToken computes the UsernameToken digest mandated by the WS-Security
// UsernameToken profile:
//
//	Digest = B64ENCODE( SHA1( B64DECODE( Nonce ) + Created + Password ) )
//
// SHA-1 is what the profile specifies, not a choice. A nonce that is not valid base64
// is hashed as-is: silently substituting empty bytes would make the device compute a
// different digest and reject the credentials with no clue why.
func generateToken(Username string, Nonce string, Created string, Password string) string {
	decoded, err := base64.StdEncoding.DecodeString(Nonce)
	if err != nil {
		decoded = []byte(Nonce)
	}

	hasher := sha1.New()
	hasher.Write(decoded)
	hasher.Write([]byte(Created))
	hasher.Write([]byte(Password))

	return base64.StdEncoding.EncodeToString(hasher.Sum(nil))
}
