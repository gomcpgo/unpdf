// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package font

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/gomcpgo/unpdf/internal/pdfobj"
	"github.com/gomcpgo/unpdf/internal/pdftest"
	"github.com/gomcpgo/unpdf/internal/reader"
)

var ctx = context.Background()

// load builds a document around one font dictionary and loads the font.
// build receives the builder to add the objects the font refers to, and
// returns the font dictionary in PDF syntax.
func load(t *testing.T, build func(b *pdftest.Builder) string) *Font {
	t.Helper()
	b := pdftest.New()
	num := b.Add(build(b))
	b.Pages(pdftest.PageSpec{})
	data := b.Bytes()
	f, err := reader.Open(ctx, bytes.NewReader(data), int64(len(data)), "", reader.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	return NewCache(f).Get(ctx, pdfobj.Ref{Num: num})
}

// decode runs a string through the font and returns the text, with U+FFFD
// for unmapped glyphs, and the summed width.
func decode(fnt *Font, s []byte) (text string, width float64) {
	for len(s) > 0 {
		g := fnt.Next(s)
		if !g.Mapped {
			text += string(rune(0xFFFD))
		}
		text += g.Text
		width += g.Width
		s = s[g.Bytes:]
	}
	return text, width
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestSimpleFontEncodings(t *testing.T) {
	t.Run("named encoding with differences and widths", func(t *testing.T) {
		fnt := load(t, func(b *pdftest.Builder) string {
			return `<< /Type /Font /Subtype /TrueType /BaseFont /ABCDEF+Arial-BoldItalicMT /Encoding
				<< /BaseEncoding /WinAnsiEncoding /Differences [65 /Omega /fi 128 /uni20AC /f_f_i /g17] >>
				/FirstChar 65 /LastChar 67 /Widths [700 550 600] >>`
		})
		if fnt.Name != "Arial-BoldItalicMT" || !fnt.Bold || !fnt.Italic || fnt.Subtype != "TrueType" {
			t.Errorf("name %q bold %v italic %v", fnt.Name, fnt.Bold, fnt.Italic)
		}
		// A is renamed Omega, B is the fi ligature (expanded), C is unchanged,
		// 0x92 comes from WinAnsi, 128 to 130 from the differences.
		text, width := decode(fnt, []byte{65, 66, 67, 0x92, 128, 129, 130})
		want := string(rune(0x03A9)) + "fiC" + string(rune(0x2019)) + string(rune(0x20AC)) + "ffi" + string(rune(0xFFFD))
		if text != want {
			t.Errorf("text = %+q, want %+q", text, want)
		}
		if !near(width, 0.7+0.55+0.6+4*0.5) {
			t.Errorf("width = %v", width)
		}
		if fnt.Unmapped != 1 {
			t.Errorf("Unmapped = %d, want 1", fnt.Unmapped)
		}
	})

	t.Run("standard font without widths", func(t *testing.T) {
		fnt := load(t, func(b *pdftest.Builder) string {
			return `<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>`
		})
		// Helvetica: H 722, i 222, space 278.
		if _, w := decode(fnt, []byte("Hi ")); !near(w, 0.722+0.222+0.278) {
			t.Errorf("width of \"Hi \" = %v", w)
		}
		if g := fnt.Next([]byte(" ")); !g.Space || g.Text != " " {
			t.Errorf("space glyph = %+v", g)
		}
		courier := load(t, func(b *pdftest.Builder) string {
			return `<< /Type /Font /Subtype /Type1 /BaseFont /Courier-Bold >>`
		})
		if _, w := decode(courier, []byte("iW")); !near(w, 1.2) || !courier.Fixed || !courier.Bold {
			t.Errorf("Courier width = %v, fixed %v", w, courier.Fixed)
		}
	})

	t.Run("no encoding: Standard for Type 1, symbol fonts by name", func(t *testing.T) {
		std := load(t, func(b *pdftest.Builder) string { return `<< /Type /Font /Subtype /Type1 /BaseFont /Unknown >>` })
		if text, _ := decode(std, []byte{0x27, 0xAE}); text != string(rune(0x2019))+"fi" {
			t.Errorf("StandardEncoding text = %q", text)
		}
		sym := load(t, func(b *pdftest.Builder) string { return `<< /Type /Font /Subtype /Type1 /BaseFont /Symbol >>` })
		if text, _ := decode(sym, []byte("ab")); text != string(rune(0x03B1))+string(rune(0x03B2)) {
			t.Errorf("Symbol text = %q", text)
		}
		zapf := load(t, func(b *pdftest.Builder) string {
			return `<< /Type /Font /Subtype /Type1 /BaseFont /ZapfDingbats /Encoding << /Differences [65 /a100] >> >>`
		})
		if text, _ := decode(zapf, []byte{0x21, 65}); text != string(rune(0x2701))+string(rune(0x275E)) {
			t.Errorf("ZapfDingbats text = %q", text)
		}
	})

	t.Run("encoding from an embedded Type 1 font program", func(t *testing.T) {
		fnt := load(t, func(b *pdftest.Builder) string {
			program := "%!PS-AdobeFont-1.0: CMR10\n/FontName /CMR10 def\n/Encoding 256 array\n0 1 255 {1 index exch /.notdef put} for\n" +
				"dup 11 /ff put\ndup 12 /fi put\ndup 65 /A put\ndup 123 /endash put\nreadonly def\ncurrentdict end\ncurrentfile eexec\n\x80\x81binary"
			file := b.AddStream("<< >>", []byte(program))
			desc := b.Add(fmt.Sprintf("<< /Type /FontDescriptor /FontName /CMR10 /Flags 4 /FontFile %d 0 R >>", file))
			return fmt.Sprintf("<< /Type /Font /Subtype /Type1 /BaseFont /XYZABC+CMR10 /FontDescriptor %d 0 R >>", desc)
		})
		// 11, 12, 65 and 123 come from the program; 66 falls back to the
		// Standard encoding.
		if text, _ := decode(fnt, []byte{11, 12, 65, 123, 66}); text != "fffiA"+string(rune(0x2013))+"B" {
			t.Errorf("text = %q", text)
		}
		if !fnt.Symbolic {
			t.Error("descriptor flags not read")
		}
	})

	t.Run("Type 3 widths use the font matrix", func(t *testing.T) {
		fnt := load(t, func(b *pdftest.Builder) string {
			return `<< /Type /Font /Subtype /Type3 /FontMatrix [0.01 0 0 0.01 0 0] /FirstChar 65 /LastChar 65 /Widths [50]
				/Encoding << /Differences [65 /A] >> >>`
		})
		if text, w := decode(fnt, []byte{65, 66}); text != "A"+string(rune(0xFFFD)) || !near(w, 0.5) {
			t.Errorf("text %q width %v", text, w)
		}
	})
}

func TestToUnicodeRules(t *testing.T) {
	const cm = `1 begincodespacerange <00> <FF> endcodespacerange
3 beginbfchar <41> <0058> <42> <FB03> <43> <0000> endbfchar`

	fnt := load(t, func(b *pdftest.Builder) string {
		tu := b.AddStream("<< >>", []byte(cm))
		return fmt.Sprintf("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding /ToUnicode %d 0 R >>", tu)
	})
	// 0x41: ToUnicode wins over the encoding. 0x42: its ligature is expanded.
	// 0x43: a mapping to U+0000 is no mapping, so the encoding applies.
	// 0x44: not in the ToUnicode map, so the encoding applies.
	if text, _ := decode(fnt, []byte{0x41, 0x42, 0x43, 0x44}); text != "XfflCD"[:1]+"ffi"+"CD" {
		t.Errorf("text = %q", text)
	}

	// With no other source, a code the ToUnicode map leaves out produces no
	// text, and is reported as missing instead of unmapped.
	bare := load(t, func(b *pdftest.Builder) string {
		tu := b.AddStream("<< >>", []byte(cm))
		return fmt.Sprintf("<< /Type /Font /Subtype /Type3 /ToUnicode %d 0 R >>", tu)
	})
	g := bare.Next([]byte{0x50})
	if !g.Mapped || !g.Missing || g.Text != "" || bare.Unmapped != 0 {
		t.Errorf("glyph outside the map = %+v, Unmapped %d", g, bare.Unmapped)
	}
	if g := bare.Next([]byte{0x41}); g.Text != "X" || g.Missing {
		t.Errorf("glyph inside the map = %+v", g)
	}

	// /ToUnicode /Identity-H: the codes are the characters.
	identity := load(t, func(b *pdftest.Builder) string {
		desc := b.Add("<< /Type /Font /Subtype /CIDFontType2 /BaseFont /T /CIDSystemInfo << /Registry (Adobe) /Ordering (Identity) /Supplement 0 >> /DW 500 >>")
		return fmt.Sprintf("<< /Type /Font /Subtype /Type0 /BaseFont /T /Encoding /Identity-H /ToUnicode /Identity-H /DescendantFonts [%d 0 R] >>", desc)
	})
	if text, w := decode(identity, []byte{0x01, 0x41, 0x00, 0x42}); text != string(rune(0x141))+"B" || !near(w, 1.0) {
		t.Errorf("identity ToUnicode: text %q width %v", text, w)
	}
}

func TestCompositeFonts(t *testing.T) {
	t.Run("Identity-H with ToUnicode and widths", func(t *testing.T) {
		fnt := load(t, func(b *pdftest.Builder) string {
			tu := b.AddStream("<< >>", []byte("1 begincodespacerange <0000> <FFFF> endcodespacerange 1 beginbfrange <0003> <0005> <0041> endbfrange"))
			cid := b.Add(`<< /Type /Font /Subtype /CIDFontType2 /BaseFont /ABCDEF+Noto-Bold /DW 1000
				/CIDSystemInfo << /Registry (Adobe) /Ordering (Identity) /Supplement 0 >>
				/W [3 [600 700] 5 9 250] /FontDescriptor << /Type /FontDescriptor /Flags 64 /FontWeight 700 >> >>`)
			return fmt.Sprintf("<< /Type /Font /Subtype /Type0 /BaseFont /ABCDEF+Noto-Bold /Encoding /Identity-H /DescendantFonts [%d 0 R] /ToUnicode %d 0 R >>", cid, tu)
		})
		text, width := decode(fnt, []byte{0, 3, 0, 4, 0, 5, 0, 9, 0, 10})
		// CID 9 has a width but no text; CID 10 has neither and takes /DW.
		if text != "ABC" || !near(width, 0.6+0.7+0.25+0.25+1.0) {
			t.Errorf("text %q width %v", text, width)
		}
		if !fnt.Bold || !fnt.Italic || fnt.Vertical || fnt.Name != "Noto-Bold" {
			t.Errorf("style: %+v", fnt)
		}
		// An odd trailing byte is still consumed.
		if g := fnt.Next([]byte{7}); g.Bytes != 1 {
			t.Errorf("trailing byte: %+v", g)
		}
	})

	t.Run("predefined legacy CMap", func(t *testing.T) {
		fnt := load(t, func(b *pdftest.Builder) string {
			cid := b.Add(`<< /Type /Font /Subtype /CIDFontType0 /BaseFont /Ryumin-Light
				/CIDSystemInfo << /Registry (Adobe) /Ordering (Japan1) /Supplement 2 >> /W [231 325 500] >>`)
			return fmt.Sprintf("<< /Type /Font /Subtype /Type0 /BaseFont /Ryumin-Light /Encoding /90ms-RKSJ-H /DescendantFonts [%d 0 R] >>", cid)
		})
		// Shift-JIS: a hiragana letter (2 bytes), then ASCII "A" (1 byte,
		// a half-width glyph whose CID has width 500).
		text, width := decode(fnt, []byte{0x82, 0xA0, 0x41})
		if text != string(rune(0x3042))+"A" || !near(width, 1.5) {
			t.Errorf("text %q width %v", text, width)
		}
	})

	t.Run("Unicode CMap and vertical writing", func(t *testing.T) {
		fnt := load(t, func(b *pdftest.Builder) string {
			cid := b.Add(`<< /Type /Font /Subtype /CIDFontType0 /BaseFont /Song /CIDSystemInfo << /Registry (Adobe) /Ordering (GB1) /Supplement 2 >> >>`)
			return fmt.Sprintf("<< /Type /Font /Subtype /Type0 /BaseFont /Song /Encoding /UniGB-UCS2-V /DescendantFonts [%d 0 R] >>", cid)
		})
		text, width := decode(fnt, []byte{0x55, 0x4A, 0x4E, 0x00})
		if text != string(rune(0x554A))+string(rune(0x4E00)) || !fnt.Vertical || !near(width, -2) {
			t.Errorf("text %q width %v vertical %v", text, width, fnt.Vertical)
		}
	})

	t.Run("Identity-H with a known collection and no ToUnicode", func(t *testing.T) {
		fnt := load(t, func(b *pdftest.Builder) string {
			cid := b.Add(`<< /Type /Font /Subtype /CIDFontType0 /BaseFont /Kozuka /CIDSystemInfo << /Registry (Adobe) /Ordering (Japan1) /Supplement 6 >> >>`)
			return fmt.Sprintf("<< /Type /Font /Subtype /Type0 /BaseFont /Kozuka /Encoding /Identity-H /DescendantFonts [%d 0 R] >>", cid)
		})
		// CID 843 is HIRAGANA LETTER A in Adobe-Japan1.
		if text, _ := decode(fnt, []byte{0x03, 0x4B}); text != string(rune(0x3042)) {
			t.Errorf("text = %q", text)
		}
	})

	t.Run("embedded encoding CMap", func(t *testing.T) {
		fnt := load(t, func(b *pdftest.Builder) string {
			enc := b.AddStream("<< /Type /CMap /CMapName /Custom /WMode 0 >>", []byte(
				"1 begincodespacerange <00> <7F> endcodespacerange 1 begincodespacerange <8000> <FFFF> endcodespacerange "+
					"2 begincidrange <41> <42> 843 <8000> <8001> 844 endcidrange"))
			cid := b.Add(`<< /Type /Font /Subtype /CIDFontType0 /BaseFont /X /CIDSystemInfo << /Registry (Adobe) /Ordering (Japan1) /Supplement 6 >> /W [843 [300]] >>`)
			return fmt.Sprintf("<< /Type /Font /Subtype /Type0 /BaseFont /X /Encoding %d 0 R /DescendantFonts [%d 0 R] >>", enc, cid)
		})
		// One-byte 0x41 is CID 843; two-byte 0x8000 is CID 844.
		text, width := decode(fnt, []byte{0x41, 0x80, 0x00})
		if text != string(rune(0x3042))+string(rune(0x3043)) || !near(width, 0.3+1.0) {
			t.Errorf("text %q width %v", text, width)
		}
	})

	t.Run("nothing to go on", func(t *testing.T) {
		fnt := load(t, func(b *pdftest.Builder) string {
			return `<< /Type /Font /Subtype /Type0 /BaseFont /Mystery /Encoding /NoSuchCMap-H >>`
		})
		text, _ := decode(fnt, []byte{0, 1, 0, 2})
		if text != string(rune(0xFFFD))+string(rune(0xFFFD)) || fnt.Unmapped != 2 {
			t.Errorf("text %q, Unmapped %d", text, fnt.Unmapped)
		}
	})
}

func TestCache(t *testing.T) {
	b := pdftest.New()
	num := b.Add(`<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>`)
	notFont := b.Add(`(a string)`)
	b.Pages(pdftest.PageSpec{})
	data := b.Bytes()
	f, _ := reader.Open(ctx, bytes.NewReader(data), int64(len(data)), "", reader.Limits{})
	c := NewCache(f)
	first := c.Get(ctx, pdfobj.Ref{Num: num})
	if first == nil || c.Get(ctx, pdfobj.Ref{Num: num}) != first {
		t.Error("an indirect font is not reused")
	}
	if c.Get(ctx, pdfobj.Ref{Num: notFont}) != nil || c.Get(ctx, pdfobj.Ref{Num: 999}) != nil || c.Get(ctx, nil) != nil {
		t.Error("a non-font yielded a font")
	}
	direct := c.Get(ctx, pdftest.Parse(`<< /Type /Font /Subtype /Type1 /BaseFont /Courier >>`))
	if direct == nil || !direct.Fixed || len(c.All()) != 1 {
		t.Errorf("direct font: %+v; cached %d", direct, len(c.All()))
	}
}

func TestType1EncodingParser(t *testing.T) {
	for _, in := range []string{
		"", "/Encoding", "/Encoding StandardEncoding def", "/Encoding 256 array dup", "/Encoding dup 999 /A put dup x /B put def",
		"/Encoding 256 array dup 65 /A put", "no encoding here at all",
	} {
		type1Encoding([]byte(in)) // must not panic
	}
	got, ok := type1Encoding([]byte("/Encoding 256 array dup 65 /A put dup 66 /B put readonly def dup 67 /C put"))
	if !ok || len(got) != 2 || got[65] != "A" || got[66] != "B" {
		t.Errorf("got %v, %v", got, ok)
	}
	if _, ok := type1Encoding([]byte("/Encoding StandardEncoding def")); ok {
		t.Error("StandardEncoding should report no custom encoding")
	}
}

func TestClean(t *testing.T) {
	r := func(codes ...rune) string { return string(codes) }
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"plain", "plain", true},
		{r(0xFB01), "fi", true},
		{r('o', 0xFB03, 'c', 'e'), "office", true},
		{r(0xFE8D), r(0x0627), true},                 // alef, isolated
		{r(0xFE8E), r(0x0627), true},                 // alef, final
		{r(0xFE91), r(0x0628), true},                 // beh, initial
		{r(0xFEDF), r(0x0644), true},                 // lam, initial
		{r(0xFEF2), r(0x064A), true},                 // yeh, final
		{r(0xFEF4), r(0x064A), true},                 // yeh, medial: the last letter form
		{r(0xFEFB), r(0x0644, 0x0627), true},         // lam-alef ligature
		{r(0xFEF5), r(0x0644, 0x0622), true},         // lam-alef with madda
		{r(0xFE80), r(0x0621), true},                 // hamza
		{r(0xFE76), r(0x064E), true},                 // fatha
		{r(0xFE77), r(0x0640, 0x064E), true},         // fatha on a tatweel
		{r(0xFB58), r(0x067E), true},                 // peh, initial
		{r(0xFB8B), r(0x0698), true},                 // jeh, final
		{r(0x05D0, 0x05D1), r(0x05D0, 0x05D1), true}, // Hebrew is left alone
		{"", "", false},
		{r(0), "", false},
		{r('a', 0xFFFD), "", false},
	}
	for _, tt := range tests {
		got, ok := clean(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("clean(%+q) = %+q, %v; want %+q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}
