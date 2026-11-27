// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package table

import "testing"

// continued builds a table of two columns of words, ruled off above, under
// its header and between its rows, with a last line at x under it.
func continued(x int, last string) *page {
	p := &page{}
	p.h(92, 40, 420)
	p.text(100, 50, "Category", 170, "Subcategories")
	p.h(105, 40, 420)
	p.text(116, 50, "Govern", 170, "Policies are in place and are understood")
	p.text(128, 170, "by all who work on the system.")
	p.h(133, 40, 420)
	p.text(144, 50, "Map", 170, "The context is established and understood")
	p.text(156, 170, "before any work begins.")
	p.h(161, 40, 420)
	p.text(172, x, last)
	return p
}

func TestAlignedNoteUnderTheClosingRule(t *testing.T) {
	want := [][]string{{"Category", "Subcategories"},
		{"Govern", "Policies are in place and are understood by all who work on the system."},
		{"Map", "The context is established and understood before any work begins."}}

	// A note at the right, under the last rule of a table that runs on.
	// Then a source at the left. Neither is a row.
	for _, c := range []struct {
		x    int
		note string
	}{{261, "Continued on next page"}, {50, "Source: Office of Records"}} {
		p := continued(c.x, c.note)
		tables, member := Aligned(p.words, p.lines, p.rules)
		if len(tables) != 1 {
			t.Fatalf("%q: %d tables", c.note, len(tables))
		}
		if got := flatRows(tables[0]); !same(got, want) {
			t.Errorf("%q: rows = %q", c.note, got)
		}
		if member[len(member)-1] != -1 {
			t.Errorf("%q is part of the table", c.note)
		}
		if tables[0].Y1 > 161 {
			t.Errorf("%q: the table reaches down to %.0f, past its closing rule", c.note, tables[0].Y1)
		}
	}

	// With a rule under it as well, the line is the table's last row, which
	// has one cell filled.
	p := continued(170, "Not yet assessed")
	p.h(177, 40, 420)
	tables, member := Aligned(p.words, p.lines, p.rules)
	if len(tables) != 1 || tables[0].Rows != 4 || member[len(member)-1] != 0 {
		t.Fatalf("a last row with a rule under it: %d tables, membership %v", len(tables), member)
	}
	if got := flatRows(tables[0]); got[3][0] != "" || got[3][1] != "Not yet assessed" {
		t.Errorf("last row = %q", got[3])
	}
}

func TestAlignedTotalUnderTheClosingRule(t *testing.T) {
	// A total with no label, under a rule across the table and with none
	// below it, is a row: a note is words, and this is a figure.
	build := func(x int, last string) *page {
		p := &page{}
		p.h(92, 40, 330)
		p.text(100, 50, "Item", 200, "2023", 260, "2022")
		p.h(105, 40, 330)
		p.text(116, 50, "Rent", 200, "1,200", 260, "1,100")
		p.text(128, 50, "Power", 200, "300", 260, "280")
		p.text(140, 50, "Water", 200, "90", 260, "85")
		p.h(145, 40, 330)
		p.text(156, x, last)
		return p
	}
	p := build(260, "1,465")
	tables, member := Aligned(p.words, p.lines, p.rules)
	if len(tables) != 1 || tables[0].Rows != 5 || member[4] != 0 {
		t.Fatalf("a total under the rule: %d tables, membership %v", len(tables), member)
	}
	if got := rowsOf(tables[0]); got[4][2] != "1,465" {
		t.Errorf("last row = %q", got[4])
	}

	p = build(50, "Source: the accounts")
	tables, member = Aligned(p.words, p.lines, p.rules)
	if len(tables) != 1 || tables[0].Rows != 4 || member[4] != -1 {
		t.Fatalf("a source under the rule: %d tables, membership %v", len(tables), member)
	}
}

