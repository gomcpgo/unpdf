// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package security

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// End-to-end tests with real encrypted files are in package reader, which
// builds them with an independent implementation of the write side.

func TestNewRejectsBadDictionaries(t *testing.T) {
	ok32, ok48 := make([]byte, 32), make([]byte, 48)
	tests := []struct {
		name string
		p    Params
		want error
	}{
		{"public-key handler", Params{Filter: "Adobe.PubSec", V: 4, R: 4}, ErrUnsupported},
		{"unknown version", Params{Filter: "Standard", V: 3, R: 3, O: ok32, U: ok32}, ErrUnsupported},
		{"unknown revision", Params{Filter: "Standard", V: 2, R: 7, O: ok32, U: ok32}, ErrUnsupported},
		{"unknown crypt filter method", Params{Filter: "Standard", V: 4, R: 4, O: ok32, U: ok32,
			CF: map[string]CryptFilter{"StdCF": {CFM: "AESV9"}}}, ErrUnsupported},
		{"short O", Params{Filter: "Standard", V: 2, R: 3, Length: 128, O: ok32[:10], U: ok32}, ErrInvalid},
		{"bad key length", Params{Filter: "Standard", V: 2, R: 3, Length: 4096, O: ok32, U: ok32}, ErrInvalid},
		{"short UE", Params{Filter: "Standard", V: 5, R: 6, O: ok48, U: ok48, OE: ok32, UE: ok32[:5]}, ErrInvalid},
		{"wrong password, revision 3", Params{Filter: "Standard", V: 2, R: 3, Length: 128, O: ok32, U: ok32}, ErrPassword},
		{"wrong password, revision 6", Params{Filter: "Standard", V: 5, R: 6, O: ok48, U: ok48, OE: ok32, UE: ok32}, ErrPassword},
	}
	for _, tt := range tests {
		if _, err := New(tt.p, "secret"); !errors.Is(err, tt.want) {
			t.Errorf("%s: got %v, want %v", tt.name, err, tt.want)
		}
	}
}

func TestPasswordForms(t *testing.T) {
	if got := legacyPasswords(""); len(got) != 1 || !bytes.Equal(got[0], padding) {
		t.Errorf("empty password: got %d forms", len(got))
	}
	// ASCII: the Latin-1 and UTF-8 forms are the same, so it is tried once.
	if got := legacyPasswords("abc"); len(got) != 2 || !bytes.HasPrefix(got[1], []byte("abc\x28\xBF")) {
		t.Errorf("ASCII password: got %d forms", len(got))
	}
	// "é" is one byte in Latin-1 and two in UTF-8: both are tried.
	if got := legacyPasswords("café"); len(got) != 3 || got[1][3] != 0xE9 || got[2][3] != 0xC3 {
		t.Errorf("accented password: got %d forms", len(got))
	}
	// Outside Latin-1 only UTF-8 is possible.
	if got := legacyPasswords("日本"); len(got) != 2 {
		t.Errorf("CJK password: got %d forms", len(got))
	}
	if got := pad([]byte(strings.Repeat("x", 40))); len(got) != 32 || got[31] != 'x' {
		t.Error("long password is not cut to 32 bytes")
	}
	long := truncate127(strings.Repeat("é", 100)) // 200 bytes of 2-byte runes
	if len(long) != 126 {
		t.Errorf("truncate127 kept %d bytes, want 126 (no split rune)", len(long))
	}
}

func TestDecryptDamagedInput(t *testing.T) {
	key16, key32 := make([]byte, 16), make([]byte, 32)
	for _, h := range []*Handler{
		{key: key16, stm: rc4Method, str: rc4Method},
		{key: key16, stm: aesV2, str: aesV2},
		{key: key32, stm: aesV3, str: aesV3},
		{key: nil, stm: aesV3, str: rc4Method}, // a key that no cipher accepts
	} {
		for _, n := range []int{0, 1, 15, 16, 17, 31, 32, 33, 47, 48, 100} {
			data := bytes.Repeat([]byte{0xA5}, n)
			h.DecryptString(1, 0, data)
			h.DecryptStream(1, 0, data, "")
			h.DecryptStream(1, 0, data, "NoSuchFilter")
		}
	}
}

func FuzzNew(f *testing.F) {
	f.Add(uint8(2), uint8(3), uint16(128), make([]byte, 32), make([]byte, 32), []byte{}, []byte{}, "pw", []byte("id"))
	f.Add(uint8(5), uint8(6), uint16(256), make([]byte, 48), make([]byte, 48), make([]byte, 32), make([]byte, 32), "", []byte{})
	f.Add(uint8(4), uint8(4), uint16(0), make([]byte, 40), make([]byte, 33), []byte{1}, []byte{2}, "\xff\xfe", []byte{0})
	f.Fuzz(func(t *testing.T, v, r uint8, length uint16, o, u, oe, ue []byte, pw string, id []byte) {
		p := Params{
			Filter: "Standard", V: int(v), R: int(r), Length: int(length),
			O: o, U: u, OE: oe, UE: ue, P: -44, EncryptMetadata: v%2 == 0, ID0: id,
			CF:   map[string]CryptFilter{"StdCF": {CFM: []string{"V2", "AESV2", "AESV3", "None"}[int(r)%4]}},
			StmF: "StdCF", StrF: "StdCF",
		}
		h, err := New(p, pw)
		if err != nil {
			return
		}
		h.DecryptString(7, 0, o)
		h.DecryptStream(7, 0, u, "")
	})
}
