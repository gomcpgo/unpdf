// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package table

import "testing"

// columnsOfText puts characters on the made-up page as vertical text read
// across: each string is a row cut through the columns, one character from
// each, with a wider space where a column is left empty. pitch is the
// distance from one row to the next; the type is 10 points.
func columnsOfText(pitch float64, rows ...string) *page {
	p := &page{}
	for i, row := range rows {
		var cells []any
		x := 50
		for _, r := range row {
			if r != ' ' {
				cells = append(cells, x, string(r))
			}
			x += 12
		}
		p.text(100+float64(i)*pitch, cells...)
	}
	return p
}

func TestAlignedVerticalTextIsNoTable(t *testing.T) {
	// Six columns of vertical text, two of them shorter than the rest, so
	// that the rows read across have gaps as wide as a table's. Each
	// character stands right under the one before it.
	rows := []string{"第三条の項を", "十法第二第改", "三第二十二め", "条  五 る", "第  条 こ", "一  のと"}
	p := columnsOfText(10, rows...)
	if tables, member := Aligned(p.words, p.lines, nil); len(tables) != 0 {
		t.Errorf("%d tables, the first %q; membership %v", len(tables), rowsOf(tables[0]), member)
	}
	for i, is := range stacked(p.words, p.lines) {
		if !is {
			t.Errorf("row %d is not taken for vertical text", i)
		}
	}

	// The same characters with space between the rows are a table with a
	// character to a cell: rare, but nothing says it is not one.
	p = columnsOfText(16, rows...)
	if tables, _ := Aligned(p.words, p.lines, nil); len(tables) != 1 {
		t.Errorf("rows set apart: %d tables", len(tables))
	}

	// A header of one character to a column over rows of figures.
	p = &page{}
	p.text(100, 50, "日", 110, "月", 170, "火", 230, "水")
	p.text(110, 50, "1", 110, "120", 170, "135", 230, "128")
	p.text(120, 50, "2", 110, "98", 170, "101", 230, "97")
	p.text(130, 50, "3", 110, "143", 170, "150", 230, "139")
	tables, _ := Aligned(p.words, p.lines, nil)
	if len(tables) != 1 || tables[0].Rows != 4 {
		t.Fatalf("a header of single characters: %d tables", len(tables))
	}
	if got := rowsOf(tables[0])[0]; got[1] != "月" {
		t.Errorf("header row %q", got)
	}

	// Words of the same script, set solid, are a table too: the cells of
	// vertical text read across never hold more than a character.
	p = &page{}
	p.text(100, 50, "東京", 110, "大阪", 170, "京都")
	p.text(110, 50, "札幌", 110, "仙台", 170, "新潟")
	p.text(120, 50, "福岡", 110, "那覇", 170, "広島")
	p.text(130, 50, "松山", 110, "高知", 170, "徳島")
	if tables, _ := Aligned(p.words, p.lines, nil); len(tables) != 1 {
		t.Errorf("words of two characters: %d tables", len(tables))
	}
}