func TestAlignedTitleOfTheNextTable(t *testing.T) {
	// The title of the table that follows is centred on the page, and its
	// first lines are short enough to keep within the first column of the
	// table above. They stand apart from its rows and are centred over it,
	// where a label is flush with the other labels.
	build := func() *page {
		p := &page{}
		p.text(100, 50, "Comprehensive income", 300, "2023", 360, "2022", 420, "2021")
		p.text(112, 50, "Net earnings", 300, "97,147", 360, "21,998", 420, "90,949")
		p.text(124, 50, "Other income, net", 300, "1,318", 360, "3,010", 420, "1,889")
		p.text(136, 50, "Income attributable to shareholders", 300, "97,512", 360, "19,688", 420, "91,808")
		return p
	}
	p := build()
	p.text(156, 210, "COMPANY NAME INC.")
	p.text(168, 211, "and Subsidiaries")
	tables, member := Aligned(p.words, p.lines, nil)
	if len(tables) != 1 {
		t.Fatalf("%d tables", len(tables))
	}
	if tables[0].Rows != 4 || member[4] != -1 || member[5] != -1 {
		t.Errorf("%d rows, membership %v; rows = %q", tables[0].Rows, member, rowsOf(tables[0]))
	}

	// The second line of the last label is close under it and begins where
	// labels do.
	p = build()
	p.text(148, 60, "of the company")
	tables, member = Aligned(p.words, p.lines, nil)
	if len(tables) != 1 || member[4] != 0 {
		t.Fatalf("the second line of a label: %d tables, membership %v", len(tables), member)
	}

	// A line centred over the table but as close as its rows are is not set
	// apart, and is left alone.
	p = build()
	p.text(148, 225, "(unaudited)")
	tables, member = Aligned(p.words, p.lines, nil)
	if len(tables) != 1 || member[4] != 0 {
		t.Fatalf("a centred line close under the rows: %d tables, membership %v", len(tables), member)
	}
}

func TestAlignedNoteFlushRight(t *testing.T) {
	// No rules. The last column is set flush left, and the note under it
	// flush right, ending where the table does.
	build := func() *page {
		p := &page{}
		p.text(100, 50, "Function", 170, "What it does")
		p.text(114, 50, "Govern", 170, "Sets the rules for the work")
		p.text(128, 50, "Map", 170, "Finds the risks in context")
		p.text(142, 50, "Measure", 170, "Tracks the risks over time")
		p.text(156, 50, "Manage", 170, "Acts on the risks")
		return p
	}
	p := build()
	p.text(170, 191, "Continued on next page")
	if l := p.lines[5]; l.X1 != 295 {
		t.Fatalf("the note ends at %.0f, not at the table's edge", l.X1)
	}
	tables, member := Aligned(p.words, p.lines, nil)
	if len(tables) != 1 {
		t.Fatalf("%d tables", len(tables))
	}
	if tables[0].Rows != 5 || member[5] != -1 {
		t.Errorf("%d rows, membership %v; rows = %q", tables[0].Rows, member, rowsOf(tables[0]))
	}

	// The next line of the last cell begins where the column does.
	p = build()
	p.text(170, 170, "when they are found")
	tables, member = Aligned(p.words, p.lines, nil)
	if len(tables) != 1 || member[5] != 0 {
		t.Fatalf("the next line of a cell: %d tables, membership %v", len(tables), member)
	}

	// A column of figures is set flush right itself. A word at its right
	// edge is an entry like the others.
	p = &page{}
	p.text(100, 50, "Item", 150, "Unit", 255, "Amount")
	p.text(114, 50, "Rent", 150, "year", 255, "1,200,000")
	p.text(128, 50, "Power", 150, "month", 285, "300")
	p.text(142, 50, "Water", 150, "month", 290, "90")
	p.text(156, 280, "none")
	tables, member = Aligned(p.words, p.lines, nil)
	if len(tables) != 1 || member[4] != 0 {
		t.Fatalf("a word under a column of figures: %d tables, membership %v", len(tables), member)
	}
}
