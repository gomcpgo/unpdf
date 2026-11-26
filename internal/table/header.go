// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package table

import (
	"math"
	"slices"
	"strings"
)

// maxHeaderLines is the most lines of text the header of a table is looked
// for in.
const maxHeaderLines = 6

// dataRow reports whether a row reads as a row of data: a third or more of
// its cells hold figures, or one of them holds an amount of money. A column
// heading may hold a year or a footnote mark, but the headings of a table
// are words in the main.
func dataRow(words []Word, r row) bool {
	filled, numeric := 0, 0
	for _, idx := range r.cells {
		if len(idx) == 0 {
			continue
		}
		filled++
		if isNumeric(cellText(words, idx)) {
			numeric++
		}
		for _, i := range idx {
			if isAmount(words[i].Text) {
				return true
			}
		}
	}
	return filled > 0 && numeric*3 >= filled
}

// isAmount reports a currency sign, alone or in front of a figure.
func isAmount(s string) bool {
	for _, sign := range []string{"$", "€", "£", "¥", "₹"} {
		if rest, ok := strings.CutPrefix(s, sign); ok {
			return rest == "" || isNumeric(rest)
		}
	}
	return false
}

// joinHeader finds the header of a table built from lines of text and
// makes it one row for each level of headings.
//
// A column heading too long for its column is set over two or three lines,
// "Cases" over "received", and comes here as two or three rows. The header
// is the rows at the top down to the rule under the headings, where one is
// drawn across the columns, and otherwise down to the first row of figures.
// Its lines are joined column by column. Where a rule runs between two of
// its lines over some of the columns, the line above holds a heading for
// that group of columns and the lines are two levels: the columns the rule
// runs over keep both, a row for each, and the other columns, whose one
// heading stands beside the two, have it in the lower.
//
// It returns the rows with the header joined and the number of header
// rows, which is 0 when nothing shows where the headings end.
func joinHeader(words []Word, rows []row, cols int, hs []hline) ([]row, int) {
	if len(rows) < 2 || cols < 2 {
		return rows, 0
	}
	limit := min(len(rows)-1, maxHeaderLines)
	mid := func(r row) float64 { return (r.y0 + r.y1) / 2 }

	// The rules between the first lines: ruled[i][c] reports one above
	// line i over column c, and across[i] one over nearly all the columns
	// that hold text, in one stroke or a stroke for each column.
	edges := columnEdges(words, rows, cols)
	used := 0
	for c := 0; c < cols; c++ {
		if edges[c][1] > edges[c][0] {
			used++
		}
	}
	reach := min(len(rows)-1, limit+2)
	ruled := make([][]bool, reach+1)
	drawn, across := make([]bool, reach+1), make([]bool, reach+1)
	for i := 1; i <= reach; i++ {
		ruled[i] = make([]bool, cols)
		covered := 0
		for c := 0; c < cols; c++ {
			width := edges[c][1] - edges[c][0]
			for _, l := range hs {
				if l.y <= mid(rows[i-1]) || l.y >= mid(rows[i]) || width <= 0 {
					continue
				}
				if over := math.Min(l.x1, edges[c][1]) - math.Max(l.x0, edges[c][0]); over >= 0.5*width {
					ruled[i][c], drawn[i] = true, true
					covered++
					break
				}
			}
		}
		across[i] = used > 0 && covered*5 >= used*4
	}

	// Down to the rule under the headings: the first one across the
	// columns, unless the table is ruled off row by row.
	h := 0
	for i := 1; i <= limit; i++ {
		if !across[i] {
			continue
		}
		byRow := 0
		for k := i + 1; k <= min(i+2, reach); k++ {
			if across[k] {
				byRow++
			}
		}
		// What stands above it must read as headings: words, and years.
		if (byRow == 0 || byRow < min(2, reach-i)) && headings(words, rows[:i]) {
			h = i
		}
		break
	}
	if h == 0 {
		// Down to the first row of figures.
		figures := false
		for h < limit {
			if dataRow(words, rows[h]) {
				figures = true
				break
			}
			h++
		}
		if h == limit && !figures {
			figures = dataRow(words, rows[h])
		}
		if !figures {
			// A table of words: nothing but a rule can end its headings.
			return rows, 0
		}
		// A row with text in its first cell only, just above the figures,
		// names the rows that follow. It is no column heading.
		for h > 0 && rows[h-1].filled() == 1 && len(rows[h-1].cells[0]) > 0 {
			h--
		}
		// A rule over some of the columns parts two levels of headings. A
		// rule over all of them, where the table is ruled row by row, parts
		// two rows, and what is under it is no heading.
		for i := 1; i < h; i++ {
			if across[i] {
				h = i
			}
		}
	}
	if h == 0 {
		return rows, 0
	}
	// The headings must be headings of the columns: between them, the
	// header's lines have text over most of the columns that hold any.
	body, headed := 0, 0
	for c := 0; c < cols; c++ {
		inHeader, inBody := false, false
		for r := range rows {
			if len(rows[r].cells[c]) > 0 {
				if r < h {
					inHeader = true
				} else {
					inBody = true
				}
			}
		}
		if inBody {
			body++
			if inHeader {
				headed++
			}
		}
	}
	if body == 0 || headed*5 < body*3 {
		return rows, 0
	}
	if h == 1 {
		return rows, 1
	}
	// A heading for a group of columns is a phrase that runs on over them:
	// its words stand as close as the words of a phrase do, not apart as
	// the cells of a row do. It is one cell, and a level above the
	// headings of its columns.
	head := make([]row, h) // the header's lines, to change without changing rows
	for i := range head {
		head[i] = rows[i]
		head[i].cells = slices.Clone(rows[i].cells)
	}
	phrase := false
	for i := 0; i < h; i++ {
		for c := 0; c < cols; c++ {
			end := c
			for end+1 < cols && runsOn(words, head[i].cells[end], head[i].cells[end+1]) {
				end++
			}
			if end == c {
				continue
			}
			for k := c + 1; k <= end; k++ {
				head[i].cells[c] = append(slices.Clip(head[i].cells[c]), head[i].cells[k]...)
				head[i].cells[k] = nil
			}
			if phrase = true; i+1 < h {
				for k := c; k <= end; k++ {
					ruled[i+1][k] = true
				}
				drawn[i+1] = true
			}
			c = end
		}
	}
	// Two rows of words above the figures may as well be a heading and a
	// first row of data that holds no figure. They are lines of one
	// heading when a rule is drawn under them, when one of them holds
	// such a phrase, or when they are ragged, as headings of different
	// lengths are: one of them leaves empty a column that another fills.
	ragged := false
	for c := 0; c < cols && !ragged; c++ {
		with := 0
		for i := 0; i < h; i++ {
			if len(rows[i].cells[c]) > 0 {
				with++
			}
		}
		ragged = with > 0 && with < h
	}
	if !drawn[h] && !ragged && !phrase {
		return rows, 1
	}

	// Levels. A rule between two lines of the header parts two levels.
	parted := ruled
	level := make([]int, h) // the level each line belongs to
	for i := 1; i < h; i++ {
		level[i] = level[i-1]
		if drawn[i] {
			level[i]++
		}
	}
	levels := level[h-1] + 1
	out := make([]row, levels, levels+len(rows)-h)
	for k := range out {
		out[k] = row{cells: make([][]int, cols), y0: math.Inf(1), y1: math.Inf(-1)}
	}
	for i := 0; i < h; i++ {
		k := level[i]
		out[k].y0, out[k].y1 = math.Min(out[k].y0, head[i].y0), math.Max(out[k].y1, head[i].y1)
		for c, idx := range head[i].cells {
			// A heading goes on down its column until a rule parts it from
			// what is below, and stands in the row of its last line.
			last := i
			for last+1 < h && !parted[last+1][c] {
				last++
			}
			out[level[last]].cells[c] = append(out[level[last]].cells[c], idx...)
		}
	}
	return append(out, rows[h:]...), levels
}

