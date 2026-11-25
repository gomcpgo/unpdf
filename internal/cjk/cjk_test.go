// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package cjk

import "testing"

func TestPredefined(t *testing.T) {
	tests := []struct {
		cmap       string
		input      []byte
		code       uint32
		n          int
		cid        uint32
		ordering   string
		want       rune
		direct     bool // the code itself is Unicode
		vertical   bool
		collection string
	}{
		// Shift-JIS: HIRAGANA LETTER A, the ideographic comma, an ASCII letter.
		{"90ms-RKSJ-H", []byte{0x82, 0xA0, 0x41}, 0x82A0, 2, 843, "Japan1", 0x3042, false, false, "Adobe-Japan1"},
		{"90ms-RKSJ-V", []byte{0x81, 0x41}, 0x8141, 2, 634, "Japan1", 0x3001, false, true, "Adobe-Japan1"},
		{"90ms-RKSJ-H", []byte{0x41, 0x82}, 0x41, 1, 0, "Japan1", 'A', false, false, "Adobe-Japan1"},
		{"83pv-RKSJ-H", []byte{0x82, 0xA0}, 0x82A0, 2, 843, "Japan1", 0x3042, false, false, "Adobe-Japan1"},
		// JIS X 0208 row-cell and EUC-JP.
		{"H", []byte{0x24, 0x22}, 0x2422, 2, 843, "Japan1", 0x3042, false, false, "Adobe-Japan1"},
		{"V", []byte{0x24, 0x22}, 0x2422, 2, 843, "Japan1", 0x3042, false, true, "Adobe-Japan1"},
		{"EUC-H", []byte{0xA4, 0xA2}, 0xA4A2, 2, 843, "Japan1", 0x3042, false, false, "Adobe-Japan1"},
		// Simplified Chinese, Traditional Chinese, Korean.
		{"GBK-EUC-H", []byte{0xB0, 0xA1}, 0xB0A1, 2, 0, "GB1", 0x554A, false, false, "Adobe-GB1"},
		{"GB-EUC-H", []byte{0xB0, 0xA1}, 0xB0A1, 2, 0, "GB1", 0x554A, false, false, "Adobe-GB1"},
		{"ETen-B5-H", []byte{0xA4, 0x40}, 0xA440, 2, 0, "CNS1", 0x4E00, false, false, "Adobe-CNS1"},
		{"KSCms-UHC-H", []byte{0xB0, 0xA1}, 0xB0A1, 2, 0, "Korea1", 0xAC00, false, false, "Adobe-Korea1"},
		{"KSC-EUC-V", []byte{0xB0, 0xA1}, 0xB0A1, 2, 0, "Korea1", 0xAC00, false, true, "Adobe-Korea1"},
		// Unicode CMaps: the code is the character.
		{"UniJIS-UCS2-H", []byte{0x30, 0x42}, 0x3042, 2, 843, "Japan1", 0x3042, true, false, "Adobe-Japan1"},
		{"UniGB-UTF16-V", []byte{0x55, 0x4A}, 0x554A, 2, 0, "GB1", 0x554A, true, true, "Adobe-GB1"},
		{"UniJIS-UTF16-H", []byte{0xD8, 0x40, 0xDC, 0x0B}, 0xD840DC0B, 4, 0, "Japan1", 0x2000B, true, false, "Adobe-Japan1"},
		{"UniKS-UTF8-H", []byte{0xEA, 0xB0, 0x80}, 0xEAB080, 3, 0, "Korea1", 0xAC00, true, false, "Adobe-Korea1"},
		{"UniCNS-UTF32-H", []byte{0, 0, 0x4E, 0x00}, 0x4E00, 4, 0, "CNS1", 0x4E00, true, false, "Adobe-CNS1"},
	}
	for _, tt := range tests {
		m, ok := Predefined(tt.cmap)
		if !ok {
			t.Errorf("%s: not found", tt.cmap)
			continue
		}
		if m.Vertical != tt.vertical || m.Collection != tt.collection {
			t.Errorf("%s: vertical %v, collection %q", tt.cmap, m.Vertical, m.Collection)
		}
		code, n := m.Next(tt.input)
		if code != tt.code || n != tt.n {
			t.Errorf("%s: Next = %#x, %d; want %#x, %d", tt.cmap, code, n, tt.code, tt.n)
			continue
		}
		if r, ok := m.Unicode(code, n); ok != tt.direct || (ok && r != tt.want) {
			t.Errorf("%s: Unicode = %U, %v", tt.cmap, r, ok)
		}
		cid, ok := m.CID(code, n)
		if !ok || (tt.cid != 0 && cid != tt.cid) {
			t.Errorf("%s: CID = %d, %v; want %d", tt.cmap, cid, ok, tt.cid)
			continue
		}
		if r, ok := ToUnicode("Adobe", tt.ordering, cid); !ok || r != tt.want {
			t.Errorf("%s: CID %d -> %U, %v; want %U", tt.cmap, cid, r, ok, tt.want)
		}
	}
}

