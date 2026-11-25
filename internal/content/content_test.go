// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package content

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/gomcpgo/unpdf/internal/font"
	"github.com/gomcpgo/unpdf/internal/pdfobj"
	"github.com/gomcpgo/unpdf/internal/pdftest"
	"github.com/gomcpgo/unpdf/internal/reader"
)

var ctx = context.Background()

// fixture is a document with two fonts, F1 (Helvetica) and F2 (Courier),
// both without widths so that the standard metrics apply, plus whatever
// extra objects a test adds.
type fixture struct {
	f   *reader.File
	res pdfobj.Dict
}

func newFixture(t *testing.T, extra func(b *pdftest.Builder) (xobjects string)) *fixture {
	t.Helper()
	b := pdftest.New()
	f1 := b.Add(`<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>`)
	f2 := b.Add(`<< /Type /Font /Subtype /Type1 /BaseFont /Courier /Encoding /WinAnsiEncoding >>`)
	xobjects := ""
	if extra != nil {
		xobjects = extra(b)
	}
	res := fmt.Sprintf("<< /Font << /F1 %d 0 R /F2 %d 0 R >> /XObject << %s >> /Properties << /P1 << /MCID 7 >> >> "+
		"/ExtGState << /G1 << /Font [%d 0 R 20] >> >> >>", f1, f2, xobjects, f2)
	b.Pages(pdftest.PageSpec{Resources: res})
	data := b.Bytes()
	f, err := reader.Open(ctx, bytes.NewReader(data), int64(len(data)), "", reader.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{f: f, res: pdftest.Parse(res).(pdfobj.Dict)}
}

func (fx *fixture) run(content string) ([]Glyph, []Image) {
	var glyphs []Glyph
	var images []Image
	in := New(fx.f, font.NewCache(fx.f), Sink{
		Glyph: func(g *Glyph) bool { glyphs = append(glyphs, *g); return true },
		Image: func(img Image) { images = append(images, img) },
	})
	in.Run(ctx, []byte(content), fx.res, Identity)
	return glyphs, images
}

func text(glyphs []Glyph) string {
	var b strings.Builder
	for _, g := range glyphs {
		b.WriteString(g.Text)
	}
	return b.String()
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func at(t *testing.T, g Glyph, wantText string, x, y float64) {
	t.Helper()
	if g.Text != wantText || !near(g.X, x) || !near(g.Y, y) {
		t.Errorf("glyph %q at (%.4f, %.4f); want %q at (%.4f, %.4f)", g.Text, g.X, g.Y, wantText, x, y)
	}
}

func TestTextPositioning(t *testing.T) {
	fx := newFixture(t, nil)

	// Courier glyphs are 0.6 em wide, which keeps the arithmetic exact.
	g, _ := fx.run("BT /F2 10 Tf 100 700 Td (AB) Tj ET")
	if len(g) != 2 {
		t.Fatalf("got %d glyphs", len(g))
	}
	at(t, g[0], "A", 100, 700)
	at(t, g[1], "B", 106, 700)
	if !near(g[0].DX, 6) || !near(g[0].DY, 0) || !near(g[0].Size, 10) || g[0].Font.Name != "Courier" {
		t.Errorf("first glyph: %+v", g[0])
	}

	// Td is relative to the start of the line, TD also sets the leading,
	// T* and the quote operators move down by it.
	g, _ = fx.run("BT /F2 10 Tf 100 700 Td (A) Tj 0 -12 TD (B) Tj T* (C) Tj (D) ' 1 2 (E F) \" ET")
	at(t, g[0], "A", 100, 700)
	at(t, g[1], "B", 100, 688)
	at(t, g[2], "C", 100, 676)
	at(t, g[3], "D", 100, 664)
	at(t, g[4], "E", 100, 652)
	at(t, g[5], " ", 108, 652)       // E: 6 wide plus 2 of character spacing
	at(t, g[6], "F", 108+6+2+1, 652) // space: 6 wide, plus character and word spacing

	// TJ numbers move back by thousandths of the font size.
	g, _ = fx.run("BT /F2 10 Tf 0 0 Td [(A) -500 (B) 1000 (C)] TJ ET")
	at(t, g[1], "B", 6+5, 0)
	at(t, g[2], "C", 6+5+6-10, 0)

	// Tm replaces the matrix; horizontal scaling stretches advances; text
	// rise lifts the origin.
	g, _ = fx.run("BT /F2 10 Tf 2 0 0 2 50 60 Tm 50 Tz 3 Ts (AB) Tj ET")
	at(t, g[0], "A", 50, 66)
	at(t, g[1], "B", 50+6, 66) // 6 wide, halved by Tz, doubled by Tm
	if !near(g[0].Size, 20) || !near(g[0].DX, 6) {
		t.Errorf("scaled glyph: size %v dx %v", g[0].Size, g[0].DX)
	}
}

func TestGraphicsState(t *testing.T) {
	fx := newFixture(t, nil)

	// cm applies to what follows; q and Q bracket it; the font is part of
	// the saved state.
	g, _ := fx.run("q 2 0 0 2 10 20 cm BT /F2 10 Tf 5 5 Td (A) Tj ET Q BT /F2 10 Tf 5 5 Td (B) Tj ET")
	at(t, g[0], "A", 20, 30)
	at(t, g[1], "B", 5, 5)
	if !near(g[0].Size, 20) || !near(g[0].DX, 12) {
		t.Errorf("under cm: size %v dx %v", g[0].Size, g[0].DX)
	}

	// Rotation by 90 degrees: the baseline runs up the page.
	g, _ = fx.run("BT /F2 10 Tf 0 1 -1 0 100 100 Tm (AB) Tj ET")
	at(t, g[1], "B", 100, 106)
	if !near(g[0].DX, 0) || !near(g[0].DY, 6) {
		t.Errorf("rotated advance: %v, %v", g[0].DX, g[0].DY)
	}

	// A font set through an ExtGState, rendering mode 3, unbalanced Q.
	g, _ = fx.run("Q Q /G1 gs BT 3 Tr (A) Tj 0 Tr (B) Tj ET")
	if len(g) != 2 || !g[0].Invisible || g[1].Invisible || !near(g[0].Size, 20) {
		t.Errorf("ExtGState font or render mode: %+v", g)
	}

	// No font selected, or an unknown one: nothing can be decoded.
	if g, _ := fx.run("BT (A) Tj /Nope 10 Tf (B) Tj ET"); len(g) != 0 {
		t.Errorf("text without a font produced %d glyphs", len(g))
	}
}

func TestFormXObjects(t *testing.T) {
	var self int
	fx := newFixture(t, func(b *pdftest.Builder) string {
		inner := b.AddStream("<< /Type /XObject /Subtype /Form /BBox [0 0 100 100] >>", []byte("BT /F2 10 Tf (in) Tj ET"))
		// The outer form has its own resources and a matrix, and calls the
		// inner form, which has none and so uses the outer form's.
		f3 := b.Add(`<< /Type /Font /Subtype /Type1 /BaseFont /Courier-Bold >>`)
		outer := b.AddStream(fmt.Sprintf("<< /Type /XObject /Subtype /Form /BBox [0 0 100 100] /Matrix [1 0 0 1 300 400] "+
			"/Resources << /Font << /F2 %d 0 R >> /XObject << /Inner %d 0 R >> >> >>", f3, inner),
			[]byte("BT /F2 10 Tf (out) Tj ET q 1 0 0 1 0 50 cm /Inner Do Q"))
		self = b.Reserve()
		b.SetStream(self, fmt.Sprintf("<< /Type /XObject /Subtype /Form /Resources << /XObject << /Self %d 0 R >> /Font << /F2 %d 0 R >> >> >>", self, f3),
			[]byte("BT /F2 10 Tf (x) Tj ET /Self Do"))
		img := b.AddStream("<< /Type /XObject /Subtype /Image /Width 1 /Height 1 >>", []byte{0})
		return fmt.Sprintf("/Outer %d 0 R /Self %d 0 R /Img %d 0 R", outer, self, img)
	})

	g, _ := fx.run("BT /F2 10 Tf 7 7 Td (a) Tj ET q 1 0 0 1 10 10 cm /Outer Do Q BT /F2 10 Tf (z) Tj ET")
	if text(g) != "aoutinz" {
		t.Fatalf("text = %q", text(g))
	}
	at(t, g[1], "o", 310, 410)
	at(t, g[4], "i", 310, 460)
	if g[1].Font.Name != "Courier-Bold" || g[4].Font.Name != "Courier-Bold" || g[0].Font.Name != "Courier" {
		t.Errorf("fonts: %s, %s, %s", g[0].Font.Name, g[1].Font.Name, g[4].Font.Name)
	}
	// The form does not disturb the caller's state.
	at(t, g[6], "z", 0, 0)

	// A form that draws itself is entered once.
	if g, _ := fx.run("/Self Do"); text(g) != "x" {
		t.Errorf("self-referential form: %q", text(g))
	}

	// An image is reported with its box under the current matrix.
	_, images := fx.run("q 200 0 0 100 50 60 cm /Img Do Q")
	if len(images) != 1 || images[0] != (Image{X0: 50, Y0: 60, X1: 250, Y1: 160}) {
		t.Errorf("images = %+v", images)
	}
}

func TestMarkedContent(t *testing.T) {
	fx := newFixture(t, nil)
	g, _ := fx.run(`BT /F2 10 Tf
		/P << /MCID 3 >> BDC (a) Tj EMC
		/P /P1 BDC (b) Tj /Span << /ActualText (fi) >> BDC (xy) Tj EMC (c) Tj EMC
		/Artifact BMC (h) Tj /Span << /ActualText <FEFF00E9> >> BDC (q) Tj EMC EMC
		/Span << /ActualText () >> BDC (gone) Tj EMC
		(d) Tj EMC EMC ET`)
	type want struct {
		text     string
		mcid     int
		artifact bool
		replaced bool
	}
	wants := []want{
		{"a", 3, false, false},
		{"b", 7, false, false},
		{"fi", 7, false, true}, {"", 7, false, true}, // "xy" is replaced by its ActualText
		{"c", 7, false, false},
		{"h", -1, true, false},
		{string(rune(0xE9)), -1, true, true},
		{"", -1, false, true}, {"", -1, false, true}, {"", -1, false, true}, {"", -1, false, true},
		{"d", -1, false, false},
	}
	if len(g) != len(wants) {
		t.Fatalf("got %d glyphs (%q), want %d", len(g), text(g), len(wants))
	}
	for i, w := range wants {
		if g[i].Text != w.text || g[i].MCID != w.mcid || g[i].Artifact != w.artifact || g[i].Replaced != w.replaced {
			t.Errorf("glyph %d = {%q mcid %d artifact %v replaced %v}, want %+v", i, g[i].Text, g[i].MCID, g[i].Artifact, g[i].Replaced, w)
		}
	}
}

func TestInlineImagesAndDamage(t *testing.T) {
	fx := newFixture(t, nil)

	// Image data that contains "EI" and brackets must not derail the parser.
	data := "BT /F2 10 Tf (a) Tj ET q 10 0 0 10 0 0 cm BI /W 2 /H 2 /BPC 8 /CS /G ID \x00EI\xff(]>> EI\x01\x02 EI Q BT /F2 10 Tf (b) Tj ET"
	g, images := fx.run(data)
	if text(g) != "ab" || len(images) != 1 || !images[0].Inline || images[0].X1 != 10 {
		t.Errorf("text %q, images %+v", text(g), images)
	}

	// Damage: operators without operands, stray brackets, a string that
	// never ends, an inline image that never ends.
	for _, in := range []string{
		"Tj TJ Td Tm cm Tf ' \" gs Do BDC EMC EMC Q", "BT /F2 10 Tf ) ] >> (ok) Tj ET", "BT /F2 10 Tf (ok) Tj (unterminated",
		"BT /F2 10 Tf (ok) Tj ET BI /W 1 ID \x00\x01\x02", "BT /F2 1e400 Tf 1e400 0 0 1e400 0 0 Tm (ok) Tj ET",
		"BT /F2 10 Tf [(ok) [nested] << /a 1 >> null] TJ ET", strings.Repeat("q ", 5000) + "BT /F2 10 Tf (ok) Tj ET",
		strings.Repeat("/X BMC ", 5000) + "BT /F2 10 Tf (ok) Tj ET",
	} {
		g, _ := fx.run(in)
		for _, gl := range g {
			if math.IsNaN(gl.X+gl.Y+gl.DX+gl.DY+gl.Size) || math.IsInf(gl.X+gl.Y+gl.DX+gl.DY+gl.Size, 0) {
				t.Errorf("%q: non-finite glyph %+v", in[:20], gl)
			}
		}
	}
	if g, _ := fx.run("BT /F2 10 Tf ) ] >> (ok) Tj ET"); text(g) != "ok" {
		t.Errorf("text after stray brackets = %q", text(g))
	}
}

func TestStoppingEarly(t *testing.T) {
	fx := newFixture(t, nil)
	n := 0
	in := New(fx.f, font.NewCache(fx.f), Sink{Glyph: func(*Glyph) bool { n++; return n < 3 }})
	if err := in.Run(ctx, []byte("BT /F2 10 Tf (abcdefgh) Tj [(ij)] TJ ET"), fx.res, Identity); err != nil || n != 3 {
		t.Errorf("stopped after %d glyphs, err %v", n, err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	in = New(fx.f, font.NewCache(fx.f), Sink{})
	if err := in.Run(cancelled, []byte(strings.Repeat("q Q ", 10000)), fx.res, Identity); err == nil {
		t.Error("a cancelled run returned no error")
	}
}

func TestMatrix(t *testing.T) {
	scale := Matrix{2, 0, 0, 3, 0, 0}
	move := Matrix{1, 0, 0, 1, 10, 20}
	// Scale then move: (1,1) -> (2,3) -> (12,23).
	if m := scale.Mul(move); m != (Matrix{2, 0, 0, 3, 10, 20}) {
		t.Errorf("scale then move = %v", m)
	}
	// Move then scale: (1,1) -> (11,21) -> (22,63).
	if m := move.Mul(scale); m != (Matrix{2, 0, 0, 3, 20, 60}) {
		t.Errorf("move then scale = %v", m)
	}
}

func FuzzRun(f *testing.F) {
	f.Add([]byte("BT /F1 12 Tf 72 720 Td (Hello) Tj [(a) -250 (b)] TJ T* (c) ' ET"))
	f.Add([]byte("q 1 0 0 1 5 5 cm /Outer Do Q /Self Do /Img Do"))
	f.Add([]byte("/P << /MCID 1 /ActualText (x) >> BDC BT /F2 9 Tf (y) Tj ET EMC BI /W 1 /H 1 ID \x00 EI"))
	f.Add([]byte("BT /F2 10 Tf 0 1 -1 0 9 9 Tm 3 Tr 50 Tz 2 Tc 1 Tw 4 Ts (x y) Tj ET"))
	b := pdftest.New()
	f1 := b.Add(`<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>`)
	cid := b.Add(`<< /Type /Font /Subtype /CIDFontType0 /CIDSystemInfo << /Registry (Adobe) /Ordering (Japan1) /Supplement 2 >> /W [1 [500 600] 10 20 300] >>`)
	f2 := b.Add(fmt.Sprintf("<< /Type /Font /Subtype /Type0 /Encoding /90ms-RKSJ-V /DescendantFonts [%d 0 R] >>", cid))
	self := b.Reserve()
	b.SetStream(self, fmt.Sprintf("<< /Subtype /Form /Resources << /XObject << /Self %d 0 R >> >> >>", self), []byte("/Self Do"))
	img := b.AddStream("<< /Subtype /Image >>", nil)
	res := fmt.Sprintf("<< /Font << /F1 %d 0 R /F2 %d 0 R >> /XObject << /Self %d 0 R /Outer %d 0 R /Img %d 0 R >> >>", f1, f2, self, self, img)
	b.Pages(pdftest.PageSpec{Resources: res})
	data := b.Bytes()
	file, err := reader.Open(ctx, bytes.NewReader(data), int64(len(data)), "", reader.Limits{})
	if err != nil {
		f.Fatal(err)
	}
	resources := pdftest.Parse(res).(pdfobj.Dict)
	cache := font.NewCache(file)
	f.Fuzz(func(t *testing.T, content []byte) {
		n := 0
		in := New(file, cache, Sink{
			Glyph: func(g *Glyph) bool {
				n++
				if math.IsNaN(g.X+g.Y+g.DX+g.DY+g.Size) || math.IsInf(g.X+g.Y+g.DX+g.DY+g.Size, 0) {
					t.Fatalf("non-finite glyph %+v", g)
				}
				return n < 10000
			},
			Image: func(Image) {},
		})
		in.Run(ctx, content, resources, Identity)
	})
}
