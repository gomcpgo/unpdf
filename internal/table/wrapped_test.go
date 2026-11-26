// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package table

import (
	"strings"
	"testing"
)

func TestAlignedLineWithANarrowGap(t *testing.T) {
	// One label is so long that the space between it and the next cell is
	// narrower than cells are set apart. The line still keeps its two
	// columns apart, and the table goes on through it.
	p := &page{}
	p.text(100, 50, "Function", 170, "What it does")
	p.text(114, 50, "Govern", 170, "Sets the rules")
	p.text(128, 50, "Map the context in full", 170, "Finds the risks")
	p.text(142, 50, "Measure", 170, "Tracks the risks")
	p.text(156, 50, "Manage", 170, "Acts on the risks")
	if a, b := p.words[p.lines[2].Words[4]], p.words[p.lines[2].Words[5]]; b.X0-a.X1 > cellGap*10 {
		t.Fatalf("the gap of %.0f points is wide enough to part cells", b.X0-a.X1)
	}
	tables, _ := Aligned(p.words, p.lines, nil)
	if len(tables) != 1 {
		t.Fatalf("%d tables", len(tables))
	}
	want := [][]string{{"Function", "What it does"}, {"Govern", "Sets the rules"}, {"Map the context in full", "Finds the risks"},
		{"Measure", "Tracks the risks"}, {"Manage", "Acts on the risks"}}
	if got := rowsOf(tables[0]); !same(got, want) {
		t.Errorf("rows = %q", got)
	}

	// A line of prose under the table has words in both columns too, and
	// no more space where the second begins than between any two words.
	p.text(170, 50, "These four functions are described in the chapters that follow.")
	tables, member := Aligned(p.words, p.lines, nil)
	if len(tables) != 1 || tables[0].Rows != 5 || member[5] != -1 {
		t.Errorf("with a line of prose under it: %d rows, membership %v", tables[0].Rows, member)
	}
}

func TestAlignedCellContinuedFromThePageBefore(t *testing.T) {
	// The top of a page: the header again, then the rest of a cell that
	// began on the page before, in the second column only, then the rows
	// that start on this page. Rules run between the rows.
	p := &page{}
	p.text(100, 50, "Category", 170, "Subcategories")
	p.h(105, 40, 420)
	p.text(116, 170, "the rest of a cell that began on the page before")
	p.text(128, 170, "and runs on here for some lines of text until")
	p.text(140, 170, "it ends.")
	p.h(145, 40, 420)
	p.text(156, 50, "Map", 170, "The context is established and understood")
	p.text(168, 170, "by all who work on the system.")
	p.h(173, 40, 420)
	p.text(184, 50, "Measure", 170, "Risks are assessed with methods fit for them")
	p.text(196, 170, "and tracked over time.")
	tables, member := Aligned(p.words, p.lines, p.rules)
	if len(tables) != 1 {
		t.Fatalf("%d tables", len(tables))
	}
	got := flatRows(tables[0])
	want := [][]string{{"Category", "Subcategories"},
		{"", "the rest of a cell that began on the page before and runs on here for some lines of text until it ends."},
		{"Map", "The context is established and understood by all who work on the system."},
		{"Measure", "Risks are assessed with methods fit for them and tracked over time."}}
	if !same(got, want) || tables[0].Confidence < 0.5 {
		t.Errorf("rows = %q, confidence %.2f", got, tables[0].Confidence)
	}
	for i, m := range member {
		if m != 0 {
			t.Errorf("line %d is not part of the table", i)
		}
	}

	// A heading with a gap in it over a paragraph is no table, however
	// many lines the paragraph has.
	q := &page{}
	q.text(100, 50, "3.2", 90, "Scope of the work")
	for i := 0; i < 8; i++ {
		q.text(114+12*float64(i), 50, "This paragraph runs the full width of the page and says a good deal.")
	}
	if tables, _ := Aligned(q.words, q.lines, nil); len(tables) != 0 {
		t.Errorf("a heading over a paragraph: %+v", tables)
	}
}

// flatRows returns the rows of a table with the lines of each cell joined.
func flatRows(t Table) [][]string {
	rows := rowsOf(t)
	for _, r := range rows {
		for c := range r {
			r[c] = strings.ReplaceAll(r[c], "\n", " ")
		}
	}
	return rows
}

