// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package unpdf

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/gomcpgo/unpdf/internal/layout"
	"github.com/gomcpgo/unpdf/internal/pdftest"
)

func blocksOf(t *testing.T, content, extra string) []Block {
	t.Helper()
	b := pdftest.New()
	f1, f2 := b.Add(helvetica), b.Add(courier)
	b.Pages(pdftest.PageSpec{Content: content, Resources: fmt.Sprintf("<< /Font << /F1 %d 0 R /F2 %d 0 R >> >>", f1, f2), Extra: extra})
	doc := openBytes(t, b.Bytes(), Options{})
	p, err := doc.Page(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	blocks, err := p.Blocks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return blocks
}

func closeTo(a, b Box) bool {
	return math.Abs(a.X0-b.X0)+math.Abs(a.Y0-b.Y0)+math.Abs(a.X1-b.X1)+math.Abs(a.Y1-b.Y1) < 1e-6
}

func TestBlocks(t *testing.T) {
	// Courier at 10 points: every glyph is 6 points wide.
	blocks := blocksOf(t, "BT /F2 10 Tf 72 700 Td (Hello world) Tj 0 -12 Td (again) Tj ET BT /F1 20 Tf 72 600 Td (Next) Tj ET", "")
	if len(blocks) != 2 || len(blocks[0].Lines) != 2 || len(blocks[0].Lines[0].Words) != 2 {
		t.Fatalf("blocks = %+v", blocks)
	}
	w := blocks[0].Lines[0].Words[0]
	if w.Text != "Hello" || w.Font != "Courier" || w.Size != 10 || w.Bold || w.Italic {
		t.Errorf("word = %+v", w)
	}
	// The page is 792 high: a baseline at 700 is 92 from the top.
	if want := (Box{72, 92 - 8, 72 + 30, 92 + 2}); !closeTo(w.Box, want) {
		t.Errorf("word box = %+v, want %+v", w.Box, want)
	}
	if want := (Box{72, 84, 72 + 66, 106}); !closeTo(blocks[0].Box, want) {
		t.Errorf("block box = %+v, want %+v", blocks[0].Box, want)
	}
	if blocks[0].Lines[0].Text() != "Hello world" || blocks[0].Text() != "Hello world\nagain" || blocks[1].Text() != "Next" {
		t.Errorf("texts: %q, %q", blocks[0].Text(), blocks[1].Text())
	}
	if blocks[0].Angle != 0 || blocks[0].Furniture || blocks[0].Lines[0].RTL {
		t.Errorf("flags: %+v", blocks[0])
	}
}

func TestBlocksFollowCropAndRotation(t *testing.T) {
	const text = "BT /F2 10 Tf 100 300 Td (AB) Tj ET"
	tests := []struct {
		extra string
		want  Box
		angle int
	}{
		// The crop box moves the origin: 100,300 is 80 from the left and
		// 400 from the top of a box 20..420 by 100..700.
		{"/CropBox [20 100 420 700]", Box{80, 392, 92, 402}, 0},
		// Rotated a quarter turn clockwise, the text reads down the page.
		{"/Rotate 90", Box{298, 100, 308, 112}, 90},
		// Upside down.
		{"/Rotate 180", Box{500, 298, 512, 308}, 180},
		{"/Rotate 270", Box{484, 500, 494, 512}, 270},
	}
	for _, tt := range tests {
		blocks := blocksOf(t, text, tt.extra)
		if len(blocks) != 1 {
			t.Fatalf("%s: %d blocks", tt.extra, len(blocks))
		}
		if got := blocks[0].Lines[0].Words[0].Box; !closeTo(got, tt.want) || blocks[0].Angle != tt.angle {
			t.Errorf("%s: box %+v angle %d, want %+v angle %d", tt.extra, got, blocks[0].Angle, tt.want, tt.angle)
		}
	}

	// A sideways page whose text is drawn sideways too reads upright.
	blocks := blocksOf(t, "BT /F2 10 Tf 0 1 -1 0 300 100 Tm (AB) Tj ET", "/Rotate 90")
	if blocks[0].Angle != 0 || blocks[0].Text() != "AB" {
		t.Errorf("rotated page and text: angle %d, %q", blocks[0].Angle, blocks[0].Text())
	}
}

func TestHiddenAndClippedText(t *testing.T) {
	// Invisible text beside visible text is dropped.
	_, got := textOf(t, "BT /F1 12 Tf 72 700 Td (visible words here) Tj 3 Tr 0 -14 Td (hidden) Tj ET", "", nil)
	if got != "visible words here" {
		t.Errorf("hidden text beside visible: %q", got)
	}
	// Text outside the clipping path is dropped; the clip ends with Q.
	_, got = textOf(t, "q 50 650 300 100 re W n BT /F1 12 Tf 72 700 Td (inside) Tj ET BT /F1 12 Tf 72 400 Td (outside) Tj ET Q "+
		"BT /F1 12 Tf 72 300 Td (after) Tj ET", "", nil)
	if got != "inside\n\nafter" {
		t.Errorf("clipping: %q", got)
	}
}

func TestScanned(t *testing.T) {
	image := func(b *pdftest.Builder) string {
		img := b.AddStream("<< /Type /XObject /Subtype /Image /Width 1 /Height 1 /ColorSpace /DeviceGray /BitsPerComponent 8 >>", []byte{0})
		return fmt.Sprintf("/Im %d 0 R", img)
	}
	const fullPage = "q 612 0 0 792 0 0 cm /Im Do Q "
	const strips = "q 612 0 0 400 0 392 cm /Im Do Q q 612 0 0 392 0 0 cm /Im Do Q "
	const thumbnail = "q 100 0 0 100 50 50 cm /Im Do Q "
	visible := "BT /F1 12 Tf 72 700 Td (" + strings.Repeat("plenty of visible text ", 4) + ") Tj ET "
	ocr := "BT 3 Tr /F1 12 Tf 72 700 Td (text recognised from the scan) Tj ET "
	tests := []struct {
		name, content string
		scanned       bool
		text          string
	}{
		{"an image and nothing else", fullPage, true, ""},
		{"an image in two strips", strips, true, ""},
		{"an image with an OCR layer", fullPage + ocr, true, "text recognised from the scan"},
		{"an image with a visible stamp", fullPage + "BT /F1 12 Tf 500 20 Td (Page 3) Tj ET", true, "Page 3"},
		{"an image behind real text", fullPage + visible, false, strings.TrimSpace(strings.Repeat("plenty of visible text ", 4))},
		{"a small image", thumbnail, false, ""},
		{"text only", visible, false, strings.TrimSpace(strings.Repeat("plenty of visible text ", 4))},
		{"an empty page", "", false, ""},
	}
	for _, tt := range tests {
		b := pdftest.New()
		f1 := b.Add(helvetica)
		b.Pages(pdftest.PageSpec{Content: tt.content, Resources: fmt.Sprintf("<< /Font << /F1 %d 0 R >> /XObject << %s >> >>", f1, image(b))})
		doc := openBytes(t, b.Bytes(), Options{})
		p, _ := doc.Page(ctx, 1)
		scanned, err := p.Scanned(ctx)
		if err != nil || scanned != tt.scanned {
			t.Errorf("%s: Scanned = %v, %v; want %v", tt.name, scanned, err, tt.scanned)
		}
		if text, _ := p.Text(ctx); text != tt.text {
			t.Errorf("%s: text = %q, want %q", tt.name, text, tt.text)
		}
	}
}

// book builds a document whose pages carry running heads and page numbers.
func book(pages int, head func(n int) string) []byte {
	b := pdftest.New()
	f1 := b.Add(helvetica)
	var specs []pdftest.PageSpec
	for n := 1; n <= pages; n++ {
		content := fmt.Sprintf("BT /F1 9 Tf 72 760 Td (%s) Tj ET ", head(n)) +
			fmt.Sprintf("BT /F1 11 Tf 72 640 Td (Body text of page %d, which is different on) Tj 0 -14 Td (every page of the book.) Tj ET ", n) +
			fmt.Sprintf("BT /F1 9 Tf 300 30 Td (Page %d) Tj ET", n)
		specs = append(specs, pdftest.PageSpec{Content: content, Resources: fmt.Sprintf("<< /Font << /F1 %d 0 R >> >>", f1)})
	}
	b.Pages(specs...)
	return b.Bytes()
}

func TestFurniture(t *testing.T) {
	// Chapter titles that change half way, and alternate between left and
	// right pages.
	head := func(n int) string {
		chapter := "Chapter One"
		if n > 6 {
			chapter = "Chapter Two"
		}
		if n%2 == 0 {
			return "The Book Title"
		}
		return chapter
	}
	doc := openBytes(t, book(12, head), Options{})
	for p, err := range doc.Pages(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		blocks, err := p.Blocks(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var furniture, body []string
		for _, b := range blocks {
			if b.Furniture {
				furniture = append(furniture, b.Text())
			} else {
				body = append(body, b.Text())
			}
		}
		wantBody := fmt.Sprintf("Body text of page %d, which is different on\nevery page of the book.", p.Number)
		if len(body) != 1 || body[0] != wantBody {
			t.Errorf("page %d: body = %q", p.Number, body)
		}
		// Both the head and the page number are found on every page,
		// including the first and last pages of each chapter.
		if len(furniture) != 2 || furniture[0] != head(p.Number) || furniture[1] != fmt.Sprintf("Page %d", p.Number) {
			t.Errorf("page %d: furniture = %q, want the head and the page number", p.Number, furniture)
		}
		// Text keeps everything; the flag is for the caller to act on.
		if text, _ := p.Text(ctx); !strings.Contains(text, head(p.Number)) || !strings.Contains(text, "Page ") {
			t.Errorf("page %d: text lost its furniture: %q", p.Number, text)
		}
	}

	// A page asked for on its own gives the same answer as in sequence.
	fresh := openBytes(t, book(12, head), Options{})
	p, _ := fresh.Page(ctx, 9)
	blocks, _ := p.Blocks(ctx)
	if len(blocks) != 3 || !blocks[0].Furniture || blocks[1].Furniture || !blocks[2].Furniture {
		t.Errorf("page 9 alone: %+v", blocks)
	}

	// Two pages are too few to call anything furniture.
	short := openBytes(t, book(2, head), Options{})
	for p := range short.Pages(ctx) {
		blocks, _ := p.Blocks(ctx)
		for _, b := range blocks {
			if b.Furniture {
				t.Errorf("two-page document: %q marked as furniture", b.Text())
			}
		}
	}
}

func TestSignature(t *testing.T) {
	top := func(text string, y float64) string {
		return signature(text, layout.Box{X0: 72, Y0: y, X1: 300, Y1: y + 10}, 792, 0)
	}
	// Page numbers do not matter, in digits or in roman numerals.
	if top("Page 12 of 300", 30) != top("Page 7 of 300", 30) || top("xiv", 30) != top("ii", 30) || top("12", 30) != top("xiv", 30) {
		t.Error("page numbers change the signature")
	}
	// Words and position do.
	if top("Chapter One", 30) == top("Chapter Two", 30) || top("Chapter One", 30) == top("Chapter One", 60) {
		t.Error("different lines share a signature")
	}
	// Only lines near an edge have one.
	if top("Body text", 400) != "" || top("   ", 30) != "" {
		t.Error("a line that cannot be furniture has a signature")
	}
	if signature("Foot", layout.Box{X0: 72, Y0: 760, X1: 300, Y1: 770}, 792, 0) == top("Foot", 22) {
		t.Error("top and bottom lines share a signature")
	}
}
