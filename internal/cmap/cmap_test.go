// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package cmap

import (
	"strings"
	"testing"
)

const toUnicode = `/CIDInit /ProcSet findresource begin
12 dict begin
begincmap
/CIDSystemInfo << /Registry (Adobe) /Ordering (UCS) /Supplement 0 >> def
/CMapName /Adobe-Identity-UCS def
/CMapType 2 def
1 begincodespacerange
<0000> <FFFF>
endcodespacerange
4 beginbfchar
<0003> <0020>
<0010> <00660069>
<0011> <D83DDE00>
<0012> <>
endbfchar
4 beginbfrange
<0020> <0022> <0041>
<0030> <0032> [<0058> <00590059> <005A>]
<0040> <0041> <D835DC00>
<0050> <0051> <00410030>
endbfrange
endcmap
CMapName currentdict /CMap defineresource pop
end
end`

func TestToUnicode(t *testing.T) {
	c, err := Parse([]byte(toUnicode))
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "Adobe-Identity-UCS" || !c.HasUnicode() || c.HasCIDs() {
		t.Errorf("name %q, unicode %v, cids %v", c.Name, c.HasUnicode(), c.HasCIDs())
	}
	if len(c.Space) != 1 || c.Space[0] != (CodeRange{N: 2, Lo: 0, Hi: 0xFFFF}) {
		t.Errorf("codespace = %+v", c.Space)
	}
	tests := []struct {
		code uint32
		want string
		ok   bool
	}{
		{0x03, " ", true},
		{0x10, "fi", true},                  // one code, two characters
		{0x11, string(rune(0x1F600)), true}, // a surrogate pair
		{0x12, "", true},                    // mapped to nothing
		{0x20, "A", true},                   // start of a range
		{0x22, "C", true},                   // end of a range
		{0x23, "", false},                   // just past it
		{0x30, "X", true},                   // range given as an array
		{0x31, "YY", true},
		{0x32, "Z", true},
		{0x40, string(rune(0x1D400)), true}, // range outside the BMP
		{0x41, string(rune(0x1D401)), true},
		{0x51, "A1", true}, // only the last unit counts up
		{0x99, "", false},
	}
	for _, tt := range tests {
		got, ok := c.Unicode(tt.code)
		if got != tt.want || ok != tt.ok {
			t.Errorf("Unicode(%#x) = %q, %v; want %q, %v", tt.code, got, ok, tt.want, tt.ok)
		}
	}
}

func TestLaterMappingsWin(t *testing.T) {
	c, _ := Parse([]byte(`2 beginbfrange <00> <FF> <0100> <41> <42> <0061> endbfrange 1 beginbfchar <41> <0058> endbfchar`))
	for code, want := range map[uint32]string{0x40: string(rune(0x140)), 0x41: "X", 0x42: "b", 0x43: string(rune(0x143))} {
		if got, _ := c.Unicode(code); got != want {
			t.Errorf("Unicode(%#x) = %q, want %q", code, got, want)
		}
	}
}

func TestEncodingCMap(t *testing.T) {
	c, err := Parse([]byte(`/CMapName /Test-V def /WMode 1 def /90ms-RKSJ-H usecmap
3 begincodespacerange <00> <80> <8140> <9FFC> <A0> <DF> endcodespacerange
2 begincidrange <20> <7E> 1 <8140> <817E> 633 endcidrange
1 begincidchar <A0> 9999 endcidchar
1 beginnotdefrange <00> <1F> 1 endnotdefrange`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "Test-V" || c.WMode != 1 || c.UseCMap != "90ms-RKSJ-H" || !c.HasCIDs() || c.HasUnicode() {
		t.Errorf("%+v", c)
	}
	for _, tt := range []struct {
		code uint32
		n    int
		cid  uint32
		ok   bool
	}{
		{0x20, 1, 1, true}, {0x41, 1, 34, true}, {0x8141, 2, 634, true}, {0xA0, 1, 9999, true},
		{0x41, 2, 0, false}, // same value, different length
		{0x10, 1, 0, false}, // only in a notdef range
	} {
		if cid, ok := c.CID(tt.code, tt.n); cid != tt.cid || ok != tt.ok {
			t.Errorf("CID(%#x, %d) = %d, %v; want %d, %v", tt.code, tt.n, cid, ok, tt.cid, tt.ok)
		}
	}

	// Splitting a string into codes with the codespace.
	input := []byte{0x41, 0x81, 0x41, 0xA5, 0xFF, 0x42, 0x81}
	var codes []uint32
	var lens []int
	for len(input) > 0 {
		code, n := Next(c.Space, input)
		if n < 1 {
			t.Fatal("Next made no progress")
		}
		codes, lens = append(codes, code), append(lens, n)
		input = input[n:]
	}
	wantCodes := []uint32{0x41, 0x8141, 0xA5, 0xFF, 0x42, 0x81}
	wantLens := []int{1, 2, 1, 1, 1, 1}
	for i := range wantCodes {
		if i >= len(codes) || codes[i] != wantCodes[i] || lens[i] != wantLens[i] {
			t.Fatalf("codes %x lens %v; want %x %v", codes, lens, wantCodes, wantLens)
		}
	}
	if code, n := Next(nil, []byte{0x12, 0x34}); code != 0x12 || n != 1 {
		t.Errorf("Next with no codespace = %#x, %d", code, n)
	}
	if _, n := Next(c.Space, nil); n != 0 {
		t.Errorf("Next on empty input consumed %d", n)
	}
}

func TestDamagedPrograms(t *testing.T) {
	for _, in := range []string{
		"", "beginbfchar", "1 beginbfchar <41>", "beginbfrange <41> <40> <0041> endbfrange",
		"beginbfchar <4142434445> <0041> (text) 5 /Name endbfchar", "begincidrange <41> <42> -5 <41> <4242> 7 endcidrange",
		"))) >> ] beginbfchar <41> <0042> endbfchar", "beginbfrange <41> <43> [<0041>] endbfrange",
		strings.Repeat("( ", 5000),
	} {
		c, _ := Parse([]byte(in))
		if c == nil {
			t.Fatalf("Parse(%q) returned nil", in)
		}
		c.Unicode(0x41)
		c.Unicode(0x43)
		c.CID(0x41, 1)
	}
	// Mappings before the damage survive it.
	c, _ := Parse([]byte("beginbfchar <41> <0042> endbfchar ))))"))
	if s, ok := c.Unicode(0x41); !ok || s != "B" {
		t.Errorf("mapping lost: %q, %v", s, ok)
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte(toUnicode))
	f.Add([]byte("1 begincodespacerange <00> <FF> endcodespacerange 1 begincidrange <00> <FF> 0 endcidrange"))
	f.Add([]byte("beginbfrange <0000> <FFFF> [<0041>] <00> <FF> <D800> endbfrange"))
	f.Fuzz(func(t *testing.T, data []byte) {
		c, _ := Parse(data)
		if c == nil {
			t.Fatal("nil CMap")
		}
		for _, code := range []uint32{0, 1, 0x41, 0xFF, 0x100, 0xFFFF, 0x10000, 0xFFFFFFFF} {
			c.Unicode(code)
			for n := 1; n <= 4; n++ {
				c.CID(code, n)
			}
		}
		s := data
		for i := 0; len(s) > 0 && i < 64; i++ {
			_, n := Next(c.Space, s)
			if n < 1 || n > len(s) {
				t.Fatalf("Next consumed %d of %d bytes", n, len(s))
			}
			s = s[n:]
		}
	})
}
