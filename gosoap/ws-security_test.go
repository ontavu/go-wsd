// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// Portions of this file derive from github.com/jfsmig/onvif,
// originally distributed under the MIT License,
// Copyright (c) 2018 Yakovlev Dmitry, Zhorzh Palanjyan, Crazybber.
//
// SPDX-License-Identifier: MIT

package gosoap

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

// TestGenerateTokenKnownAnswer pins the UsernameToken digest algorithm mandated by the
// WS-Security UsernameToken profile:
//
//	Digest = B64ENCODE( SHA1( B64DECODE( Nonce ) + Created + Password ) )
//
// The vector below is derived from that formula. It must not move when the nonce
// generator changes, because devices compute the same digest independently.
func TestGenerateTokenKnownAnswer(t *testing.T) {
	const (
		nonceSeq = "S7wO1ZFTh0KXv2CR7bd2ZXkLAAAAAA=="
		created  = "2018-04-10T18:04:25.836Z"
		passwd   = "admin"
		want     = "qHbvHWcQqUMKlMEaXalKgGC7GF8="
	)

	if got := generateToken("admin", nonceSeq, created, passwd); got != want {
		t.Errorf("generateToken() = %q, want %q", got, want)
	}

	// The username is deliberately not part of the digest; changing it must not move it.
	if got := generateToken("someone-else", nonceSeq, created, passwd); got != want {
		t.Errorf("digest depends on the username, it must not: %q", got)
	}
}

// TestNewSecurityAtIsDeterministic checks the injected nonce and clock fully determine
// the header, which is what makes the digest reproducible on the device side.
func TestNewSecurityAtIsDeterministic(t *testing.T) {
	at := time.Date(2018, 4, 10, 18, 4, 25, 836000000, time.UTC)

	a := newSecurityAt("admin", "hunter2", "S7wO1ZFTh0KXv2CR7bd2ZXkLAAAAAA==", at)
	b := newSecurityAt("admin", "hunter2", "S7wO1ZFTh0KXv2CR7bd2ZXkLAAAAAA==", at)

	if a != b {
		t.Fatal("newSecurityAt is not deterministic for a fixed nonce and time")
	}
	if a.Auth.Username != "admin" {
		t.Errorf("Username = %q", a.Auth.Username)
	}
	if a.Auth.Created != "2018-04-10T18:04:25.836Z" {
		t.Errorf("Created = %q, want RFC3339Nano UTC", a.Auth.Created)
	}
	if a.Auth.Password.Type != passwordType {
		t.Errorf("Password.Type = %q", a.Auth.Password.Type)
	}
	if a.Auth.Nonce.Type != encodingType {
		t.Errorf("Nonce.Type = %q", a.Auth.Nonce.Type)
	}
}

// TestNewSecurityAtDistinctPasswords guards against the digest being dropped or
// constant, which would silently authenticate with anything.
func TestNewSecurityAtDistinctPasswords(t *testing.T) {
	at := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	one := newSecurityAt("admin", "a", "bm9uY2UtdmFsdWUtMTIzNDU2Nzg5MA==", at)
	two := newSecurityAt("admin", "b", "bm9uY2UtdmFsdWUtMTIzNDU2Nzg5MA==", at)

	if one.Auth.Password.Password == "" {
		t.Fatal("digest is empty")
	}
	if one.Auth.Password.Password == two.Auth.Password.Password {
		t.Error("two different passwords produced the same digest")
	}
}

// TestNewSecurityNonceIsValidBase64 is the regression guard for the nonce encoding.
// The header declares EncodingType=...#Base64Binary and generateToken base64-decodes
// the value, so a nonce that is not valid base64 makes client and device hash
// different bytes.
func TestNewSecurityNonceIsValidBase64(t *testing.T) {
	for i := 0; i < 50; i++ {
		sec := NewSecurity("admin", "admin")
		got := sec.Auth.Nonce.Nonce
		if got == "" {
			t.Fatal("nonce is empty")
		}
		if _, err := base64.StdEncoding.DecodeString(got); err != nil {
			t.Fatalf("nonce %q is not valid base64: %v", got, err)
		}
	}
}

// TestNewSecurityNonceIsFresh checks successive headers do not reuse a nonce, which
// would defeat the replay protection the nonce exists for.
func TestNewSecurityNonceIsFresh(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		n := NewSecurity("admin", "admin").Auth.Nonce.Nonce
		if seen[n] {
			t.Fatalf("nonce %q reused after %d draws", n, i)
		}
		seen[n] = true
	}
}

// TestAddWSSecurityHeader checks the header actually lands in the envelope, under the
// WS-Security namespace the profile requires.
func TestAddWSSecurityHeader(t *testing.T) {
	msg := NewEmptySOAP()
	msg.AddWSSecurity("admin", "hunter2")
	got := msg.String()

	for _, want := range []string{
		"Security",
		"UsernameToken",
		"<Username>admin</Username>",
		"oasis-200401-wss-wssecurity-secext-1.0.xsd",
		passwordType,
		encodingType,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("security header is missing %q\ngot: %s", want, got)
		}
	}
	if strings.Contains(got, "hunter2") {
		t.Error("the cleartext password leaked into the envelope")
	}
}

// TestAddWSSecurityAtUsesSuppliedStamp checks the Created timestamp actually comes from
// the caller, which is how the device clock offset reaches the wire.
func TestAddWSSecurityAtUsesSuppliedStamp(t *testing.T) {
	at := time.Date(2019, 7, 4, 12, 30, 15, 0, time.UTC)

	msg := NewEmptySOAP()
	msg.AddWSSecurityAt("admin", "hunter2", at)

	if got := msg.String(); !strings.Contains(got, "2019-07-04T12:30:15Z") {
		t.Errorf("the supplied timestamp did not reach the header\ngot: %s", got)
	}
}

// TestNewSecurityAtHonoursOffset checks two headers stamped an hour apart differ, so a
// clock offset genuinely changes the digest a device will verify.
func TestNewSecurityAtHonoursOffset(t *testing.T) {
	base := time.Date(2019, 7, 4, 12, 0, 0, 0, time.UTC)

	a := newSecurityAt("admin", "p", "bm9uY2UtdmFsdWUtMTIzNDU2Nzg5MA==", base)
	b := newSecurityAt("admin", "p", "bm9uY2UtdmFsdWUtMTIzNDU2Nzg5MA==", base.Add(time.Hour))

	if a.Auth.Created == b.Auth.Created {
		t.Error("the Created stamp ignored the offset")
	}
	if a.Auth.Password.Password == b.Auth.Password.Password {
		t.Error("the digest ignored the Created stamp")
	}
}
