// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package unpdf

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gomcpgo/unpdf/internal/pdftest"
)

const (
	helveticaBold = `<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold /Encoding /WinAnsiEncoding >>`
	// filler is enough body text for the document to know its body size.
	filler = "BT /F1 10 Tf 72 300 Td (Body text that sets the usual size of the document, in several lines,) Tj " +
		"0 -12 Td (so that there is no doubt about which size is the size of ordinary paragraphs) Tj " +
		"0 -12 Td (and which sizes are used for the headings that stand above those paragraphs.) Tj " +
		"0 -12 Td (A fourth line makes this block too long to be taken for a heading itself.) Tj ET "
)

// structured builds a one-page document with F1 Helvetica and F2
// Helvetica-Bold, and returns its blocks without the filler paragraph.
func structured(t *testing.T, content string, more func(b *pdftest.Builder)) (*Document, []Block) {
	t.Helper()
	b := pdftest.New()
	f1, f2 := b.Add(helvetica), b.Add(helveticaBold)
	b.Pages(pdftest.PageSpec{Content: content + filler, Resources: fmt.Sprintf("<< /Font << /F1 %d 0 R /F2 %d 0 R >> >>", f1, f2)})
	if more != nil {
		more(b)
	}
	doc := openBytes(t, b.Bytes(), Options{})
	p, err := doc.Page(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	blocks, err := p.Blocks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var out []Block
	for _, blk := range blocks {
		if !strings.HasPrefix(blk.Text(), "Body text that sets") {
			out = append(out, blk)
		}
	}
	return doc, out
}

// describe renders blocks as "kind/level: text" for comparison.
func describe(blocks []Block) []string {
	var out []string
	for _, b := range blocks {
		tag := "p"
		switch b.Kind {
		case Heading:
			tag = fmt.Sprintf("h%d", b.Level)
		case ListItem:
			tag = fmt.Sprintf("li%d[%s]", b.Level, b.Marker)
		}
		out = append(out, tag+": "+strings.ReplaceAll(b.Text(), "\n", " / "))
	}
	return out
}

func sameStrings(t *testing.T, name string, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("%s:\n got %q\nwant %q", name, got, want)
	}
}

func TestHeadings(t *testing.T) {
	_, blocks := structured(t,
		"BT /F1 24 Tf 72 740 Td (The Title) Tj ET "+
			"BT /F1 16 Tf 72 700 Td (1. A numbered section) Tj ET "+
			"BT /F1 10 Tf 72 680 Td (A short paragraph.) Tj ET "+
			"BT /F1 16 Tf 72 650 Td (Another section) Tj ET "+
			"BT /F1 13 Tf 72 620 Td (A subsection) Tj ET "+
			"BT /F2 10 Tf 72 590 Td (A minor heading in bold) Tj ET "+
			"BT /F2 10 Tf 72 560 Td (A bold sentence that ends with a full stop.) Tj ET "+
			"BT /F2 10 Tf 72 530 Td (Region) Tj 200 0 Td (Units) Tj 100 0 Td (Share) Tj ET "+
			"BT /F1 16 Tf 72 500 Td (Chapter one . . . . . . . . 12) Tj ET "+
			"BT /F1 30 Tf 72 450 Td (7) Tj ET ", nil)
	sameStrings(t, "headings", describe(blocks), []string{
		"h1: The Title",
		"h2: 1. A numbered section", // a heading, although it starts like a list item
		"p: A short paragraph.",
		"h2: Another section",
		"h3: A subsection",
		"h4: A minor heading in bold",
		"p: A bold sentence that ends with a full stop.",
		"p: Region Units Share",             // a table's header row
		"p: Chapter one . . . . . . . . 12", // a line of a table of contents
		"p: 7",                              // a number is not a heading, however large
	})
}

func TestSecondBodySize(t *testing.T) {
	// A letter set in 12 point before accounts in 10 point: the 12 point
	// text is body text too, and its short paragraphs are not headings.
	letter := "BT /F1 12 Tf 72 740 Td (A paragraph of the letter, which is set larger than the accounts) Tj " +
		"0 -14 Td (that follow it, and runs to several lines as paragraphs do, so the) Tj " +
		"0 -14 Td (size it is set in is plainly a size for running text and not one) Tj " +
		"0 -14 Td (that is kept for headings, however much larger than the rest it is.) Tj ET " +
		"BT /F1 12 Tf 72 660 Td (A short paragraph of the same letter.) Tj ET " +
		"BT /F1 18 Tf 72 620 Td (A real heading) Tj ET "
	more := "BT /F1 10 Tf 72 240 Td (" + strings.Repeat("More of the accounts in the smaller size. ", 2) + ") Tj " +
		strings.Repeat("0 -12 Td (And more of them, line after line after line after line after line.) Tj ", 8) + "ET "
	_, blocks := structured(t, letter+more, nil)
	got := describe(blocks)
	if len(got) < 3 || !strings.HasPrefix(got[0], "p: A paragraph of the letter") || got[1] != "p: A short paragraph of the same letter." || got[2] != "h1: A real heading" {
		t.Errorf("blocks = %q", got)
	}
}

