// Copyright (C) 2022-2026 Jean-Francois SMIGIELSKI
//
// SPDX-License-Identifier: MIT

//go:build audit

// The gosoap defects found by the 2026-09-11 whole-tree audit. See wsd/audit_test.go for
// why these sit behind a build tag, and .claude/AUDIT-2026-09-11.md for the evidence.

package gosoap

import (
	"reflect"
	"testing"
	"unicode"
)

// TestSecurityIsUsableByAnOutsideCaller — audit D1.
//
// Security is exported and its field Auth is exported, but wsAuth is not. An outside caller
// can therefore receive a Security from NewSecurity and can never write one, name its type
// in a signature, or read a field of it. An exported field of an unexported type is the
// shape that compiles and does not work.
//
// This is inside the half of gosoap that nothing in this repository reaches: WS-Discovery
// carries no credentials, so ws-security.go has no caller outside its own tests.
func TestSecurityIsUsableByAnOutsideCaller(t *testing.T) {
	for i, field := range reflect.VisibleFields(reflect.TypeOf(Security{})) {
		if !field.IsExported() {
			continue
		}
		name := field.Type.Name()
		if name == "" || !unicode.IsUpper(rune(name[0])) {
			t.Errorf("Security field %d (%s) is exported but its type %s is not, so no "+
				"caller outside this package can construct a Security or read that field",
				i, field.Name, field.Type)
		}
	}
}
