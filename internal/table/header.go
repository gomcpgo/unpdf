// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package table

import (
	"math"
	"slices"
	"strings"
	"unicode"
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
// heading stands beside the two, have it in the lower. The heading of a
// group is one cell merged across its columns, which the row's spans give.
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
	stroke := make([][]int, reach+1) // which rule it is, to tell one stroke over two columns from two
	drawn, across := make([]bool, reach+1), make([]bool, reach+1)
	for i := 1; i <= reach; i++ {
		ruled[i], stroke[i] = make([]bool, cols), make([]int, cols)
		covered := 0
		for c := 0; c < cols; c++ {
			stroke[i][c] = -1
			width := edges[c][1] - edges[c][0]
			for k, l := range hs {
				if l.y <= mid(rows[i-1]) || l.y >= mid(rows[i]) || width <= 0 {
					continue
				}
				if over := math.Min(l.x1, edges[c][1]) - math.Max(l.x0, edges[c][0]); over >= 0.5*width {
					ruled[i][c], drawn[i], stroke[i][c] = true, true, k
					covered++
					break
				}
			}
		}
		across[i] = used > 0 && covered*5 >= used*4
	}
	// sameGroups reports whether the rules above lines i and k part the
	// columns into the same groups, one of them of two columns or more: a
	// stroke over each pair of columns, say, under both.
	sameGroups := func(i, k int) bool {
		wide := false
		for c := 0; c < cols; c++ {
			if (stroke[i][c] < 0) != (stroke[k][c] < 0) {
				return false
			}
			if c+1 < cols && stroke[i][c] >= 0 && stroke[i][c+1] >= 0 {
				joined := stroke[i][c] == stroke[i][c+1]
				if joined != (stroke[k][c] == stroke[k][c+1]) {
					return false
				}
				wide = wide || joined
			}
		}
		return wide
	}

	// Down to the rule under the headings: the first one across the
	// columns, unless the table is ruled off row by row.
	h := 0
	for i := 1; i <= limit; i++ {
		if !across[i] {
			continue
		}
		// A header with a rule under every one of its lines, each level
		// ruled off over its columns, goes down to the last of them: the
		// lines between the rules read as headings, and under the last
		// rule the rows are no longer ruled off.
		last := i
		for last+1 <= limit && across[last+1] && headings(words, rows[:last+1]) &&
			!(rows[last].filled() == 1 && len(rows[last].cells[0]) > 0) {
			last++
		}
		for _, end := range []int{last, i} {
			byRow := 0
			for k := end + 1; k <= min(end+2, reach); k++ {
				if across[k] {
					byRow++
				}
			}
			// What stands above it must read as headings: words, and years.
			// A header ruled off line by line is told from a table ruled
			// off row by row by two rows under it with no rule between.
			ok := byRow == 0 || byRow < min(2, reach-i)
			if end > i {
				ok = byRow == 0 && end+2 <= reach
			}
			if ok && headings(words, rows[:end]) {
				h = end
				break
			}
		}
		break
	}
	years := false // the header ends in a line of years that no rule sets apart
	if h == 0 {
		// Down to the first row of figures.
		figures := false
		for h < limit {
			if dataRow(words, rows[h]) {
				// A line of years over rows of amounts, with nothing in
				// the first column, is the last line of the headings.
				if !yearsLine(words, rows[h]) || !dataRow(words, rows[h+1]) || yearsLine(words, rows[h+1]) {
					figures = true
					break
				}
				years = true
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
	spans := make([][]int, h) // for each line, the columns each of its cells covers, where more than one
	for i := 0; i < h; i++ {
		spans[i] = make([]int, cols)
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
			spans[i][c] = end - c + 1
			if phrase = true; i+1 < h {
				for k := c; k <= end; k++ {
					ruled[i+1][k] = true
				}
				drawn[i+1] = true
			}
			c = end
		}
	}
	// group makes the one heading that line i has in columns a to b the
	// heading of them all: a cell merged across them, in the first.
	group := func(i, a, b int) bool {
		at := -1
		for k := a; k <= b; k++ {
			if len(head[i].cells[k]) == 0 {
				continue
			}
			if at >= 0 {
				return false // two headings side by side
			}
			at = k
		}
		if at < 0 {
			return false
		}
		if at != a {
			head[i].cells[a], head[i].cells[at] = head[i].cells[at], nil
			spans[i][at] = 0
		}
		spans[i][a] = b - a + 1
		return true
	}
	// A rule between two lines of the header, drawn in one stroke over
	// several columns, shows how far the heading above it reaches, which
	// its words do not: they are centred over the group, and fall in the
	// column in its middle.
	for i := 0; i+1 < h; i++ {
		for a := 0; a < cols; a++ {
			b := a
			for b+1 < cols && stroke[i+1][a] >= 0 && stroke[i+1][b+1] == stroke[i+1][a] {
				b++
			}
			if b > a {
				group(i, a, b)
			}
			a = b
		}
	}
	// Years side by side on the last line with one heading over them and
	// nothing else: "Losses to December 31," over "2022" and "2023". The
	// heading is the group's, though it may fit over one of the years and
	// no rule sets it apart.
	for a, last := 0, h-1; h >= 2 && a < cols; a++ {
		if !isYears(cellText(words, head[last].cells[a])) {
			continue
		}
		b := a
		for k := a + 1; k < cols; k++ {
			if len(head[last].cells[k]) == 0 {
				continue
			}
			if !isYears(cellText(words, head[last].cells[k])) {
				break
			}
			b = k
		}
		over := -1 // the last line above the years with a heading over them
		for i := 0; i < last && b > a; i++ {
			n := 0
			for k := a; k <= b; k++ {
				if len(head[i].cells[k]) > 0 {
					n++
				}
			}
			if n > 1 || (n == 1 && isYears(cellText(words, slices.Concat(head[i].cells[a:b+1]...)))) {
				over = -1
				break
			}
			if n == 1 {
				over = i
			}
		}
		if over >= 0 {
			for i := 0; i <= over; i++ {
				group(i, a, b)
			}
			for k := a; k <= b; k++ {
				ruled[over+1][k] = true
			}
			drawn[over+1] = true
		}
		a = b
	}
	// Two rows of words above the figures may as well be a heading and a
	// first row of data that holds no figure. They are lines of one
	// heading when a rule is drawn under them, when one of them holds
	// such a phrase, when the last is a line of years, or when they are
	// ragged, as headings of different lengths are: one of them leaves
	// empty a column that another fills.
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
	if !drawn[h] && !ragged && !phrase && !years {
		return rows, 1
	}

	// Levels. A rule between two lines of the header parts two levels.
	parted := ruled
	level := make([]int, h) // the level each line belongs to
	for i := 1; i < h; i++ {
		level[i] = level[i-1]
		// Two lines with the same groups of columns ruled off under each
		// are two lines of the headings of those groups, not two levels.
		if drawn[i] && !sameGroups(i, i+1) {
			level[i]++
		}
	}
	levels := level[h-1] + 1
	out := make([]row, levels, levels+len(rows)-h)
	for k := range out {
		out[k] = row{cells: make([][]int, cols), y0: math.Inf(1), y1: math.Inf(-1), spans: make([]int, cols)}
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
			to := &out[level[last]]
			to.cells[c] = append(to.cells[c], idx...)
			to.spans[c] = max(to.spans[c], spans[i][c])
		}
	}
	// What a line of a level has in a column that another line's heading
	// is merged across is part of that heading.
	for k := range out {
		for c := 0; c < cols; c++ {
			for under := c + 1; under < min(c+out[k].spans[c], cols); under++ {
				out[k].cells[c] = append(out[k].cells[c], out[k].cells[under]...)
				out[k].cells[under], out[k].spans[under] = nil, 0
			}
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

// yearsLine reports whether a row is a line of years set over columns:
// nothing in the first cell, and a year or two in each of the others that
// hold anything.
func yearsLine(words []Word, r row) bool {
	if len(r.cells) == 0 || len(r.cells[0]) > 0 || r.filled() == 0 {
		return false
	}
	for _, idx := range r.cells {
		if len(idx) > 0 && !isYears(cellText(words, idx)) {
			return false
		}
	}
	return true
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

// ruledHeader returns how many rows at the top of a table found from rules
// are its header, given, for each row, whether it is one of the rows of
// figures that a band between two rules was split into. The evidence, from
// the surest down: a row that numbers the columns; type set in bold; a
// header ruled off over rows of figures that white space parts; and, where
// every row is ruled off alike, the text of the rows.
func ruledHeader(words []Word, t *Table, split []bool) int {
	rows := topRows(t)
	if n := numbered(t, rows); n > 0 {
		return n
	}
	if n := boldRows(words, t, rows); n > 0 {
		return n
	}
	if n := headerBands(t, split); n > 0 {
		// A line of units under the headings, "(1,000 acres)" in each
		// column, has no rule under it and falls in with the figures.
		if n < len(split) && split[n] && n+1 < len(rows) && unitRow(rows[n]) && dataCells(rows[n+1]) {
			n++
		}
		return n
	}
	return textHeader(t, rows)
}

// TextHeader returns how many rows at the top of a table read as its
// header by the text of its cells alone: the rows down to a row that
// numbers the columns, or down to the first row of figures. It is for a
// table whose rules, type and tags say nothing about its header, and is 0
// when the text says nothing either.
func TextHeader(t *Table) int {
	rows := topRows(t)
	if n := numbered(t, rows); n > 0 {
		return n
	}
	return textHeader(t, rows)
}

// bodyLook is how many rows under a header are looked at to see that they
// are rows of figures.
const bodyLook = 6

// topRows returns the cells with text that start in each of the first rows
// of a table, from the left: as many rows as a header and the look at what
// is under it may take.
func topRows(t *Table) [][]*Cell {
	if t.Rows < 2 || t.Cols < 2 {
		return nil
	}
	rows := make([][]*Cell, min(t.Rows, maxHeaderLines+1+bodyLook))
	for i := range t.Cells {
		if c := &t.Cells[i]; c.Row >= 0 && c.Row < len(rows) && c.Text != "" {
			rows[c.Row] = append(rows[c.Row], c)
		}
	}
	for _, r := range rows {
		slices.SortStableFunc(r, func(a, b *Cell) int { return a.Col - b.Col })
	}
	return rows
}

// figure reports whether the text of a cell is a figure, and not a label
// that holds one: "1,234", "12.5%", "(45)" and "$ 13,230" are figures, and
// so is a currency sign in a cell of its own. "Q4", "FY24", "Under $15,000"
// and "(1,000 acres)" are labels, and a year is as often a heading as not.
func figure(s string) bool {
	if isAmount(s) {
		return true
	}
	digits, letters, lead := 0, 0, false
	for _, r := range s {
		switch {
		case unicode.IsDigit(r):
			digits++
		case unicode.IsLetter(r):
			lead = lead || digits == 0
			letters++
		}
	}
	return digits > 0 && letters <= 1 && !lead && !isYears(s)
}

// dataCells reports whether the cells of a row read as a row of data: a
// third or more of them hold figures.
func dataCells(row []*Cell) bool {
	figures := 0
	for _, c := range row {
		if figure(c.Text) {
			figures++
		}
	}
	return len(row) > 0 && figures*3 >= len(row)
}

// columnNumber returns the number in a cell that numbers its column, as in
// "(3)" or "[3]".
func columnNumber(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if len(s) < 3 || len(s) > 5 || !(s[0] == '(' && s[len(s)-1] == ')' || s[0] == '[' && s[len(s)-1] == ']') {
		return 0, false
	}
	n := 0
	for _, r := range s[1 : len(s)-1] {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, n > 0
}

// numbered returns the number of header rows of a table that numbers its
// columns under their headings, as statistical tables do: "(1)", "(2)",
// "(3)", counting up from the left. That row is the last of the header. It
// is 0 when the table has no such row.
func numbered(t *Table, rows [][]*Cell) int {
	for r := 1; r < len(rows) && r <= maxHeaderLines && r < t.Rows-1; r++ {
		if len(rows[r]) < 2 {
			continue
		}
		last, ok := 0, true
		for i, c := range rows[r] {
			n, is := columnNumber(c.Text)
			if !is || (i > 0 && n != last+1) {
				ok = false
				break
			}
			last = n
		}
		if ok {
			return r + 1
		}
	}
	return 0
}

// unitRow reports whether a row is a line of units under the headings: it
// has nothing in the first column, and what it has under the others is
// words, with or without a figure among them, and no figure alone.
func unitRow(row []*Cell) bool {
	if len(row) < 2 || row[0].Col == 0 {
		return false
	}
	for _, c := range row {
		if figure(c.Text) || strings.IndexFunc(c.Text, unicode.IsLetter) < 0 {
			return false
		}
	}
	return true
}

// boldRows returns the number of header rows of a table whose first row is
// set in bold and whose rows are not in the main. Where the rows under the
// first are bold as well, they are header rows down to the first row of
// figures: a row of totals may be bold too.
func boldRows(words []Word, t *Table, rows [][]*Cell) int {
	if headerRows(words, t) == 0 {
		return 0
	}
	limit := min(len(rows), maxHeaderLines, t.Rows-1)
	h := 1
	for h < limit && !dataCells(rows[h]) {
		bold := true
		for _, c := range rows[h] {
			for _, i := range c.Words {
				bold = bold && words[i].Bold
			}
		}
		if !bold {
			break
		}
		h++
	}
	return max(1, headerEnd(t, rows, h))
}

// textHeader returns the number of rows at the top of a table down to its
// first row of figures, when the rows that follow are rows of figures too
// and what stands above them reads as the headings of the columns.
func textHeader(t *Table, rows [][]*Cell) int {
	first := 0
	for first < len(rows) && first <= maxHeaderLines && !dataCells(rows[first]) {
		first++
	}
	if first == 0 || first >= len(rows) || first > maxHeaderLines {
		return 0
	}
	// The rows under it: those that hold more than a label are rows of
	// figures, two in three or more.
	looked, data := 0, 0
	for r := first; r < min(len(rows), first+bodyLook); r++ {
		if len(rows[r]) < 2 {
			continue
		}
		looked++
		if dataCells(rows[r]) {
			data++
		}
	}
	if data*3 < looked*2 {
		return 0
	}
	h := headerEnd(t, rows, first)
	if h == 0 {
		return 0
	}
	// A row of words under the first, with text in the first column, may
	// as well be the first row of data, one that holds no figure, as be
	// the row of headings that has the first column's. It is the row of
	// headings when the rows above it head groups of columns: each has a
	// cell merged across columns, or leaves empty a column, other than the
	// first, that a row under it fills.
	for k := 1; k < h; k++ {
		if len(rows[k]) == 0 || rows[k][0].Col != 0 {
			continue
		}
		for r := 0; r < k; r++ {
			covered := make([]bool, t.Cols)
			groups := false
			for _, c := range rows[r] {
				groups = groups || c.ColSpan > 1
				for col := max(c.Col, 0); col < min(c.Col+max(c.ColSpan, 1), t.Cols); col++ {
					covered[col] = true
				}
			}
			for under := r + 1; under < h && !groups; under++ {
				for _, c := range rows[under] {
					groups = groups || (c.Col > 0 && c.Col < t.Cols && !covered[c.Col])
				}
			}
			if !groups {
				h = k
				break
			}
		}
		break
	}
	// The headings must be headings of the columns: between them, the
	// header rows have text over most of the columns the rows under them
	// fill.
	headed, filled := make([]bool, t.Cols), make([]bool, t.Cols)
	for r, row := range rows {
		for _, c := range row {
			if c.ColSpan >= t.Cols {
				continue // a title across the table heads no column
			}
			for col := max(c.Col, 0); col < min(c.Col+max(c.ColSpan, 1), t.Cols); col++ {
				if r < h {
					headed[col] = true
				} else if c.ColSpan <= 1 {
					filled[col] = true
				}
			}
		}
	}
	body, over := 0, 0
	for col := range filled {
		if filled[col] {
			body++
			if headed[col] {
				over++
			}
		}
	}
	if body == 0 || over*5 < body*3 {
		return 0
	}
	return h
}

// headerEnd cuts back a header of h rows to the rows that are headings of
// the columns. A row just above the rows of figures with text in its first
// cell only names the rows that follow. And the first column has one
// heading: a second row with text there is a row of the table, whose other
// cells happen to hold no figure.
func headerEnd(t *Table, rows [][]*Cell, h int) int {
	h = min(h, len(rows), t.Rows-1)
	for h > 0 && len(rows[h-1]) == 1 && rows[h-1][0].Col == 0 {
		h--
	}
	labelled := false
	for r := 0; r < h; r++ {
		if len(rows[r]) == 0 || rows[r][0].Col != 0 || rows[r][0].ColSpan >= t.Cols {
			continue // a title across the table is not the first column's
		}
		if labelled {
			return r
		}
		labelled = true
	}
	return h
}
