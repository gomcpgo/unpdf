// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package unpdf

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gomcpgo/unpdf/internal/pdftest"
)

// linesDoc builds a document with one page for each list of lines, set one
// under the other as a paragraph.
func linesDoc(t *testing.T, pages ...[]string) *Document {
	t.Helper()
	b := pdftest.New()
	res := fmt.Sprintf("<< /Font << /F1 %d 0 R >> >>", b.Add(helvetica))
	specs := make([]pdftest.PageSpec, len(pages))
	for i, lines := range pages {
		var c strings.Builder
		c.WriteString("BT /F1 12 Tf 72 700 Td ")
		for j, l := range lines {
			if j > 0 {
				c.WriteString("0 -14 Td ")
			}
			fmt.Fprintf(&c, "(%s) Tj ", l)
		}
		c.WriteString("ET")
		specs[i] = pdftest.PageSpec{Content: c.String(), Resources: res}
	}
	b.Pages(specs...)
	return openBytes(t, b.Bytes(), Options{})
}

func pageText(t *testing.T, doc *Document, n int) string {
	t.Helper()
	p, err := doc.Page(ctx, n)
	if err != nil {
		t.Fatal(err)
	}
	text, err := p.Text(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func TestCompoundBrokenAtItsHyphen(t *testing.T) {
	doc := linesDoc(t,
		// The page writes the compound within a line, and breaks it and an
		// ordinary word at line ends.
		[]string{"A long-term plan looks far ahead.", "This one is a long-", "term plan and an exam-", "ple of what is meant."},
		// The next page only breaks it, at the start of a sentence.
		[]string{"Other plans are short. Long-", "term ones are rare."},
		// A page that writes a word both ways gives no ground to keep the
		// hyphen.
		[]string{"Some write well-being and some wellbeing.", "Here it is well-", "being again."},
	)
	want := []string{
		"A long-term plan looks far ahead.\nThis one is a long-term\nplan and an example\nof what is meant.",
		"Other plans are short. Long-term\nones are rare.",
		"Some write well-being and some wellbeing.\nHere it is wellbeing\nagain.",
	}
	for i, w := range want {
		if got := pageText(t, doc, i+1); got != w {
			t.Errorf("page %d:\n got %q\nwant %q", i+1, got, w)
		}
	}

	// Blocks and Markdown say the same.
	p, err := doc.Page(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	blocks, err := p.Blocks(ctx)
	if err != nil || len(blocks) != 1 {
		t.Fatalf("%d blocks (err %v)", len(blocks), err)
	}
	if got := blocks[0].Text(); got != want[1] {
		t.Errorf("block text %q", got)
	}
	if last := blocks[0].Lines[0].Words; last[len(last)-1].Text != "Long-" {
		t.Errorf("the words of the line are changed: %q", last[len(last)-1].Text)
	}
	md, err := doc.Markdown(ctx, MarkdownOptions{OmitPageMarkers: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"is a long-term\n", "and an example\n", "Long-term ones are rare.", "it is wellbeing\n"} {
		if !strings.Contains(md, s) {
			t.Errorf("Markdown lacks %q:\n%s", s, md)
		}
	}
}

func TestCompoundIsLookedForNearby(t *testing.T) {
	// The compound stands within a line on the first page only. A page up
	// to four pages on takes it from there; a page further on does not, and
	// joins the word as it would any other.
	pages := make([][]string, 7)
	pages[0] = []string{"A short-term loan was made."}
	for i := 1; i < len(pages); i++ {
		pages[i] = []string{"Nothing of note on this page."}
	}
	pages[4] = []string{"It was repaid as a short-", "term loan is."}
	pages[6] = []string{"It was repaid as a short-", "term loan is."}
	doc := linesDoc(t, pages...)
	if got, want := pageText(t, doc, 5), "It was repaid as a short-term\nloan is."; got != want {
		t.Errorf("four pages on: %q", got)
	}
	if got, want := pageText(t, doc, 7), "It was repaid as a shortterm\nloan is."; got != want {
		t.Errorf("six pages on: %q", got)
	}

	// Asking for a page by itself gives the same answer as reading the
	// document through.
	doc = linesDoc(t, pages...)
	if got, want := pageText(t, doc, 5), "It was repaid as a short-term\nloan is."; got != want {
		t.Errorf("the page alone: %q", got)
	}
}

func TestCompoundInTheCellOfATable(t *testing.T) {
	doc := linesDoc(t, []string{"Output is given in current-dollar terms."})
	p, err := doc.Page(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range [][2]string{
		{"Share of current-\ndollar GDP\n(percent)", "Share of current-dollar\nGDP\n(percent)"},
		{"Current-\ndollar", "Current-dollar"},
		{"an exam-\nple", "an exam-\nple"}, // joined without its hyphen when the lines are
		{"one line", "one line"},
		{"1990-\n1995", "1990-\n1995"},
		{"-\n-", "-\n-"},
	} {
		got, err := p.cellCompounds(ctx, tt[0])
		if err != nil || got != tt[1] {
			t.Errorf("%q: got %q (err %v)", tt[0], got, err)
		}
	}
	tb := Table{Rows: 1, Cols: 2, Cells: []TableCell{{Text: "Share of current-dollar\nGDP"}, {Col: 1, Text: "an exam-\nple"}}}
	if got, want := tb.Markdown(), "| Share of current-dollar GDP | an example |\n| --- | --- |"; got != want {
		t.Errorf("Markdown %q", got)
	}
}

func TestWordKey(t *testing.T) {
	same := []string{"long-term", "Long-term,", "(LONG-TERM)", "long\u2010term", "long-term\u00ad"}
	for _, s := range same[1:] {
		if wordKey(s) != wordKey(same[0]) {
			t.Errorf("%q and %q have different keys", s, same[0])
		}
	}
	if wordKey("longterm") == wordKey("long-term") {
		t.Error("the hyphen makes no difference")
	}
	for _, s := range []string{"", "-", "1,234", "(…)"} {
		if wordKey(s) != 0 {
			t.Errorf("%q has a key", s)
		}
	}
}
