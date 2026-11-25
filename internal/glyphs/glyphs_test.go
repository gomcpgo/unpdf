// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package glyphs

import "testing"

func TestName(t *testing.T) {
	tests := []struct {
		name string
		zapf bool
		want string
		ok   bool
	}{
		{"A", false, "A", true},
		{"space", false, " ", true},
		{"Aacute", false, "Á", true},
		{"fi", false, "ﬁ", true},
		{"ffi", false, "ﬃ", true},
		{"afii10017", false, "А", true},
		{"dalethatafpatah", false, "דֲ", true}, // one name, two code points
		{"uni0041", false, "A", true},
		{"uni00410042", false, "AB", true},
		{"u1F600", false, "\U0001F600", true},
		{"u0041", false, "A", true},
		{"A.sc", false, "A", true},
		{"f_f_i", false, "ffi", true},
		{"T_h.swash", false, "Th", true},
		{"a100", true, "❞", true},
		{"a100", false, "", false},
		{"Omega", false, string(rune(0x03A9)), true},
		{"Delta", false, string(rune(0x0394)), true},
		{"mu", false, string(rune(0x03BC)), true},
		{"Omegagreek", false, string(rune(0x03A9)), true},
		{"negationslash", false, string(rune(0x0338)), true},
		{"mapsto", false, string(rune(0x21A6)), true},
		{"parenleftBig", false, "(", true},
		{"summationdisplay", false, string(rune(0x2211)), true},
		{"radicalbigg", false, string(rune(0x221A)), true},
		{"dotlessj", false, string(rune(0x0237)), true},
		{"hatwide", false, string(rune(0x0302)), true},
		{"g123", false, "", false},
		{"uniD800", false, "", false},
		{"uni004", false, "", false},
		{"u110000", false, "", false},
		{"unknown_A", false, "", false},
		{"", false, "", false},
		{".notdef", false, "", false},
	}
	for _, tt := range tests {
		got, ok := Name(tt.name, tt.zapf)
		if got != tt.want || ok != tt.ok {
			t.Errorf("Name(%q, %v) = %q, %v; want %q, %v", tt.name, tt.zapf, got, ok, tt.want, tt.ok)
		}
	}
}

func TestEncodings(t *testing.T) {
	tests := []struct {
		encoding string
		code     byte
		want     rune
	}{
		{"WinAnsiEncoding", 'A', 'A'},
		{"WinAnsiEncoding", 0x80, 0x20AC},
		{"WinAnsiEncoding", 0x92, 0x2019},
		{"WinAnsiEncoding", 0xE9, 0xE9},
		{"WinAnsiEncoding", 0xAD, '-'},
		{"MacRomanEncoding", 0x8E, 0xE9},
		{"MacRomanEncoding", 0xD5, 0x2019},
		{"MacRomanEncoding", 0xDE, 0xFB01},
		{"StandardEncoding", 'A', 'A'},
		{"StandardEncoding", 0x27, 0x2019}, // quoteright, not the ASCII apostrophe
		{"StandardEncoding", 0xAE, 0xFB01},
		{"StandardEncoding", 0xE1, 0xC6},
		{"SymbolEncoding", 0x61, 0x03B1},
		{"SymbolEncoding", 0xA5, 0x221E},
		{"ZapfDingbatsEncoding", 0x21, 0x2701},
		{"MacExpertEncoding", 'A', 'A'},
	}
	for _, tt := range tests {
		table, ok := Encoding(tt.encoding)
		if !ok {
			t.Fatalf("no encoding %s", tt.encoding)
		}
		if table[tt.code] != tt.want {
			t.Errorf("%s[%#x] = %U, want %U", tt.encoding, tt.code, table[tt.code], tt.want)
		}
	}
	if _, ok := Encoding("NoSuchEncoding"); ok {
		t.Error("an unknown encoding was found")
	}
	std, _ := Encoding("StandardEncoding")
	if std[0x80] != 0 {
		t.Errorf("StandardEncoding[0x80] = %U, want undefined", std[0x80])
	}
}
