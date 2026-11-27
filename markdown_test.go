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

// report builds a four-page document with running heads, headings, a
// paragraph with a hyphenated word and a link, a list, and an address.
func report(t *testing.T) *Document {
	t.Helper()
	b := pdftest.New()
	f1, f2 := b.Add(helvetica), b.Add(helveticaBold)
	res := fmt.Sprintf("<< /Font << /F1 %d 0 R /F2 %d 0 R >> >>", f1, f2)
	head := "BT /F1 8 Tf 72 770 Td (Annual Report) Tj ET BT /F1 8 Tf 500 770 Td (Page %d) Tj ET "
	body := "BT /F1 10 Tf 72 %d Td (This paragraph is long enough to be wrapped over several lines by the) Tj " +
		"0 -12 Td (program that made the file, and one of its words is split by hyphen-) Tj " +
		"0 -12 Td (ation at the end of a line, which the Markdown output puts right.) Tj ET "
	link := b.Add("<< /Type /Annot /Subtype /Link /Rect [72 596 160 608] /A << /S /URI /URI (https://example.com/a b) >> >>")

	page1 := fmt.Sprintf(head, 1) +
		"BT /F1 22 Tf 72 720 Td (Annual Report) Tj ET " +
		"BT /F1 15 Tf 72 690 Td (1. Overview) Tj ET " +
		fmt.Sprintf(body, 660) +
		"BT /F1 10 Tf 72 600 Td (See the full tables online.) Tj ET " +
		"BT /F1 10 Tf 72 570 Td (\\225 revenue rose) Tj 0 -12 Td (\\225 costs fell, and) Tj 10 -12 Td (margins widened) Tj -10 -12 Td (1. first step) Tj 0 -12 Td (2. second step) Tj ET " +
		"BT /F1 10 Tf 72 480 Td (Northwind Supply Co.) Tj 0 -12 Td (48 Harbour Road East) Tj 0 -12 Td (Portland, OR 97201) Tj ET " +
		"BT /F1 10 Tf 72 420 Td (# not a heading) Tj ET BT /F1 10 Tf 72 400 Td (2024. A year.) Tj ET "
	page2 := fmt.Sprintf(head, 2) + "BT /F1 15 Tf 72 700 Td (2. Details) Tj ET " + fmt.Sprintf(body, 670)
	page3 := fmt.Sprintf(head, 3) + fmt.Sprintf(body, 700)
	page4 := fmt.Sprintf(head, 4) + fmt.Sprintf(body, 700)
	b.Pages(
		pdftest.PageSpec{Content: page1, Resources: res, Extra: fmt.Sprintf("/Annots [%d 0 R]", link)},
		pdftest.PageSpec{Content: page2, Resources: res},
		pdftest.PageSpec{Content: page3, Resources: res},
		pdftest.PageSpec{Content: page4, Resources: res},
	)
	return openBytes(t, b.Bytes(), Options{})
}

const paragraphText = "This paragraph is long enough to be wrapped over several lines by the program that made the file, " +
	"and one of its words is split by hyphenation at the end of a line, which the Markdown output puts right."

