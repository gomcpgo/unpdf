// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package table

import "testing"

func TestFormFieldsAreNoTable(t *testing.T) {
	// The questions of a form: a number, then labels with rows of dots that
	// lead to boxes, which are drawn and so are not there as text.
	p := &page{}
	p.text(100, 50, "5", 80, "Insolvent ..........", 250, "Likely to be ..........", 420, "Overindebted ..........")
	p.text(120, 50, "6", 80, "Opened ..........", 250, "Costs so far ..........")
	p.text(140, 50, "7", 80, "No link abroad ..........", 250, "As main proceedings ..........")
	p.text(160, 80, "Link within the Union ..........", 250, "As secondary ones ..........")
	p.text(180, 50, "8", 80, "Under supervision ..........", 250, "Refused by the court ..........", 420, "Not applied for ..........")
	tables, _ := Aligned(p.words, p.lines, nil)
	if len(tables) != 1 {
		t.Fatalf("%d tables", len(tables))
	}
	if tables[0].Confidence > 0.3 {
		t.Errorf("the fields of a form have confidence %.2f: %q", tables[0].Confidence, rowsOf(tables[0]))
	}

	// A table whose labels lead to their figures with dots.
	p = &page{}
	p.text(100, 50, "Crop", 250, "2023", 320, "2024")
	p.text(115, 50, "Wheat ..........", 250, "1,200", 320, "1,310")
	p.text(130, 50, "Maize ..........", 250, "2,450", 320, "2,380")
	p.text(145, 50, "Barley ..........", 250, "310", 320, "295")
	p.text(160, 50, "Oats ..........", 250, "88", 320, "91")
	tables, _ = Aligned(p.words, p.lines, nil)
	if len(tables) != 1 || tables[0].Confidence < 0.5 {
		t.Fatalf("dots that lead to figures: %d tables, confidence %.2f", len(tables), tables[0].Confidence)
	}
}

func TestFormLike(t *testing.T) {
	cells := func(rows ...[]string) *Table {
		tb := &Table{Rows: len(rows)}
		for r, row := range rows {
			tb.Cols = max(tb.Cols, len(row))
			for c, text := range row {
				tb.Cells = append(tb.Cells, Cell{Row: r, Col: c, RowSpan: 1, ColSpan: 1, Text: text})
			}
		}
		return tb
	}
	// Dots set apart count as dots; a label of two lines ends in them on
	// its second.
	if !formLike(cells(
		[]string{"Name . . . . .", ""},
		[]string{"Street and\nnumber . . . .", "Town . . . . ."},
		[]string{"Telephone ......", ""})) {
		t.Error("labels with dots that lead to nothing are not taken for a form")
	}
	// Two such labels are too few, and so is one in a table whose other
	// dots lead to figures.
	if formLike(cells([]string{"Name .....", ""}, []string{"Town .....", ""}, []string{"Notes", "none"})) {
		t.Error("two labels make a form")
	}
	if formLike(cells(
		[]string{"Farm income: .....", ""},
		[]string{"Crops .....", "120"}, []string{"Livestock .....", "95"}, []string{"Other .....", "(7)"},
		[]string{"Costs .....", "88"}, []string{"Net .....", "120"}, []string{"Share .....", "12%"},
		[]string{"Rank .....", "3"}, []string{"Change .....", "+4"})) {
		t.Error("a table with one line of dots that leads to nothing is taken for a form")
	}
}
