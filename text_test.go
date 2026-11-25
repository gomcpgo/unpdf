// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package unpdf

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gomcpgo/unpdf/internal/pdftest"
)

const (
	helvetica = `<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>`
	courier   = `<< /Type /Font /Subtype /Type1 /BaseFont /Courier /Encoding /WinAnsiEncoding >>`
)

// textOf builds a one-page document with fonts F1 (Helvetica) and F2
// (Courier) and returns the document and the text of its page.
func textOf(t *testing.T, content, extra string, more func(b *pdftest.Builder) string) (*Document, string) {
	t.Helper()
	b := pdftest.New()
	f1, f2 := b.Add(helvetica), b.Add(courier)
	if more != nil {
		extra += " " + more(b)
	}
	b.Pages(pdftest.PageSpec{
		Content:   content,
		Resources: fmt.Sprintf("<< /Font << /F1 %d 0 R /F2 %d 0 R >> >>", f1, f2),
		Extra:     extra,
	})
	doc := openBytes(t, b.Bytes(), Options{})
	p, err := doc.Page(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	text, err := p.Text(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return doc, text
}

func TestTextFromTestdata(t *testing.T) {
	tests := map[string][]string{
		"testdata/classic.pdf": {
			"1 First page\nThis file has a classic cross-reference table. The quick brown fox jumps over the\nlazy dog.\n1",
			"2 Second page\nThe second page of the classic test file.\n2",
		},
		"testdata/compressed.pdf": {
			"1 First page\nThis file has a cross-reference stream and keeps most objects in object streams.\n1",
			"2 Second page\nSphinx of black quartz, judge my vow.\n2",
			"3 Third page\nThe third page of the compressed test file.\n3",
		},
	}
	for file, want := range tests {
		doc, err := OpenFile(ctx, file, Options{})
		if err != nil {
			t.Fatal(err)
		}
		i := 0
		for p, err := range doc.Pages(ctx) {
			if err != nil {
				t.Fatal(err)
			}
			got, err := p.Text(ctx)
			if err != nil || got != want[i] {
				t.Errorf("%s page %d:\n got %q\nwant %q (err %v)", file, p.Number, got, want[i], err)
			}
			i++
		}
		if ws := doc.Warnings(); len(ws) != 0 {
			t.Errorf("%s: warnings %v", file, ws)
		}
		doc.Close()
	}
}

func TestWordsAndLines(t *testing.T) {
	tests := []struct {
		name, content, want string
	}{
		{"space characters", "BT /F1 12 Tf 72 700 Td (Hello, world) Tj ET", "Hello, world"},
		{"words split by positioning", "BT /F1 12 Tf 72 700 Td [(Hello,) -300 (world)] TJ ET", "Hello, world"},
		{"kerning does not split a word", "BT /F1 12 Tf 72 700 Td [(A) 80 (V) -30 (A)] TJ ET", "AVA"},
		{"a new baseline is a new line", "BT /F1 12 Tf 72 700 Td (one) Tj 0 -14 Td (two) Tj ET", "one\ntwo"},
		{"separate text objects on one line", "BT /F1 12 Tf 72 700 Td (left) Tj ET BT /F1 12 Tf 200 700 Td (right) Tj ET", "left right"},
		{"going back along the line", "BT /F1 12 Tf 300 700 Td (second) Tj ET BT /F1 12 Tf 72 700 Td (first) Tj ET", "second\nfirst"},
		{"a superscript stays on its line", "BT /F1 12 Tf 72 700 Td (E=mc) Tj 4 Ts /F1 8 Tf (2) Tj ET", "E=mc2"},
		{"no double spaces", "BT /F1 12 Tf 72 700 Td [(a ) -500 ( b)] TJ ET", "a  b"},
		{"rotated text reads along its baseline", "BT /F1 12 Tf 0 1 -1 0 100 100 Tm [(up) -400 (the)] TJ 0 -14 Td (page) Tj ET", "up the\npage"},
		{"empty page", "", ""},
	}
	for _, tt := range tests {
		if _, got := textOf(t, tt.content, "", nil); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestWhatIsNotText(t *testing.T) {
	// Text outside the crop box is not on the page as displayed.
	_, got := textOf(t, "BT /F1 12 Tf 100 700 Td (visible) Tj ET BT /F1 12 Tf 100 20 Td (trimmed) Tj ET BT /F1 12 Tf 700 700 Td (outside) Tj ET",
		"/CropBox [50 50 562 742]", nil)
	if got != "visible" {
		t.Errorf("crop box: got %q", got)
	}

	// A word drawn twice, slightly offset, to look bold is one word. The
	// same letters elsewhere are untouched, at any font size.
	_, got = textOf(t, "BT /F1 12 Tf 100 700 Td (Bold) Tj ET BT /F1 12 Tf 100.3 700 Td (Bold) Tj ET "+
		"BT /F1 12 Tf 100 650 Td (Bold eee) Tj ET BT /F1 40 Tf 100 600 Td (Bee) Tj ET BT /F1 4 Tf 100 590 Td (Bee) Tj ET", "", nil)
	if got != "Bold\nBold eee\nBee\nBee" {
		t.Errorf("duplicates: got %q", got)
	}
}

func TestUnmappedGlyphs(t *testing.T) {
	doc, got := textOf(t, "BT /F3 12 Tf 72 700 Td (ABC) Tj ET BT /F4 12 Tf 72 680 Td (AB) Tj ET", "", func(b *pdftest.Builder) string {
		// F3 knows only A. F4 has a ToUnicode map with only A in it.
		f3 := b.Add(`<< /Type /Font /Subtype /Type3 /BaseFont /Odd /Encoding << /Differences [65 /A /g1 /g2] >> /FirstChar 65 /LastChar 67 /Widths [500 500 500] /FontMatrix [0.001 0 0 0.001 0 0] >>`)
		tu := b.AddStream("<< >>", []byte("1 beginbfchar <41> <0041> endbfchar"))
		f4 := b.Add(fmt.Sprintf(`<< /Type /Font /Subtype /Type3 /BaseFont /Shaped /ToUnicode %d 0 R /FirstChar 65 /LastChar 66 /Widths [500 500] /FontMatrix [0.001 0 0 0.001 0 0] >>`, tu))
		return fmt.Sprintf("/Resources << /Font << /F3 %d 0 R /F4 %d 0 R >> >>", f3, f4)
	})
	if got != "A"+strings.Repeat(string(rune(0xFFFD)), 2)+"\nA" {
		t.Errorf("text = %q", got)
	}
	var codes []string
	for _, w := range doc.Warnings() {
		codes = append(codes, w.Code+": "+w.Message)
	}
	want := []string{
		"unmapped-glyphs: font Odd: 2 glyphs on 1 pages have no Unicode mapping and appear as U+FFFD",
		"glyphs-without-text: font Shaped: 1 glyphs on 1 pages are left out of its ToUnicode map and produce no text",
	}
	if strings.Join(codes, "|") != strings.Join(want, "|") {
		t.Errorf("warnings:\n got %q\nwant %q", codes, want)
	}
	// Reading the page again must not double the counts.
	p, _ := doc.Page(ctx, 1)
	p.Text(ctx)
	if ws := doc.Warnings(); len(ws) != 2 || !strings.Contains(ws[0].Message, "2 glyphs on 1 pages") {
		t.Errorf("after a second read: %v", ws)
	}
}

func TestFormFieldsAndAnnotations(t *testing.T) {
	_, got := textOf(t, "BT /F1 12 Tf 72 700 Td (Name:) Tj ET", "", func(b *pdftest.Builder) string {
		// A text field with an appearance stream, fitted to its rectangle.
		ap := b.AddStream("<< /Type /XObject /Subtype /Form /BBox [0 0 100 20] /Resources << /Font << /F1 << /Type /Font /Subtype /Type1 /BaseFont /Helvetica >> >> >> >>",
			[]byte("/Tx BMC BT /F1 10 Tf 2 5 Td (Alice) Tj ET EMC"))
		filled := b.Add(fmt.Sprintf("<< /Type /Annot /Subtype /Widget /FT /Tx /T (name) /V (Alice) /Rect [120 695 220 715] /AP << /N %d 0 R >> >>", ap))
		// A text field with a value and no appearance.
		bare := b.Add("<< /Type /Annot /Subtype /Widget /Parent << /FT /Tx /V <FEFF0042006F0062> >> /Rect [120 660 220 680] >>")
		// A checkbox: its state is a name, not text.
		box := b.Add("<< /Type /Annot /Subtype /Widget /FT /Btn /V /Yes /Rect [120 630 130 640] >>")
		// A hidden field, a link, and a free-text note with an appearance.
		hidden := b.Add(fmt.Sprintf("<< /Type /Annot /Subtype /Widget /FT /Tx /V (secret) /F 2 /Rect [120 600 220 620] /AP << /N %d 0 R >> >>", ap))
		link := b.Add("<< /Type /Annot /Subtype /Link /Rect [0 0 10 10] /A << /S /URI /URI (https://example.com) >> >>")
		noteAP := b.AddStream("<< /Type /XObject /Subtype /Form /BBox [0 0 100 20] /Resources << /Font << /F1 << /Type /Font /Subtype /Type1 /BaseFont /Helvetica >> >> >> >>",
			[]byte("BT /F1 10 Tf 2 5 Td (a note) Tj ET"))
		note := b.Add(fmt.Sprintf("<< /Type /Annot /Subtype /FreeText /Rect [300 500 400 520] /AP << /N << /On %d 0 R /Off %d 0 R >> >> /AS /On >>", noteAP, ap))
		return fmt.Sprintf("/Annots [%d 0 R %d 0 R %d 0 R %d 0 R %d 0 R %d 0 R]", filled, bare, box, hidden, link, note)
	})
	if got != "Name: Alice\nBob\na note" {
		t.Errorf("text = %q", got)
	}
}

func TestTextLimitsAndCancellation(t *testing.T) {
	b := pdftest.New()
	f1 := b.Add(helvetica)
	b.Pages(pdftest.PageSpec{
		Content:   "BT /F1 1 Tf 72 700 Td (" + strings.Repeat("x", 500) + ") Tj ET",
		Resources: fmt.Sprintf("<< /Font << /F1 %d 0 R >> >>", f1),
	})
	data := b.Bytes()

	doc := openBytes(t, data, Options{Limits: Limits{MaxPageGlyphs: 100}})
	p, _ := doc.Page(ctx, 1)
	text, err := p.Text(ctx)
	if err != nil || len(text) != 100 {
		t.Errorf("limited text has %d characters, err %v", len(text), err)
	}
	found := false
	for _, w := range doc.Warnings() {
		found = found || w.Code == WarnLimit
	}
	if !found {
		t.Errorf("no limit warning in %v", doc.Warnings())
	}

	doc = openBytes(t, data, Options{})
	p, _ = doc.Page(ctx, 1)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := p.Text(cancelled); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: got %v", err)
	}
}

func TestContentInSeveralStreams(t *testing.T) {
	b := pdftest.New()
	f1 := b.Add(helvetica)
	// The content is split in the middle of a text object, and one of the
	// streams is unreadable.
	s1 := b.AddStream("<< >>", []byte("BT /F1 12 Tf 72 700 Td (first) Tj"))
	bad := b.AddStream("<< /Filter /FlateDecode >>", []byte("this is not deflate data"))
	s2 := b.AddStream("<< >>", []byte("0 -14 Td (second) Tj ET"))
	pages := b.Reserve()
	page := b.Add(fmt.Sprintf("<< /Type /Page /Parent %d 0 R /MediaBox [0 0 612 792] /Contents [%d 0 R %d 0 R %d 0 R] /Resources << /Font << /F1 %d 0 R >> >> >>", pages, s1, bad, s2, f1))
	b.Set(pages, fmt.Sprintf("<< /Type /Pages /Kids [%d 0 R] /Count 1 >>", page))
	b.Root = b.Add(fmt.Sprintf("<< /Type /Catalog /Pages %d 0 R >>", pages))
	doc := openBytes(t, b.Bytes(), Options{})
	p, _ := doc.Page(ctx, 1)
	if got, err := p.Text(ctx); err != nil || got != "first\nsecond" {
		t.Errorf("text = %q, err %v", got, err)
	}
	if ws := doc.Warnings(); len(ws) != 1 || ws[0].Code != WarnStreamData {
		t.Errorf("warnings = %v", ws)
	}
}