func TestMarkdown(t *testing.T) {
	doc := report(t)
	got, err := doc.Markdown(ctx, MarkdownOptions{Pages: "1-2"})
	if err != nil {
		t.Fatal(err)
	}
	want := "<!-- page 1 -->\n\n" +
		"# Annual Report\n\n" +
		"## 1. Overview\n\n" +
		paragraphText + "\n\n" +
		"[See the full tables](<https://example.com/a b>) online.\n\n" +
		"- revenue rose\n" +
		"- costs fell, and margins widened\n" +
		"1. first step\n" +
		"2. second step\n\n" +
		"Northwind Supply Co.\n48 Harbour Road East\nPortland, OR 97201\n\n" +
		"\\# not a heading\n\n" +
		"2024\\. A year.\n" +
		"\n<!-- page 2 -->\n\n" +
		"## 2. Details\n\n" +
		paragraphText + "\n"
	if got != want {
		t.Errorf("Markdown:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}

	// With furniture, and without page markers.
	got, _ = doc.Markdown(ctx, MarkdownOptions{Pages: "3", Furniture: true, OmitPageMarkers: true})
	if want := "Annual Report Page 3\n\n" + paragraphText + "\n"; got != want {
		t.Errorf("with furniture: %q, want %q", got, want)
	}

	// Everything, to a limit: the output stops at a page boundary and says so.
	all, _ := doc.Markdown(ctx, MarkdownOptions{})
	if strings.Count(all, "<!-- page ") != 4 || strings.Contains(all, "Page 2") {
		t.Errorf("whole document: %d page markers", strings.Count(all, "<!-- page "))
	}
	limited, _ := doc.Markdown(ctx, MarkdownOptions{MaxBytes: 900})
	if !strings.HasSuffix(limited, "\n<!-- output limit reached: stopped before page 3 -->\n") || !strings.Contains(limited, "## 2. Details") {
		t.Errorf("limited output ends %q", limited[max(0, len(limited)-120):])
	}
	// A limit smaller than the first page still gives the first page.
	if tiny, _ := doc.Markdown(ctx, MarkdownOptions{MaxBytes: 1}); !strings.Contains(tiny, "# Annual Report") || !strings.Contains(tiny, "stopped before page 2") {
		t.Errorf("tiny limit: %q", tiny)
	}
}

type failingWriter struct{ after int }

func (w *failingWriter) Write(p []byte) (int, error) {
	if w.after -= len(p); w.after < 0 {
		return 0, errors.New("disk full")
	}
	return len(p), nil
}

func TestMarkdownErrors(t *testing.T) {
	doc := report(t)
	for _, pages := range []string{"0", "x", "3-1", "1,,2", "-", "1-2-3", "1.5"} {
		if _, err := doc.Markdown(ctx, MarkdownOptions{Pages: pages}); err == nil {
			t.Errorf("page list %q was accepted", pages)
		}
	}
	for _, pages := range []string{"", " 1 , 3 ", "2-", "-2", "4-9", "1,1,1"} {
		if _, err := doc.Markdown(ctx, MarkdownOptions{Pages: pages}); err != nil {
			t.Errorf("page list %q: %v", pages, err)
		}
	}
	if out, _ := doc.Markdown(ctx, MarkdownOptions{Pages: "99"}); out != "" {
		t.Errorf("a page past the end gave %q", out)
	}
	if err := doc.WriteMarkdown(ctx, &failingWriter{after: 50}, MarkdownOptions{}); err == nil || err.Error() != "disk full" {
		t.Errorf("write error: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := doc.Markdown(cancelled, MarkdownOptions{}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
}

func TestParsePages(t *testing.T) {
	selected, err := parsePages("1-3, 7, 10-")
	if err != nil {
		t.Fatal(err)
	}
	for n, want := range map[int]bool{1: true, 3: true, 4: false, 7: true, 8: false, 10: true, 5000: true, 0: false} {
		if selected(n) != want {
			t.Errorf("page %d selected = %v", n, !want)
		}
	}
}

func TestMarkdownOfScannedAndEmptyPages(t *testing.T) {
	b := pdftest.New()
	img := b.AddStream("<< /Type /XObject /Subtype /Image /Width 1 /Height 1 >>", []byte{0})
	b.Pages(
		pdftest.PageSpec{Content: "q 612 0 0 792 0 0 cm /Im Do Q", Resources: fmt.Sprintf("<< /XObject << /Im %d 0 R >> >>", img)},
		pdftest.PageSpec{},
	)
	doc := openBytes(t, b.Bytes(), Options{})
	got, _ := doc.Markdown(ctx, MarkdownOptions{})
	want := "<!-- page 1 -->\n\n<!-- this page is a scanned image with no text layer -->\n\n<!-- page 2 -->\n\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, _ := doc.Markdown(ctx, MarkdownOptions{OmitPageMarkers: true}); got != "<!-- this page is a scanned image with no text layer -->\n" {
		t.Errorf("without markers: %q", got)
	}
}

func TestEscapeStart(t *testing.T) {
	for in, want := range map[string]string{
		"plain text": "plain text", "# hash": "\\# hash", "> quote": "\\> quote", "- dash": "\\- dash", "+ plus": "\\+ plus",
		"* star": "\\* star", "-5 degrees": "-5 degrees", "*bold*": "*bold*", "12. item": "12\\. item", "3) item": "3\\) item",
		"3.5 metres": "3.5 metres", "2024": "2024", "": "",
	} {
		if got := escapeStart(in); got != want {
			t.Errorf("escapeStart(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestContentsLinesKeepTheirBreaks(t *testing.T) {
	// Words 6 points wide to a character, in 10 point type.
	line := func(y float64, text string) Line {
		l := Line{Box: Box{72, y, 72, y + 10}}
		x := 72.0
		for _, w := range strings.Fields(text) {
			l.Words = append(l.Words, Word{Text: w, Size: 10, Box: Box{x, y, x + 6*float64(len(w)), y + 10}})
			x += 6*float64(len(w)) + 3
		}
		l.Box.X1 = x - 3
		return l
	}
	block := func(lines ...string) *Block {
		b := &Block{Box: Box{72, 100, 72, 100}}
		for i, text := range lines {
			l := line(100+12*float64(i), text)
			b.Lines = append(b.Lines, l)
			b.Box.X1, b.Box.Y1 = max(b.Box.X1, l.Box.X1), l.Box.Y1
		}
		return b
	}
	// The entries of a table of contents reach the full width, as the
	// lines of a paragraph do, and each stays a line.
	contents := block(
		"Item 1. Business of the company . . . . . . . . . . . 1",
		"Item 2. Risks the company runs . . . . . . . . . . . 14",
		"Item 3. Properties that it owns . . . . . . . . . . . 22",
		"Item 4. Proceedings at law . . . . . . . . . . . . . 25")
	if isProse(contents) {
		t.Error("the lines of a table of contents run on")
	}
	if got := renderBlock(contents, nil); strings.Count(got, "\n") != 3 {
		t.Errorf("rendered as %q", got)
	}
	// One line with a leader at its end is the end of a sentence.
	sentence := block(
		"If you do not want to claim the credit, as most do, you",
		"check the box at the end of the line here . . . . . . . .")
	if !isProse(sentence) {
		t.Error("a sentence that ends in a leader does not run on")
	}
	for text, want := range map[string]bool{
		"Chapter one . . . . 12": true, "Chapter one ........ 12": true, "and so on ... to the end": false,
		"a . b . c . d": false, "no dots": false, "": false,
	} {
		if got := leaderLine(line(0, text)); got != want {
			t.Errorf("leaderLine(%q) = %v", text, got)
		}
	}
}
