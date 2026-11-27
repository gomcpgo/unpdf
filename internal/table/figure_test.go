// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package table

import "testing"

// labels is four rows of three entries that line up: a table, or the boxes
// of a diagram. Its text lies between x 50 and 290 and between y 106 and
// 176, a row every 20 points.
func labels(t *testing.T) *Table {
	t.Helper()
	p := &page{}
	p.text(114, 50, "conv one", 150, "conv two", 250, "conv six")
	p.text(134, 50, "pool one", 150, "pool two", 250, "pool six")
	p.text(154, 50, "norm one", 150, "norm two", 250, "norm six")
	p.text(174, 50, "full one", 150, "full two", 250, "full six")
	tables, _ := Aligned(p.words, p.lines, nil)
	if len(tables) != 1 || tables[0].Rows != 4 || tables[0].Cols != 3 || tables[0].Confidence < 0.5 {
		t.Fatalf("tables = %+v", tables)
	}
	return &tables[0]
}

func TestFigureFromShapes(t *testing.T) {
	tb := labels(t)
	for _, tc := range []struct {
		name   string
		shapes []Box
		want   bool
	}{
		{"nothing drawn", nil, false},
		{"an arc from the first row to the third", []Box{{120, 108, 140, 152}}, true},
		{"a line of a chart across the whole table", []Box{{40, 100, 300, 180}}, true},
		{"an arc between two rows next to each other", []Box{{200, 110, 215, 132}}, true},
		// What decorates a table stays within a row.
		{"a badge behind one word", []Box{{148, 105, 152, 117}, {188, 105, 192, 117}}, false},
		{"a tick in every row", []Box{{210, 108, 218, 116}, {210, 128, 218, 136}, {210, 148, 218, 156}, {210, 168, 218, 176}}, false},
		{"the rounded corners of a frame", []Box{{44, 100, 52, 108}, {288, 100, 296, 108}, {44, 174, 52, 182}, {288, 174, 296, 182}}, false},
		{"an arrowhead in the gap between two rows", []Box{{170, 117, 176, 123}}, false},
		{"a shape that only grazes the next row", []Box{{120, 108, 140, 125}}, false},
		// What is drawn beside the table says nothing about it.
		{"an arc to the left of the table", []Box{{10, 108, 40, 152}}, false},
		{"an arc above the table", []Box{{120, 20, 140, 90}}, false},
		{"an arc below the table", []Box{{120, 190, 140, 260}}, false},
	} {
		if got := Figure(tb, tc.shapes, nil); got != tc.want {
			t.Errorf("%s: figure = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestFigureFromPictures(t *testing.T) {
	tb := labels(t) // x 50 to 290, y 106 to 176
	for _, tc := range []struct {
		name     string
		pictures []Box
		want     bool
	}{
		{"no picture", nil, false},
		{"a plot between the labels of its axes", []Box{{90, 100, 240, 170}}, true},
		{"a photograph the labels are set on", []Box{{0, 0, 600, 500}}, true},
		{"a picture under the right half", []Box{{170, 90, 400, 200}}, true},
		// A panel behind the table, made for it.
		{"a backdrop a little larger than the table", []Box{{40, 96, 300, 186}}, false},
		{"a logo that overlaps a corner", []Box{{260, 60, 330, 120}}, false},
		{"a picture beside the table", []Box{{300, 100, 500, 180}}, false},
		{"a strip of shading behind the first row", []Box{{40, 104, 300, 118}}, false},
	} {
		if got := Figure(tb, nil, tc.pictures); got != tc.want {
			t.Errorf("%s: figure = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestFigureEdges(t *testing.T) {
	// Nothing to go by, and nothing to divide by.
	if Figure(&Table{}, []Box{{0, 0, 10, 10}}, []Box{{0, 0, 10, 10}}) {
		t.Error("an empty table is a figure")
	}
	// A cell merged down two rows does not make a row of the two.
	tb := labels(t)
	tb.Cells[0].RowSpan = 2
	if !Figure(tb, []Box{{120, 108, 140, 152}}, nil) {
		t.Error("a merged cell hid the rows an arc runs through")
	}
}

func TestBesideAFigure(t *testing.T) {
	// The labels lie between x 50 and 290 and between y 106 and 176: 70
	// points of four rows, 17.5 to a row.
	tb := labels(t)
	for _, tc := range []struct {
		name             string
		shapes, pictures []Box
		want             bool
	}{
		{"a picture that ends at the top row", nil, []Box{{60, 20, 280, 110}}, true},
		{"a picture that begins at the last row", nil, []Box{{60, 170, 280, 300}}, true},
		{"a picture two rows above", nil, []Box{{60, 0, 280, 70}}, false},
		{"a picture over a third of the width", nil, []Box{{50, 20, 130, 110}}, false},
		{"a picture lower than the table is high", nil, []Box{{60, 60, 280, 110}}, false},
		{"a picture under the table", nil, []Box{{40, 100, 300, 180}}, false},
		{"two small pictures side by side", nil, []Box{{50, 20, 150, 110}, {190, 20, 290, 110}}, false},
		{"the lines of a chart below", []Box{{55, 180, 160, 260}, {150, 185, 285, 250}}, nil, true},
		{"an arrowhead below", []Box{{100, 180, 110, 190}}, nil, false},
		{"lines that begin three rows below", []Box{{55, 230, 285, 400}}, nil, false},
		{"nothing drawn", nil, nil, false},
	} {
		if got := Beside(tb, tc.shapes, tc.pictures); got != tc.want {
			t.Errorf("%s: got %v", tc.name, got)
		}
	}
	// A table of more rows is no row of labels, whatever stands over it.
	tb.Rows = edgeRows + 1
	if Beside(tb, nil, []Box{{60, 20, 280, 110}}) {
		t.Error("a table of five rows is taken for the labels of a figure")
	}
	if Beside(&Table{}, nil, []Box{{0, 0, 10, 10}}) {
		t.Error("an empty table")
	}
}