func TestAlignedRowsFromTheText(t *testing.T) {
	// Rows of figures with wordy labels, which fill their column as text
	// that wraps does. Every line is a row.
	p := &page{}
	p.text(100, 50, "Under eight years old", 220, "543", 280, "236", 340, "779")
	p.text(112, 50, "of which under three", 220, "248", 280, "111", 340, "359")
	p.text(124, 50, "Eight years and older", 220, "449", 280, "133", 340, "582")
	p.text(136, 50, "Age not known at all", 220, "152", 280, "35", 340, "187")
	tables, _ := Aligned(p.words, p.lines, nil)
	if len(tables) != 1 || tables[0].Rows != 4 || tables[0].Confidence < 0.5 || rowsOf(tables[0])[1][0] != "of which under three" {
		t.Errorf("rows of figures with wordy labels: %+v", tables)
	}

	// Entries of a length, one under the other, in every column.
	p = &page{}
	p.text(100, 50, "Name of the security", 220, "Symbol", 300, "Where it is traded")
	p.text(112, 50, "Ordinary shares of A", 220, "ORDA", 300, "Northern Stock Exchange")
	p.text(124, 50, "Ordinary shares of B", 220, "ORDB", 300, "Northern Stock Exchange")
	p.text(136, 50, "Loan notes due 2027", 220, "LNSV", 300, "Northern Stock Exchange")
	p.text(148, 50, "Loan notes due 2030", 220, "LNTH", 300, "Northern Stock Exchange")
	tables, _ = Aligned(p.words, p.lines, nil)
	if len(tables) != 1 || tables[0].Rows != 5 || tables[0].Confidence < 0.5 {
		t.Errorf("entries of a length: %+v", tables)
	}

	// Cells of wrapped text, with the lines evenly spaced and no rule: a
	// row starts where the first column starts an entry.
	p = &page{}
	p.text(100, 50, "Govern", 150, "Policies and procedures are in place", 370, "Board")
	p.text(112, 150, "across the organisation and are known")
	p.text(124, 150, "to those who carry them out.")
	p.text(136, 50, "Map", 150, "The context the system works in is", 370, "Design team")
	p.text(148, 150, "known and its risks are written down.")
	p.text(160, 50, "Measure", 150, "Risks are assessed with methods that", 370, "Audit")
	p.text(172, 150, "fit them and tracked over its life.")
	tables, _ = Aligned(p.words, p.lines, nil)
	if len(tables) != 1 {
		t.Fatalf("wrapped cells: %d tables", len(tables))
	}
	want := [][]string{
		{"Govern", "Policies and procedures are in place across the organisation and are known to those who carry them out.", "Board"},
		{"Map", "The context the system works in is known and its risks are written down.", "Design team"},
		{"Measure", "Risks are assessed with methods that fit them and tracked over its life.", "Audit"}}
	if got := flatRows(tables[0]); !same(got, want) || tables[0].Confidence < 0.5 {
		t.Errorf("wrapped cells: rows %q, confidence %.2f", got, tables[0].Confidence)
	}

	// The same with a first column that wraps as well: a line that goes
	// on in lower case starts no row, nor does a capital under a full line.
	p = &page{}
	p.text(100, 50, "Govern the whole", 150, "Policies and procedures are in place", 370, "Board")
	p.text(112, 50, "of the system", 150, "across the organisation and are known")
	p.text(124, 150, "to those who carry them out.")
	p.text(136, 50, "Map what the AI", 150, "The context the system works in is", 370, "Design team")
	p.text(148, 50, "Act requires", 150, "known and its risks are written down.")
	p.text(160, 150, "Each of them has an owner.")
	p.text(172, 50, "Measure", 150, "Risks are assessed with methods that", 370, "Audit")
	p.text(184, 150, "fit them and tracked over its life.")
	tables, _ = Aligned(p.words, p.lines, nil)
	if len(tables) != 1 {
		t.Fatalf("a first column that wraps: %d tables", len(tables))
	}
	if got := flatRows(tables[0]); len(got) != 3 || got[0][0] != "Govern the whole of the system" || got[1][0] != "Map what the AI Act requires" ||
		!strings.HasSuffix(got[1][1], "Each of them has an owner.") || got[2][0] != "Measure" {
		t.Errorf("a first column that wraps: rows %q", got)
	}
}

func TestAlignedRowsTheTextDoesNotGive(t *testing.T) {
	sure := func(p *page) (float64, int) {
		tables, _ := Aligned(p.words, p.lines, nil)
		if len(tables) != 1 {
			t.Fatalf("%d tables", len(tables))
		}
		return tables[0].Confidence, tables[0].Rows
	}
	// A script with no capitals says nothing of where an entry starts.
	p := &page{}
	for i, s := range []string{"統治", "方針と手順", "計画", "文脈の理解", "測定"} {
		p.text(100+12*float64(i), 50, s, 150, "方針 と 手順 が 組織 全体 に 整備 され 理解 され て いる")
	}
	if conf, _ := sure(p); conf > 0.3 {
		t.Errorf("no capitals: confidence %.2f", conf)
	}

	// A table of contents, with its page numbers at the end of the titles.
	p = &page{}
	for i, s := range []string{"Measures of labour market tightness", "Estimates of the natural rate", "Workers involved in stoppages",
		"Change in the share of labour", "Gaps in the rate of employment"} {
		p.text(100+12*float64(i), 50, "1-"+string(rune('1'+i)), 100, s+" ........ "+string(rune('2'+i))+"6")
	}
	if conf, _ := sure(p); conf > 0.3 {
		t.Errorf("a table of contents: confidence %.2f", conf)
	}

	// Two columns of running text: every line of the first goes on from
	// the one above, and no entry starts after a gap.
	p = &page{}
	left := []string{"The committee met in the spring", "to hear what the members had", "found in the course of a year", "and to settle what was to be",
		"done about it. Nothing was said", "of the cost, which had risen"}
	right := []string{"while the question of the hall", "was put off again until more", "was known of what the repairs", "would come to. A vote of thanks",
		"closed the meeting, and the", "members went home in the rain."}
	for i := range left {
		p.text(100+12*float64(i), 50, left[i], 260, right[i])
	}
	if conf, rows := sure(p); conf > 0.3 || rows != 6 {
		t.Errorf("two columns of running text: confidence %.2f, %d rows", conf, rows)
	}
}