// headings reports whether some rows at the top of a table read as its
// headings: no amount of money, and few figures other than years.
func headings(words []Word, rows []row) bool {
	filled, figures := 0, 0
	for _, r := range rows {
		for _, idx := range r.cells {
			if len(idx) == 0 {
				continue
			}
			filled++
			for _, i := range idx {
				if isAmount(words[i].Text) {
					return false
				}
			}
			if text := cellText(words, idx); isNumeric(text) && !isYears(text) {
				figures++
			}
		}
	}
	return filled > 0 && figures*3 <= filled
}

// isYears reports whether every figure in s is a year, as in "2023",
// "2021* 2022*" or "1998-99".
func isYears(s string) bool {
	years := 0
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r < '0' || r > '9' }) {
		switch {
		case len(f) == 4 && (f[:2] == "19" || f[:2] == "20"):
			years++
		case len(f) == 2 && years > 0: // the second year of "1998-99"
		default:
			return false
		}
	}
	return years > 0
}

// runsOn reports whether the words of two cells next to each other on a
// line read on as one phrase: the gap between them is that of two words,
// not that of two cells.
func runsOn(words []Word, left, right []int) bool {
	for _, i := range left {
		a := words[i]
		for _, k := range right {
			b := words[k]
			if math.Abs((a.Y0+a.Y1)/2-(b.Y0+b.Y1)/2) > 0.5*math.Max(a.Size, b.Size) {
				continue // not on one line
			}
			if gap := b.X0 - a.X1; gap >= -1 && gap <= 0.5*math.Max(a.Size, b.Size) {
				return true
			}
		}
	}
	return false
}

// headerBands returns how many rows at the top of a ruled table are its
// header, given, for each row, whether it is one of the rows of figures
// that a band between two rules was split into. Many tables rule off the
// header and leave the rows of figures to white space: there the rows
// above the first such band are the header, as long as they are few and
// each holds words in the main.
func headerBands(t *Table, split []bool) int {
	first := -1
	for r, s := range split {
		if s {
			first = r
			break
		}
	}
	if first < 1 || first > maxHeaderLines || first >= t.Rows {
		return 0
	}
	// Each of them must read as headings: words, and years.
	filled, figures := make([]int, first), make([]int, first)
	for _, c := range t.Cells {
		if c.Row < 0 || c.Row >= first || c.Text == "" {
			continue
		}
		filled[c.Row]++
		if isNumeric(c.Text) && !isYears(c.Text) {
			figures[c.Row]++
		}
		for _, w := range strings.Fields(c.Text) {
			if isAmount(w) {
				return 0
			}
		}
	}
	for r := range filled {
		if figures[r]*3 > filled[r] {
			return 0
		}
	}
	return first
}