func TestLists(t *testing.T) {
	_, blocks := structured(t,
		"BT /F1 10 Tf 72 740 Td (An introduction to the list:) Tj "+
			"0 -12 Td (\\225 first item, which is long enough) Tj 10 -12 Td (to continue on a second line) Tj "+
			"-10 -12 Td (\\225 second item) Tj "+
			"20 -12 Td (- nested item) Tj 0 -12 Td (- another nested item) Tj "+
			"-20 -12 Td (\\225 third item) Tj ET "+
			"BT /F1 10 Tf 72 620 Td (1. step one) Tj 0 -12 Td (2. step two) Tj 0 -12 Td (10. step ten) Tj ET "+
			"BT /F1 10 Tf 72 560 Td (\\(a\\) lettered) Tj 0 -12 Td (b\\) also lettered) Tj ET "+
			"BT /F1 10 Tf 72 500 Td (A. Smith wrote this sentence.) Tj ET "+
			"BT /F1 10 Tf 72 470 Td (* * * * *) Tj ET "+
			"BT /F1 10 Tf 72 440 Td (2024. A year, not a list.) Tj ET "+
			"BT /F1 10 Tf 72 410 Td (-) Tj ET ", nil)
	bullet := string(rune(0x2022))
	sameStrings(t, "lists", describe(blocks), []string{
		"p: An introduction to the list:",
		"li0[" + bullet + "]: " + bullet + " first item, which is long enough / to continue on a second line",
		"li0[" + bullet + "]: " + bullet + " second item",
		"li1[-]: - nested item",
		"li1[-]: - another nested item",
		"li0[" + bullet + "]: " + bullet + " third item",
		"li0[1.]: 1. step one",
		"li0[2.]: 2. step two",
		"li0[10.]: 10. step ten",
		"li0[(a)]: (a) lettered",
		"li0[b)]: b) also lettered",
		"p: A. Smith wrote this sentence.", // an initial, with no list around it
		"p: * * * * *",                     // a separator
		"p: 2024. A year, not a list.",
		"p: -",
	})
}

func TestListMarker(t *testing.T) {
	tests := []struct {
		word                 string
		ordered, certain, ok bool
	}{
		{string(rune(0x2022)), false, true, true}, {"-", false, true, true}, {"*", false, true, true}, {string(rune(0x2013)), false, true, true},
		{"1.", true, true, true}, {"12)", true, true, true}, {"(3)", true, true, true}, {"999.", true, true, true},
		{"a)", true, true, true}, {"(b)", true, true, true}, {"A.", true, false, true}, {"iv.", true, false, true}, {"XII)", true, false, true},
		{"1000.", false, false, false}, {"1", false, false, false}, {"a", false, false, false}, {"(a", false, false, false},
		{"1.5", false, false, false}, {"e.g.", false, false, false}, {"word.", false, false, false}, {"--", false, false, false},
		{"", false, false, false}, {".", false, false, false}, {"()", false, false, false}, {"(1.", false, false, false},
	}
	for _, tt := range tests {
		ordered, certain, ok := listMarker(tt.word)
		if ordered != tt.ordered || certain != tt.certain || ok != tt.ok {
			t.Errorf("listMarker(%q) = %v, %v, %v; want %v, %v, %v", tt.word, ordered, certain, ok, tt.ordered, tt.certain, tt.ok)
		}
	}
}

func TestOutlineConfirmsHeadings(t *testing.T) {
	// Headings at body size and weight, which only the outline gives away.
	// One has a section number on the page that the outline leaves out.
	content := "BT /F1 10 Tf 72 740 Td (Introduction) Tj ET " +
		"BT /F1 10 Tf 72 700 Td (2.1 Method and materials) Tj ET " +
		"BT /F1 10 Tf 72 660 Td (Not in the outline) Tj ET "
	_, blocks := structured(t, content, func(b *pdftest.Builder) {
		root := b.Reserve()
		first, child := b.Reserve(), b.Reserve()
		// The page object is number 4: fonts are 1 and 2, the page tree 3.
		b.Set(first, fmt.Sprintf("<< /Title (Introduction) /Parent %d 0 R /Dest [5 0 R /Fit] /First %d 0 R /Last %d 0 R >>", root, child, child))
		b.Set(child, fmt.Sprintf("<< /Title (Method and Materials) /Parent %d 0 R /Dest [5 0 R /XYZ 0 700 0] >>", first))
		b.Set(root, fmt.Sprintf("<< /Type /Outlines /First %d 0 R /Last %d 0 R >>", first, first))
		b.Set(b.Root, fmt.Sprintf("<< /Type /Catalog /Pages 3 0 R /Outlines %d 0 R >>", root))
	})
	sameStrings(t, "outline headings", describe(blocks), []string{
		"h1: Introduction", "h2: 2.1 Method and materials", "p: Not in the outline",
	})
}

func TestBlockKindString(t *testing.T) {
	if Paragraph.String() != "paragraph" || Heading.String() != "heading" || ListItem.String() != "list item" {
		t.Error("BlockKind names changed")
	}
}