func TestIdentityAndUnknown(t *testing.T) {
	for _, name := range []string{"Identity-H", "Identity-V"} {
		m, ok := Predefined(name)
		if !ok || m.Collection != "" || m.Vertical != (name == "Identity-V") {
			t.Fatalf("%s: %+v, %v", name, m, ok)
		}
		if code, n := m.Next([]byte{0x12, 0x34, 0x56}); code != 0x1234 || n != 2 {
			t.Errorf("%s: Next = %#x, %d", name, code, n)
		}
		if code, n := m.Next([]byte{0x12}); code != 0x12 || n != 1 {
			t.Errorf("%s: Next on one byte = %#x, %d", name, code, n)
		}
		if cid, ok := m.CID(0x1234, 2); !ok || cid != 0x1234 {
			t.Errorf("%s: CID = %d, %v", name, cid, ok)
		}
		if _, ok := m.Unicode(0x1234, 2); ok {
			t.Errorf("%s: Identity has no Unicode of its own", name)
		}
	}
	for _, name := range []string{"", "NoSuch-H", "UniXYZ-UCS2-H", "UniJIS-H", "Identity"} {
		if _, ok := Predefined(name); ok && name != "Identity" {
			t.Errorf("Predefined(%q) succeeded", name)
		}
	}
	for _, c := range []struct{ registry, ordering string }{{"Adobe", "Identity"}, {"Other", "Japan1"}, {"Adobe", "../x"}} {
		if _, ok := ToUnicode(c.registry, c.ordering, 843); ok {
			t.Errorf("ToUnicode(%q, %q) succeeded", c.registry, c.ordering)
		}
	}
	if _, ok := ToUnicode("Adobe", "Japan1", 1<<30); ok {
		t.Error("a CID far out of range mapped to something")
	}
	if _, ok := ToUnicode("Adobe", "KR", 100); !ok {
		t.Error("Adobe-KR has no entry for CID 100")
	}
}

// TestLegacyDecodingStaysInStep feeds bytes that are not valid codes and
// checks that decoding always moves forward.
func TestLegacyDecodingStaysInStep(t *testing.T) {
	for name := range legacyCollection {
		m, ok := Predefined(name + "-H")
		if !ok {
			if m, ok = Predefined(name); !ok {
				t.Errorf("%s: listed in the index but not loadable", name)
				continue
			}
		}
		s := []byte{0xFF, 0x00, 0x81, 0x30, 0x81, 0x30, 0x8E, 0xA1, 0xFE, 0xFE, 0x80}
		for len(s) > 0 {
			_, n := m.Next(s)
			if n < 1 || n > len(s) {
				t.Fatalf("%s: Next consumed %d of %d bytes", name, n, len(s))
			}
			s = s[n:]
		}
	}
}
