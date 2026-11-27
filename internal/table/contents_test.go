// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package table

import "testing"

func TestContentsInThreeColumns(t *testing.T) {
	// A table of contents with a number, a title and a page in three
	// columns, no dot leaders, page numbers that carry the letter of their
	// part, and a heading between the groups of entries.
	build := func(pages []string) *page {
		p := &page{}
		p.text(115, 50, "Item 1.", 110, "Business", 400, pages[0])
		p.text(127, 50, "Item 1A.", 110, "Risk factors", 400, pages[1])
		p.text(139, 50, "Item 2.", 110, "Properties", 400, pages[2])
		p.text(155, 200, "Part II")
		p.text(170, 50, "Item 5.", 110, "Market for the shares", 400, pages[3])
		p.text(182, 50, "Item 6.", 110, "Selected data", 400, pages[4])
		p.text(194, 50, "Item 7.", 110, "Discussion and analysis", 400, pages[5])
		p.text(210, 200, "Part III")
		p.text(225, 50, "Item 10.", 110, "Directors", 400, pages[6])
		p.text(237, 50, "Item 11.", 110, "Compensation", 400, pages[7])
		return p
	}
	p := build([]string{"K-1", "K-25", "K-29", "K-32", "K-34", "K-35", "K-118", "K-118"})
	tables, _ := Aligned(p.words, p.lines, nil)
	if len(tables) != 1 || tables[0].Rows != 10 {
		t.Fatalf("%d tables", len(tables))
	}
	if tables[0].Confidence > 0.3 {
		t.Errorf("a table of contents has confidence %.2f", tables[0].Confidence)
	}

	// The same layout with codes in the last column that go up and down is
	// a table.
	p = build([]string{"K-41", "K-25", "K-29", "K-12", "K-34", "K-3", "K-118", "K-7"})
	tables, _ = Aligned(p.words, p.lines, nil)
	if len(tables) != 1 || tables[0].Confidence < 0.5 {
		t.Fatalf("codes that do not rise: %d tables, confidence %.2f", len(tables), tables[0].Confidence)
	}
}

func TestContentsWithSeveralEntriesToARow(t *testing.T) {
	// The search put the lines of each group of entries into one row. Each
	// line of the last column is still one entry's page number.
	tb := &Table{Rows: 5, Cols: 3, Cells: []Cell{
		{Row: 0, Col: 0, Text: "Item 1.\nItem 2.\nItem 3."}, {Row: 0, Col: 1, Text: "Business\nRisk factors\nProperties"}, {Row: 0, Col: 2, Text: "K-1\nK-14\nK-22"},
		{Row: 1, Col: 1, Text: "Part II"},
		{Row: 2, Col: 0, Text: "Item 5.\nItem 6."}, {Row: 2, Col: 1, Text: "Market for the shares\nSelected data"}, {Row: 2, Col: 2, Text: "K-25\nK-27"},
		{Row: 3, Col: 1, Text: "Part III"},
		{Row: 4, Col: 0, Text: "Signatures"}, {Row: 4, Col: 2, Text: "K-30"},
	}}
	if !contentsLike(tb) {
		t.Error("not taken for a table of contents")
	}
	// A page number that goes down in the middle of a cell.
	tb.Cells[2].Text = "K-1\nK-40\nK-22"
	tb.Cells[6].Text = "K-9\nK-5"
	if contentsLike(tb) {
		t.Error("page numbers that go down are taken for a table of contents")
	}
	// A row with several cells and no page number counts against.
	tb = &Table{Rows: 5, Cols: 3}
	for r := 0; r < 5; r++ {
		tb.Cells = append(tb.Cells, Cell{Row: r, Col: 0, Text: "Name"}, Cell{Row: r, Col: 1, Text: "Place"})
	}
	if contentsLike(tb) {
		t.Error("a table with an empty last column is taken for a table of contents")
	}
}

func TestPageLabel(t *testing.T) {
	for _, tt := range []struct {
		s, part string
		n       int
		ok      bool
	}{
		{"12", "", 12, true}, {"K-118", "K", 118, true}, {"A-3", "A", 3, true}, {"App-7", "App", 7, true},
		{"xiv", "", -1, true}, {"", "", 0, false}, {"K-xiv", "", 0, false}, {"well-known", "", 0, false},
		{"2020-21", "", 0, false}, {"ABCD-1", "", 0, false}, {"-5", "", 0, false}, {"K-", "", 0, false},
		{"123456", "", 0, false}, {"K-123456", "", 0, false},
	} {
		part, n, ok := pageLabel(tt.s)
		if ok != tt.ok || (ok && (part != tt.part || n != tt.n)) {
			t.Errorf("%q: got %q, %d, %v", tt.s, part, n, ok)
		}
	}
}
