// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package table

import (
	"cmp"
	"math"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Line is a line of the page: its words, left to right, as indices into the
// page's words.
type Line struct {
	Words          []int
	X0, Y0, X1, Y1 float64
}

const (
	// cellGap is the gap between two words on a line, in font sizes, beyond
	// which they are in different cells.
	cellGap = 1.5
	// minRows is the least number of rows that make a table without rules.
	minRows = 3
	// rowPitch is the greatest distance between two rows, in font sizes.
	rowPitch = 3.0
	// maxRows is the most lines a table without rules may have.
	maxRows = 1000
	// maxLead is the most lines with no gap that may follow the first line
	// of a table before a second line shows its columns.
	maxLead = 40
)

// Aligned finds tables that have no grid of ruling lines: runs of
// consecutive lines whose words fall into the same columns. lines must be
// in reading order. Horizontal rules, which such tables often have under
// the header and between rows, help to tell the rows apart.
//
// It returns the tables and, for each line, the index of the table it
// became part of, or -1.
func Aligned(words []Word, lines []Line, rules []Rule) ([]Table, []int) {
	member := make([]int, len(lines))
	for i := range member {
		member[i] = -1
	}
	if len(rules) > maxRules {
		rules = rules[:maxRules]
	}
	hs, _ := split(append([]Rule(nil), rules...))

	// A line is tabular when it has a gap wide enough to separate cells.
	// Lines made by reading across columns of vertical text are no lines at
	// all, and are left out of the search as text at an angle is.
	upright := stacked(words, lines)
	tabular := make([]bool, len(lines))
	for i, l := range lines {
		tabular[i] = !upright[i] && gapped(words, l)
	}

	var tables []Table
	for start := 0; start < len(lines); {
		if !tabular[start] {
			start++
			continue
		}
		// Extend the run while lines follow each other closely. A line
		// with no wide gap may still be part of the table: the next line
		// of a cell that runs to several, which keeps within one column.
		// A line of prose does not: it runs across the columns, and ends
		// the table. Until two lines have shown where the columns are,
		// a line or two with no gap is let through, and as many lines of
		// running text as keep under one cell of the first line: the rest
		// of a cell that began on the page before, under the header the
		// page repeats.
		end := start + 1
		tab := []int{start}
		var columns [][2]float64
		sepsFor := 0
		x0, x1, size := extent(words, lines[start:end])
		first := cellStarts(words, lines[start])
		loose := 0     // lines let through that keep under no one cell
		prose := false // the line before was running text under a cell
		for end < len(lines) && end-start < maxRows {
			prev, cur := lines[end-1], lines[end]
			sz := lineSize(words, cur)
			if upright[end] || cur.Y0 < prev.Y0-sz || cur.Y0-prev.Y1 > rowPitch*sz {
				break
			}
			if tabular[end] {
				tab = append(tab, end)
				end++
				continue
			}
			if len(tab) < 2 {
				// Running text has several words to a line, but for the
				// last line of a paragraph.
				prose = under(cur, first, sz) && (len(cur.Words) >= 3 || prose)
				if !prose {
					if loose++; loose > 2 {
						break
					}
				}
				if end-tab[0] > maxLead {
					break
				}
				end++
				continue
			}
			if sepsFor == 0 || len(tab) >= sepsFor+sepsFor/4+1 {
				sample := make([]Line, 0, len(tab))
				for _, i := range tab[max(0, len(tab)-60):] {
					sample = append(sample, lines[i])
				}
				x0, x1, size = extent(words, sample)
				columns, sepsFor = columnExtents(words, sample, separators(words, sample, x0, x1, size)), len(tab)
			}
			if cur.X0 < x0-sz || cur.X1 > x1+sz || bridges(words, cur, columns) {
				break
			}
			end++
		}
		// Lines with no gap at the end are kept only if they sit close
		// under the table, as the last lines of a cell do.
		for end-1 > tab[len(tab)-1] {
			last, before := lines[end-1], lines[end-2]
			if len(tab) >= 2 && last.Y0-before.Y1 <= 1.5*lineSize(words, last) {
				break
			}
			end--
		}
		if len(tab) < 2 {
			end = tab[len(tab)-1] + 1
		}
		if t, ok := align(words, lines[start:end], hs); ok {
			for i := start - leadLines(words, lines, start, member, &t, hs); i < end; i++ {
				member[i] = len(tables)
			}
			tables = append(tables, t)
		}
		start = end
	}
	return tables, member
}

// leadLines takes into a table the lines of its header that stand above
// the first line found: lines[start]. A table starts at the first line
// with a gap wide enough to part two cells, and a header may begin higher,
// with a line that has one thing on it: the heading of a group of columns,
// with a rule under it over those columns, or the top line of a column's
// heading that is a line longer than the others, "Common" over "stock".
// The title of a table is neither: it has no rule under it that stops
// short of the first column, and it does not fit over one column.
//
// The heading of a group becomes a header row of its own, and the top line
// of a column's heading is added to that heading. It returns the number of
// lines taken, which are the lines just before start.
func leadLines(words []Word, lines []Line, start int, member []int, t *Table, hs []hline) int {
	if t.HeaderRows < 1 || t.Cols < 2 {
		return 0
	}
	// Where the text of each column lies.
	edges := make([][2]float64, t.Cols)
	for c := range edges {
		edges[c] = [2]float64{math.Inf(1), math.Inf(-1)}
	}
	for _, c := range t.Cells {
		if c.ColSpan != 1 || c.Col < 0 || c.Col >= t.Cols {
			continue
		}
		for _, i := range c.Words {
			edges[c.Col][0], edges[c.Col][1] = math.Min(edges[c.Col][0], words[i].X0), math.Max(edges[c.Col][1], words[i].X1)
		}
	}
	taken, grouped := 0, false
	for k := start - 1; k >= 0 && taken < maxHeaderLines && member[k] < 0; k-- {
		l := lines[k]
		size := lineSize(words, l)
		if len(l.Words) == 0 || l.Y1 > t.Y0+0.3*size || t.Y0-l.Y1 > 1.5*size || l.X1 > t.X1+size || l.X0 < edges[0][1] {
			break
		}
		// Nothing else may stand beside it: the table's box grows to take
		// the line in, and all that is in the box is the table's.
		alone := true
		for j, o := range lines {
			my := (o.Y0 + o.Y1) / 2
			if j != k && member[j] < 0 && (j < start-taken || j >= start) && my > l.Y0 && my < t.Y0 && o.X1 > t.X0 && o.X0 < t.X1 {
				alone = false
				break
			}
		}
		if !alone {
			break
		}
		if a, b, ok := ruledGroup(l, t, edges, hs, size); ok {
			for i := range t.Cells {
				t.Cells[i].Row++
			}
			row := make([]Cell, 0, t.Cols)
			for c := 0; c < t.Cols; c++ {
				cell := Cell{Row: 0, Col: c, RowSpan: 1, ColSpan: 1, Y0: l.Y0, Y1: l.Y1}
				for _, o := range t.Cells {
					if o.Col == c && o.ColSpan == 1 {
						cell.X0, cell.X1 = o.X0, o.X1
						break
					}
				}
				switch {
				case c == a:
					cell.ColSpan, cell.Words, cell.Text = b-a+1, l.Words, cellText(words, l.Words)
				case c > a && c <= b:
					continue
				}
				row = append(row, cell)
			}
			for i := range row {
				if row[i].Col == a {
					for _, o := range t.Cells {
						if o.Col == b && o.ColSpan == 1 {
							row[i].X1 = o.X1
							break
						}
					}
				}
			}
			t.Cells = append(row, t.Cells...)
			t.Rows++
			t.HeaderRows++
			grouped = true
		} else if cell := headingTop(words, l, t, size); cell != nil && !grouped {
			cell.Words = append(slices.Clone(l.Words), cell.Words...)
			cell.Text = cellText(words, cell.Words)
			for i := range t.Cells {
				if t.Cells[i].Row == cell.Row {
					t.Cells[i].Y0 = math.Min(t.Cells[i].Y0, l.Y0)
				}
			}
		} else {
			break
		}
		t.Y0 = math.Min(t.Y0, l.Y0)
		taken++
	}
	return taken
}

// ruledGroup reports whether a line just above a table is the heading of a
// group of its columns: a rule under the line, between it and the table,
// runs under the whole of it and over two or more columns of the table,
// the first column not among them. It returns the columns.
func ruledGroup(l Line, t *Table, edges [][2]float64, hs []hline, size float64) (a, b int, ok bool) {
	for _, h := range hs {
		if h.y < l.Y1-0.3*size || h.y > t.Y0+0.3*size {
			continue
		}
		if math.Min(h.x1, l.X1)-math.Max(h.x0, l.X0) < 0.8*(l.X1-l.X0) || l.X0 < h.x0-size || l.X1 > h.x1+size {
			continue
		}
		a, b = -1, -1
		for c := range edges {
			width := edges[c][1] - edges[c][0]
			if width <= 0 || math.Min(h.x1, edges[c][1])-math.Max(h.x0, edges[c][0]) < 0.5*width {
				continue
			}
			if a < 0 {
				a = c
			}
			b = c
		}
		if a >= 1 && b > a {
			return a, b, true
		}
	}
	return 0, 0, false
}

// headingTop returns the header cell that a line just above a table is the
// top line of: the highest heading of one column, when the line fits in
// that column, is set as close above the heading as the lines of a heading
// are, in the same size, and is centred or flush as the line under it is.
// A note in brackets above a column, "(in millions)", is not a line of its
// heading, and no column is the first.
func headingTop(words []Word, l Line, t *Table, size float64) *Cell {
	if len(l.Words) > 4 || strings.HasPrefix(words[l.Words[0]].Text, "(") {
		return nil
	}
	for _, i := range l.Words {
		if isNumeric(words[i].Text) {
			return nil
		}
	}
	var cell *Cell
	for i := range t.Cells {
		c := &t.Cells[i]
		if c.Row >= t.HeaderRows {
			break
		}
		if c.Col >= 1 && c.ColSpan == 1 && len(c.Words) > 0 && l.X0 >= c.X0-1 && l.X1 <= c.X1+1 {
			cell = c
			break
		}
	}
	if cell == nil {
		return nil
	}
	// The top line of the heading as it stands.
	top := math.Inf(1)
	for _, i := range cell.Words {
		top = math.Min(top, words[i].Y0)
	}
	x0, x1 := math.Inf(1), math.Inf(-1)
	for _, i := range cell.Words {
		if w := words[i]; w.Y0 < top+0.5*size {
			x0, x1 = math.Min(x0, w.X0), math.Max(x1, w.X1)
			if math.Abs(w.Size-size) > 0.5 {
				return nil
			}
		}
	}
	if gap := top - l.Y1; gap > 0.6*size || gap < -0.3*size {
		return nil
	}
	if math.Abs((l.X0+l.X1)/2-(x0+x1)/2) > 0.5*size && math.Abs(l.X0-x0) > 1 && math.Abs(l.X1-x1) > 1 {
		return nil
	}
	return cell
}

// gapped reports whether a line has a gap wide enough to part two cells.
func gapped(words []Word, l Line) bool {
	for j := 1; j < len(l.Words); j++ {
		a, b := words[l.Words[j-1]], words[l.Words[j]]
		if b.X0-a.X1 > cellGap*math.Max(a.Size, b.Size) {
			return true
		}
	}
	return false
}

// stacked reports, for each line, whether it is a row of characters cut
// from vertical text: text written down the page in columns, each character
// set upright by itself, as Chinese and Japanese are. Nothing in such a
// page says which way it reads, and taken line by line across the page it
// looks like a table with a character to a cell: the columns of the text
// line up as the columns of a table do.
//
// What gives it away is the spacing. A character stands right under the
// one before it, a type size further down, and the columns stand apart, so
// that every "word" of a line is one character and the lines follow each
// other with no space between them. A table in the same script has words
// of several characters in its cells, and space between its rows. Three
// such lines in a row are asked for: one line of single characters may be
// the header of a table, "Mon Tue Wed" in one character each.
func stacked(words []Word, lines []Line) []bool {
	out := make([]bool, len(lines))
	single := func(l Line) bool {
		n := 0
		for _, i := range l.Words {
			r, size := utf8.DecodeRuneInString(words[i].Text)
			if size == len(words[i].Text) && upright(r) {
				n++
			}
		}
		// A column set closer than the others may have joined two
		// characters into a word here and there.
		return n > 0 && n*5 >= len(l.Words)*4
	}
	for i := 0; i < len(lines); {
		if !single(lines[i]) {
			i++
			continue
		}
		j := i + 1
		for j < len(lines) && single(lines[j]) {
			prev, cur := lines[j-1], lines[j]
			if pitch := cur.Y0 - prev.Y0; pitch < 0 || pitch > 1.25*lineSize(words, cur) {
				break
			}
			j++
		}
		if j-i >= 3 {
			for k := i; k < j; k++ {
				out[k] = true
			}
		}
		i = j
	}
	return out
}

// upright reports a character of a script that is written down the page
// with its characters upright: Chinese characters, kana and hangul, and the
// punctuation and full-width forms set with them.
func upright(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul) ||
		r >= 0x3000 && r <= 0x303F || r >= 0xFF00 && r <= 0xFFEF
}

// groupNames reports, for each line, whether it names a group of the rows
// below it from over the columns of figures ("by kind of proceedings"): a
// line of words, with no gap wide enough to part cells, that comes after a
// row of figures, begins to the right of every row's first cell, and runs
// across a gap that the other lines leave between two columns. Such a line
// says nothing of where the columns are, and is one cell.
//
// Lines of one phrase that begin under the first cells are left alone:
// the further lines of a label, or a group's name set as a label. So are
// those above the first row of figures: they are the header's, whose
// phrases across columns are headings of groups of columns. And so are
// lines that keep within a column, as the further lines of a cell do.
func groupNames(words []Word, lines []Line, x0, x1, size float64) []bool {
	named := candidates(words, lines)
	var others []Line
	for li, l := range lines {
		if !named[li] {
			others = append(others, l)
		}
	}
	if len(others) == len(lines) || len(others) < 2 {
		return make([]bool, len(lines))
	}
	seps := separators(words, others, x0, x1, size)
	for li, l := range lines {
		if !named[li] {
			continue
		}
		crosses := false
		for _, i := range l.Words {
			w := words[i]
			if isNumeric(w.Text) {
				crosses = false
				break
			}
			for _, s := range seps {
				crosses = crosses || (w.X0 < s && w.X1 > s)
			}
		}
		named[li] = crosses
	}
	return named
}

// candidates returns the lines that may name a group of rows from over the
// columns of figures, by where they stand: lines of one phrase, after a row
// of figures, that begin to the right of every row's first cell.
func candidates(words []Word, lines []Line) []bool {
	named := make([]bool, len(lines))
	labels := math.Inf(-1) // where the first cells of the rows with gaps end
	for _, l := range lines {
		if !gapped(words, l) {
			continue
		}
		starts := cellStarts(words, l)
		for _, i := range l.Words {
			if w := words[i]; w.X0 < starts[1] {
				labels = math.Max(labels, w.X1)
			}
		}
	}
	figures := false
	for li, l := range lines {
		if !gapped(words, l) {
			named[li] = figures && l.X0 > labels
			continue
		}
		for _, i := range l.Words {
			if isNumeric(words[i].Text) {
				figures = true
				break
			}
		}
	}
	return named
}

// figureRow reports whether a line is a row of figures: after its first
// cell it holds figures, currency signs and dashes only, and a figure among
// them.
func figureRow(words []Word, l Line) bool {
	starts := cellStarts(words, l)
	if len(starts) < 2 {
		return false
	}
	figures := false
	for _, i := range l.Words {
		w := words[i]
		if w.X0 < starts[1] {
			continue
		}
		if !figureish(w.Text) {
			return false
		}
		figures = figures || isNumeric(w.Text)
	}
	return figures
}

// figureish reports a figure, a currency sign or a dash: what a cell of
// figures holds.
func figureish(s string) bool {
	return isNumeric(s) || isAmount(s) || isDash(s)
}

// splitFigures parts the columns of figures that no white gap parts.
// Headings centred over narrow columns, and phrases over a group of them,
// can cover the white space between the columns, and so can currency signs
// set between them. But figures are set flush right, so the right edges of
// a column's figures line up, whatever stands above them. A column that
// holds figures in the main, and two or more such edges each shared by
// several lines, is that many columns, parted just to the right of each
// edge but the last. A currency sign set before the next figure then falls
// into that figure's column, where it belongs. Only lines of a table whose
// white space shows some columns are parted further.
//
// It returns the separators, and for each whether it was found here.
func splitFigures(words []Word, lines []Line, seps []float64, x0, x1, size float64) ([]float64, []bool) {
	if len(seps) == 0 {
		// White space shows no columns at all: no table to part further.
		return seps, nil
	}
	bounds := make([]float64, 0, len(seps)+2)
	bounds = append(append(append(bounds, math.Inf(-1)), seps...), math.Inf(1))
	var out []float64
	var byEdges []bool
	type edge struct {
		x, left float64 // the edge, and where its widest figure begins
		lines   map[int]bool
	}
	for c := 0; c+1 < len(bounds); c++ {
		if c > 0 {
			out, byEdges = append(out, bounds[c]), append(byEdges, false)
		}
		// The words of the column, and the right edges of its figures.
		// It must be a column of figures: most of the lines that have a
		// figure in it have nothing else there but currency signs and
		// dashes, unlike "3x3 conv, 64" or a heading "December 31,".
		var ends [][2]int // line and word of the figures of lines that hold nothing else here
		data := map[int]bool{}
		filled, figures := 0, 0
		for li, l := range lines {
			only, at := true, len(ends)
			for _, i := range l.Words {
				w := words[i]
				if m := (w.X0 + w.X1) / 2; m < bounds[c] || m >= bounds[c+1] {
					continue
				}
				switch {
				case isFigure(w.Text):
					ends = append(ends, [2]int{li, i})
					data[li] = true
				case !isAmount(w.Text) && !isDash(w.Text):
					only = false
				}
			}
			if data[li] {
				filled++
				if only {
					figures++
				} else {
					ends = ends[:at] // "18 years": no edge of a figure
				}
			}
		}
		if len(ends) < 4 || figures*3 < filled*2 {
			continue
		}
		sort.Slice(ends, func(a, b int) bool { return words[ends[a][1]].X1 < words[ends[b][1]].X1 })
		// Edges a closing bracket's width apart are one: negative figures
		// in brackets hang the bracket past the edge.
		var edges []edge
		for _, e := range ends {
			w := words[e[1]]
			if n := len(edges); n > 0 && w.X1-edges[n-1].x <= 0.5*size {
				edges[n-1].x = w.X1
				edges[n-1].left = math.Min(edges[n-1].left, w.X0)
				edges[n-1].lines[e[0]] = true
				continue
			}
			edges = append(edges, edge{w.X1, w.X0, map[int]bool{e[0]: true}})
		}
		need := max(2, min(3, len(data)/2))
		var kept []edge
		for _, e := range edges {
			if len(e.lines) >= need {
				kept = append(kept, e)
			}
		}
		for k := 1; k < len(kept); k++ {
			if kept[k].left-kept[k-1].x < 0.6*size {
				continue
			}
			// Just to the right of the edge, or of a figure that hangs
			// past it ("2020*" over its column): the first place before
			// the next column's figures that no figure of a cell of
			// figures stands across. Words may, and figures among words
			// ("for 2014 – 2023 accident years"): the headings and labels
			// set across the columns.
			places := []float64{kept[k-1].x}
			for _, e := range ends {
				if w := words[e[1]]; w.X1 > kept[k-1].x && w.X1 < kept[k].left {
					places = append(places, w.X1)
				}
			}
			slices.Sort(places)
			for _, x := range places {
				at := x + 0.25*size
				if at >= kept[k].left {
					break
				}
				across := false
				for _, e := range ends {
					w := words[e[1]]
					across = across || (w.X0 < at && w.X1 > at)
				}
				if !across {
					out, byEdges = append(out, at), append(byEdges, true)
					break
				}
			}
		}
	}
	return out, byEdges
}

// runsOnAcross reports whether two words next to each other on a line read
// on as a phrase across columns that only the edges of figures part: the
// space between them is a space between words, and they are not two
// figures.
func runsOnAcross(words []Word, a, b int, byEdges []bool, from, to int) bool {
	wa, wb := words[a], words[b]
	if isFigure(wa.Text) && isFigure(wb.Text) {
		return false
	}
	if gap := wb.X0 - wa.X1; gap < -1 || gap > 0.5*math.Max(wa.Size, wb.Size) {
		return false
	}
	for k := from; k < to; k++ {
		if !byEdges[k] {
			return false
		}
	}
	return true
}

// signsBefore reports whether a table sets its currency signs before the
// figures they belong to: some cell begins with a sign and holds a figure,
// or a cell that holds only a sign, after one that does not end in a
// figure, stands before a cell that begins with one. Tables that write
// "12,50 €" do neither.
func signsBefore(words []Word, rows []row) bool {
	sorted := func(idx []int) []int {
		out := slices.Clone(idx)
		slices.SortFunc(out, func(a, b int) int { return cmp.Compare(words[a].X0, words[b].X0) })
		return out
	}
	for _, rw := range rows {
		for c, idx := range rw.cells {
			if len(idx) == 0 {
				continue
			}
			ws := sorted(idx)
			if !isAmount(words[ws[0]].Text) || isNumeric(words[ws[0]].Text) {
				continue
			}
			if len(ws) > 1 && isNumeric(words[ws[1]].Text) {
				return true
			}
			if len(ws) == 1 && c+1 < len(rw.cells) && len(rw.cells[c+1]) > 0 && isNumeric(words[sorted(rw.cells[c+1])[0]].Text) {
				if c == 0 || len(rw.cells[c-1]) == 0 || !isNumeric(words[sorted(rw.cells[c-1])[len(rw.cells[c-1])-1]].Text) {
					return true
				}
			}
		}
	}
	return false
}

// moveSigns moves a currency sign at the end of a cell of figures to the
// start of the next cell, in a table that sets its signs before the
// figures. Financial tables set the sign of each amount apart, and often
// nearer to the amount on its left than to its own.
func moveSigns(words []Word, cells [][]int) {
	for c := 0; c+1 < len(cells); c++ {
		idx := cells[c]
		if len(idx) < 2 {
			continue
		}
		last, figures := idx[0], true
		for _, i := range idx {
			if words[i].X1 > words[last].X1 {
				last = i
			}
			figures = figures && figureish(words[i].Text)
		}
		if !figures || isNumeric(words[last].Text) || !isAmount(words[last].Text) {
			continue
		}
		kept := make([]int, 0, len(idx)-1)
		for _, i := range idx {
			if i != last {
				kept = append(kept, i)
			}
		}
		cells[c] = kept
		cells[c+1] = append([]int{last}, cells[c+1]...)
	}
}

// cellStarts returns where each cell of a line begins: the left edge of
// the line and of every word that follows a gap wide enough to part cells.
func cellStarts(words []Word, l Line) []float64 {
	var starts []float64
	for j, i := range l.Words {
		w := words[i]
		if j == 0 {
			starts = append(starts, w.X0)
			continue
		}
		if a := words[l.Words[j-1]]; w.X0-a.X1 > cellGap*math.Max(a.Size, w.Size) {
			starts = append(starts, w.X0)
		}
	}
	return starts
}

// under reports whether a line keeps under one cell of the line whose
// cells begin at starts: it begins where that cell does, or further in,
// and ends before the next cell begins.
func under(l Line, starts []float64, size float64) bool {
	for k, x := range starts {
		if l.X0 < x-0.5*size {
			break
		}
		if k+1 == len(starts) || l.X1 < starts[k+1]-0.5*size {
			return true
		}
	}
	return false
}

func lineSize(words []Word, l Line) float64 {
	size := 0.0
	for _, i := range l.Words {
		size = math.Max(size, words[i].Size)
	}
	if size == 0 {
		size = l.Y1 - l.Y0
	}
	return size
}

// columnExtents returns where the text of each column lies in some lines,
// given the middles of the gaps between the columns.
func columnExtents(words []Word, lines []Line, seps []float64) [][2]float64 {
	out := make([][2]float64, len(seps)+1)
	for c := range out {
		out[c] = [2]float64{math.Inf(1), math.Inf(-1)}
	}
	for _, l := range lines {
		for _, i := range l.Words {
			w := words[i]
			c := sort.SearchFloat64s(seps, (w.X0+w.X1)/2)
			out[c][0], out[c][1] = math.Min(out[c][0], w.X0), math.Max(out[c][1], w.X1)
		}
	}
	return out
}

// bridges reports whether a line with no wide gap in it runs across
// columns: it has words where the text of two of them lies, and reads on
// from the one into the other. A label too long for its column runs into
// the gap beside it, but not into the next column's text. And a line that
// has text in two columns, a long label beside the start of the next cell,
// keeps them apart all the same: the space where the next column begins is
// wider than a space between words, and wider than its other spaces.
func bridges(words []Word, l Line, columns [][2]float64) bool {
	last := -1 // the last column the line has words in
	for c, col := range columns {
		touches := false
		for _, i := range l.Words {
			if w := words[i]; w.X0 < col[1] && w.X1 > col[0] {
				touches = true
				break
			}
		}
		if !touches {
			continue
		}
		if last >= 0 && !parts(words, l, col[0]) {
			return true
		}
		last = c
	}
	return false
}

// parts reports whether a line breaks off where a column begins, at x: the
// space there is no space between two words of a phrase.
func parts(words []Word, l Line, x float64) bool {
	at, widest := -1.0, 0.0 // the space at x, and the widest of the others
	for j := 1; j < len(l.Words); j++ {
		a, b := words[l.Words[j-1]], words[l.Words[j]]
		gap := b.X0 - a.X1
		size := math.Max(a.Size, b.Size)
		switch {
		case a.X1 > x+0.5*size || b.X0 < x-0.5*size:
			// A space within a column. One wide enough to part cells is
			// the gap before another column, not a space of the phrase.
			if gap <= cellGap*size {
				widest = math.Max(widest, gap)
			}
		case at < 0:
			at = gap
			if gap < 0.6*size {
				return false
			}
		}
	}
	return at >= 0 && at >= 1.5*widest
}

// extent returns the horizontal extent of some lines and their largest
// type size.
func extent(words []Word, lines []Line) (x0, x1, size float64) {
	x0, x1 = math.Inf(1), math.Inf(-1)
	for _, l := range lines {
		x0, x1 = math.Min(x0, l.X0), math.Max(x1, l.X1)
		size = math.Max(size, lineSize(words, l))
	}
	return x0, x1, size
}

// separators returns the middle of each column gap in some lines: each
// stretch of their width that almost none of them has a word over.
func separators(words []Word, lines []Line, x0, x1, size float64) []float64 {
	width := int(math.Ceil(x1 - x0))
	if width < 20 || width > 5000 || size <= 0 {
		return nil
	}
	// For every point along the width, count the lines with a word over it.
	cover := make([]int, width+1)
	seen := make([]bool, width+1)
	for _, l := range lines {
		clear(seen)
		for _, i := range l.Words {
			w := words[i]
			for x := int(w.X0 - x0); x <= int(w.X1-x0) && x <= width; x++ {
				if x >= 0 {
					seen[x] = true
				}
			}
		}
		for x, s := range seen {
			if s {
				cover[x]++
			}
		}
	}
	allowed := len(lines) / 8 // a spanning line or two may cross a gap
	minGap := int(math.Max(4, 0.6*size))
	var seps []float64
	for x := 0; x <= width; {
		if cover[x] > allowed {
			x++
			continue
		}
		from := x
		for x <= width && cover[x] <= allowed {
			x++
		}
		if from > 0 && x <= width && x-from >= minGap {
			seps = append(seps, x0+float64(from+x)/2)
		}
	}
	return seps
}

// row is a row of a table being built: the words of each of its cells.
type row struct {
	cells  [][]int
	y0, y1 float64
	// spans holds, for a header row with headings of groups of columns,
	// how many columns each cell covers: 0 or 1 for a cell of one column.
	spans []int
}

func (r *row) filled() int {
	n := 0
	for _, c := range r.cells {
		if len(c) > 0 {
			n++
		}
	}
	return n
}

// absorb adds the cells of another row to this one's.
func (r *row) absorb(o row) {
	for c := range o.cells {
		r.cells[c] = append(r.cells[c], o.cells[c]...)
	}
	r.y0, r.y1 = math.Min(r.y0, o.y0), math.Max(r.y1, o.y1)
}

// align builds a table from a run of lines, if their words share columns.
func align(words []Word, lines []Line, hs []hline) (Table, bool) {
	if len(lines) < minRows || len(lines) > maxRows {
		return Table{}, false
	}
	x0, x1, size := extent(words, lines)
	named := groupNames(words, lines, x0, x1, size)
	gapLines := make([]Line, 0, len(lines))
	for li, l := range lines {
		if !named[li] {
			gapLines = append(gapLines, l)
		}
	}
	seps, byEdges := splitFigures(words, gapLines, separators(words, gapLines, x0, x1, size), x0, x1, size)
	if len(seps) == 0 || len(seps) > 60 {
		return Table{}, false
	}
	cols := len(seps) + 1
	edged := 0 // columns told apart by the edges of their figures
	for _, e := range byEdges {
		if e {
			edged++
		}
	}
	head := len(lines) // the lines above the first row of figures
	for li, l := range lines {
		if figureRow(words, l) {
			head = li
			break
		}
	}

	// The cells of each line.
	rows := make([]row, len(lines))
	spanning := 0
	for li, l := range lines {
		cells := make([][]int, cols)
		crosses := false
		for _, i := range l.Words {
			w := words[i]
			for k, s := range seps {
				// The edges of figures part figures only. A heading over a
				// group of columns, or a label set across them, may run
				// across them, as it should.
				crosses = crosses || (w.X0 < s && w.X1 > s && !byEdges[k])
			}
		}
		at := -1 // the one cell of a line that names a group of rows
		if crosses && named[li] && len(l.Words) > 0 {
			w := words[l.Words[0]]
			at = sort.SearchFloat64s(seps, (w.X0+w.X1)/2)
		}
		prev, prevCell := -1, -1 // the word before, and its cell
		for _, i := range l.Words {
			c := at
			if c < 0 {
				w := words[i]
				c = sort.SearchFloat64s(seps, (w.X0+w.X1)/2)
				// Below the header, a phrase set across the edges of
				// figures, such as a label for a total, stays one cell.
				// The header's phrases are joined later, as headings of
				// groups of columns.
				if prev >= 0 && li >= head && c > prevCell && runsOnAcross(words, prev, i, byEdges, prevCell, c) {
					c = prevCell
				}
			}
			cells[c] = append(cells[c], i)
			prev, prevCell = i, c
		}
		if crosses {
			spanning++
		}
		rows[li] = row{cells: cells, y0: l.Y0, y1: l.Y1}
	}
	if signsBefore(words, rows) {
		for _, rw := range rows {
			moveSigns(words, rw.cells)
		}
	}
	rows, cols = mergeAffixes(words, rows, cols)
	if cols < 2 {
		return Table{}, false
	}

	// Rows. Where the cells hold short entries, every line is a row. Where
	// a column holds prose that wraps, a row takes several lines, and
	// something other than the lines has to say where rows end: rules
	// between them, or wider gaps, or failing both the text itself, by how
	// the first column starts its entries. Without any of these the lines
	// cannot be made into rows with any confidence.
	y0, y1 := rows[0].y0, rows[len(rows)-1].y1
	var between []float64 // rules that cross the table
	bounding := 0         // rules across its width, inside it or at its edges
	for _, h := range hs {
		over := math.Min(h.x1, x1) - math.Max(h.x0, x0)
		if over < 0.5*(x1-x0) || h.y < y0-1.5*size || h.y > y1+1.5*size {
			continue
		}
		if over >= 0.7*(x1-x0) {
			bounding++
		}
		if h.y > y0 && h.y < y1 {
			between = append(between, h.y)
		}
	}
	unsure := false
	if wraps(words, rows, cols) {
		switch byRules, byGaps := rowsByRules(rows, between), rowsByGaps(rows, size); {
		case len(byRules) >= 2 && len(byRules) < len(rows):
			rows = byRules
		case len(byGaps) >= 2 && len(byGaps) < len(rows):
			rows = byGaps
		default:
			if byText, ok := rowsByText(words, rows, cols, size); ok {
				rows = byText
			} else {
				unsure = true
			}
		}
	} else {
		rows = mergeContinuations(words, rows, cols, size)
	}
	if len(rows) < minRows && !(len(rows) == 2 && bounding >= 2) {
		return Table{}, false
	}

	// The header's lines become one row for each level of headings. The
	// lines still count as evidence of a table.
	asLines, lineCount, header := rows, len(rows), 0
	if !unsure {
		rows, header = joinHeader(words, rows, cols, hs)
	}

	t := Table{X0: x0, X1: x1, Y0: y0, Y1: y1, Rows: len(rows), Cols: cols, Method: "alignment"}
	edges := columnEdges(words, rows, cols)
	bounds := make([]float64, cols+1)
	bounds[0], bounds[cols] = x0, x1
	for c := 1; c < cols; c++ {
		bounds[c] = (edges[c-1][1] + edges[c][0]) / 2
	}
	// How full the rows are and how many cells hold figures are counted on
	// the lines as they stood, so that joining the header's lines leaves
	// the confidence where it was.
	well, numeric, nonEmpty := 0, 0, 0
	need := max(2, (cols*3+4)/5)
	count := func(rw row, text func(c int) string) {
		if rw.filled() >= need {
			well++
		}
		for c := range rw.cells {
			if s := text(c); s != "" {
				nonEmpty++
				if isNumeric(s) {
					numeric++
				}
			}
		}
	}
	joined := lineCount != len(rows)
	if joined {
		for _, rw := range asLines[:lineCount-(len(rows)-header)] {
			count(rw, func(c int) string { return cellText(words, rw.cells[c]) })
		}
	}
	texts := make([]string, cols)
	for r, rw := range rows {
		covered := 0 // columns still under a heading merged across them
		for c, idx := range rw.cells {
			texts[c] = cellText(words, idx)
			if covered > 0 {
				covered--
				continue
			}
			span := 1
			if rw.spans != nil && rw.spans[c] > 1 {
				span = min(rw.spans[c], cols-c)
				covered = span - 1
			}
			t.Cells = append(t.Cells, Cell{Row: r, Col: c, RowSpan: 1, ColSpan: span, X0: bounds[c], X1: bounds[c+span], Y0: rw.y0, Y1: rw.y1, Words: idx, Text: texts[c]})
		}
		if !joined || r >= header {
			count(rw, func(c int) string { return texts[c] })
		}
	}

	// Confidence. Alignment alone is weak evidence: two columns of short
	// lines look the same as a table. More rows, more columns, rows with
	// most cells filled, cells holding numbers and rules across the width
	// each add to it; lines that run across the column gaps take away.
	conf := 0.4
	if lineCount >= 4 {
		conf += 0.05
	}
	if lineCount >= 6 {
		conf += 0.05
	}
	if cols >= 3 {
		conf += 0.15
	}
	switch {
	case well*10 >= lineCount*7:
		conf += 0.15
	case well*2 >= lineCount:
		conf += 0.05
	}
	figures := nonEmpty > 0 && numeric*10 >= nonEmpty*3
	if figures {
		conf += 0.1
	}
	if bounding >= 2 {
		conf += 0.15
	}
	conf -= 0.3 * float64(spanning) / float64(len(lines))
	conf = math.Min(conf, 0.9)
	switch {
	case unsure || listLike(&t) || contentsLike(&t) || formulaLike(words, &t) || formLike(&t):
		// Prose in columns with nothing to mark the rows; a numbered list
		// with its numbers set apart; a table of contents; a matrix or a
		// formula set in rows and columns; the questions of a form with
		// the boxes for their answers.
		conf = math.Min(conf, 0.3)
	case well*2 < lineCount && edged < 2, lineCount < 5 && well < lineCount && bounding < 2, cols == 2 && !figures && bounding < 2:
		// Too ragged; or small, not full and not ruled off, like the
		// labels of a chart; or two columns of words with nothing else to
		// say it is a table: labels and their values, most often. A
		// triangle of figures, each row a figure longer than the last, is
		// ragged by nature: columns told apart by the edges their figures
		// share show it is a table all the same.
		conf = math.Min(conf, 0.45)
	}
	t.Confidence = math.Max(0.1, conf)
	if t.HeaderRows = header; header == 0 {
		t.HeaderRows = headerRows(words, &t)
	}
	return t, true
}

// columnEdges returns the extent of the text in each column.
func columnEdges(words []Word, rows []row, cols int) [][2]float64 {
	edges := make([][2]float64, cols)
	for c := range edges {
		edges[c] = [2]float64{math.Inf(1), math.Inf(-1)}
	}
	for _, rw := range rows {
		for c, idx := range rw.cells {
			for _, i := range idx {
				edges[c][0], edges[c][1] = math.Min(edges[c][0], words[i].X0), math.Max(edges[c][1], words[i].X1)
			}
		}
	}
	// A column left empty takes its neighbour's edge.
	for c := range edges {
		if math.IsInf(edges[c][0], 0) {
			if c > 0 {
				edges[c] = [2]float64{edges[c-1][1], edges[c-1][1]}
			} else {
				edges[c] = [2]float64{0, 0}
			}
		}
	}
	return edges
}

// mergeAffixes folds a column that holds nothing but currency signs into
// the column of amounts to its right, and one that holds nothing but
// percent signs or closing brackets into the column to its left. Financial
// tables set these apart so that the figures line up.
func mergeAffixes(words []Word, rows []row, cols int) ([]row, int) {
	const before, after = 1, 2
	kind := make([]int, cols)
	for c := 0; c < cols; c++ {
		n, pre, post := 0, 0, 0
		for _, rw := range rows {
			if len(rw.cells[c]) == 0 {
				continue
			}
			n++
			if len(rw.cells[c]) == 1 {
				switch words[rw.cells[c][0]].Text {
				case "$", "€", "£", "¥", "₹", "US$", "C$", "A$", "(":
					pre++
				case "%", ")", "%)":
					post++
				}
			}
		}
		switch {
		case n > 0 && pre == n && c+1 < cols:
			kind[c] = before
		case n > 0 && post == n && c > 0 && kind[c-1] != before:
			kind[c] = after
		}
	}
	// Each column's new position.
	target := make([]int, cols)
	next := 0
	for c := 0; c < cols; c++ {
		switch kind[c] {
		case after:
			target[c] = next - 1
		case before:
			target[c] = next // shared with the column that follows
		default:
			target[c] = next
			next++
		}
	}
	if next == cols {
		return rows, cols
	}
	for r := range rows {
		cells := make([][]int, next)
		for c, idx := range rows[r].cells {
			cells[target[c]] = append(cells[target[c]], idx...)
		}
		rows[r].cells = cells
	}
	return rows, next
}

// wraps reports whether some column holds prose that wraps from line to
// line: several words to a line, most lines running the full width of the
// column.
func wraps(words []Word, rows []row, cols int) bool {
	for c := 0; c < cols; c++ {
		widest := 0.0
		type extent struct {
			width float64
			words int
		}
		var lines []extent
		for _, rw := range rows {
			if len(rw.cells[c]) == 0 {
				continue
			}
			lo, hi := math.Inf(1), math.Inf(-1)
			for _, i := range rw.cells[c] {
				lo, hi = math.Min(lo, words[i].X0), math.Max(hi, words[i].X1)
			}
			lines = append(lines, extent{hi - lo, len(rw.cells[c])})
			widest = math.Max(widest, hi-lo)
		}
		if len(lines) < 3 {
			continue
		}
		full, count := 0, 0
		for _, l := range lines {
			count += l.words
			if l.width >= 0.85*widest {
				full++
			}
		}
		if count >= 3*len(lines) && full*2 >= len(lines) {
			return true
		}
	}
	return false
}

// rowsByRules groups lines into rows by the rules that run between them.
func rowsByRules(lines []row, ys []float64) []row {
	if len(ys) == 0 {
		return nil
	}
	sort.Float64s(ys)
	var out []row
	band := -1
	for _, l := range lines {
		b := sort.SearchFloat64s(ys, (l.y0+l.y1)/2)
		if b != band || len(out) == 0 {
			out = append(out, row{cells: make([][]int, len(l.cells)), y0: l.y0, y1: l.y1})
			band = b
		}
		out[len(out)-1].absorb(l)
	}
	return out
}

// rowsByGaps groups lines into rows where the space between two lines is
// clearly more than the usual.
func rowsByGaps(lines []row, size float64) []row {
	if len(lines) < 3 {
		return nil
	}
	gaps := make([]float64, 0, len(lines)-1)
	for i := 1; i < len(lines); i++ {
		gaps = append(gaps, lines[i].y0-lines[i-1].y1)
	}
	sorted := append([]float64(nil), gaps...)
	sort.Float64s(sorted)
	usual := sorted[len(sorted)/2]
	var out []row
	for i, l := range lines {
		if i == 0 || gaps[i-1] > usual+0.35*size {
			out = append(out, row{cells: make([][]int, len(l.cells)), y0: l.y0, y1: l.y1})
		}
		out[len(out)-1].absorb(l)
	}
	return out
}

// rowsByText makes lines into rows where a column holds text that wraps
// and neither rules nor wider gaps mark the rows: by what the lines say.
// It reports false when they do not say enough.
//
// Three cases, tried in turn:
//
//   - Most lines carry figures beside the first column, in columns that
//     hold little else. Then the lines are rows of figures whose labels
//     happen to be wordy, and nothing wraps but a label here and there.
//   - The first column has an entry on nearly every line, each beginning
//     with a capital or a figure. Then every line is a row here too:
//     entries of a length, one under the other, look like text that fills
//     its column.
//   - Otherwise a row starts where the first column starts an entry: it
//     has text that begins with a capital or a figure, and does not run on
//     from a full line just above. The lines between two such starts are
//     one row, whichever columns they have text in. This is believed only
//     when the first column shows the pattern of a table: its entries are
//     shorter than the rows, so that most of them start after a gap.
//
// A script with no capitals gives none of this, and its tables stay as
// lines.
func rowsByText(words []Word, lines []row, cols int, size float64) ([]row, bool) {
	if len(lines) < 2 || cols < 2 {
		return nil, false
	}
	figured, first, high := 0, 0, 0
	widest := 0.0 // the widest entry of the first column
	extent := func(idx []int) (x0, x1 float64) {
		x0, x1 = math.Inf(1), math.Inf(-1)
		for _, i := range idx {
			x0, x1 = math.Min(x0, words[i].X0), math.Max(x1, words[i].X1)
		}
		return x0, x1
	}
	// leading returns the leftmost word of a cell.
	leading := func(idx []int) Word {
		w := words[idx[0]]
		for _, i := range idx[1:] {
			if words[i].X0 < w.X0 {
				w = words[i]
			}
		}
		return w
	}
	// startsHigh reports whether a cell begins with a capital or a figure,
	// after any bracket, quotation mark or bullet.
	startsHigh := func(idx []int) bool {
		for _, r := range leading(idx).Text {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return unicode.IsUpper(r) || unicode.IsDigit(r)
			}
		}
		return false
	}
	filled, numeric := make([]int, cols), make([]int, cols)
	for _, l := range lines {
		figures := false
		for c := 1; c < cols; c++ {
			if len(l.cells[c]) == 0 {
				continue
			}
			filled[c]++
			if isNumeric(cellText(words, l.cells[c])) {
				numeric[c]++
				figures = true
			}
		}
		if figures {
			figured++
		}
		if len(l.cells[0]) == 0 {
			continue
		}
		first++
		if startsHigh(l.cells[0]) {
			high++
		}
		x0, x1 := extent(l.cells[0])
		widest = math.Max(widest, x1-x0)
	}
	columnsOfFigures := 0
	for c := 1; c < cols; c++ {
		if filled[c] >= 2 && numeric[c]*10 >= filled[c]*6 {
			columnsOfFigures++
		}
	}
	rowsOfFigures := figured*10 >= len(lines)*7 && columnsOfFigures >= min(2, cols-1)
	if rowsOfFigures || (first*5 >= len(lines)*4 && high*10 >= first*9) {
		return mergeContinuations(words, lines, cols, size), true
	}

	// Where the first column starts an entry.
	var starts []int
	gapped := 0  // starts that follow a gap in the first column
	before := -1 // the line before with text in the first column
	for i, l := range lines {
		if len(l.cells[0]) == 0 {
			continue
		}
		gap := before < 0 || l.y0-lines[before].y1 > 0.6*size
		start := startsHigh(l.cells[0])
		if start && !gap {
			// Under a full line, a capital may be mid-sentence: the line
			// above had no room for this line's first word.
			x0, x1 := extent(lines[before].cells[0])
			w := leading(l.cells[0])
			start = (x1-x0)+0.25*size+(w.X1-w.X0) <= widest
		}
		if start {
			starts = append(starts, i)
			if gap && before >= 0 {
				gapped++
			}
		}
		before = i
	}
	if len(starts) < 2 || gapped*2 < len(starts)-1 {
		return nil, false
	}
	if starts[0] != 0 {
		// Lines above the first entry: the rest of a row that began on the
		// page before.
		starts = append([]int{0}, starts...)
	}
	if len(starts) == len(lines) {
		return nil, false // every line a row: no wrapping to speak of
	}
	out := make([]row, 0, len(starts))
	for k, from := range starts {
		to := len(lines)
		if k+1 < len(starts) {
			to = starts[k+1]
		}
		r := row{cells: make([][]int, cols), y0: lines[from].y0, y1: lines[from].y1}
		for _, l := range lines[from:to] {
			r.absorb(l)
		}
		out = append(out, r)
	}
	return out, true
}

// mergeContinuations joins a line to the row it belongs to when it is only
// part of one: the second line of a row's cells, which leaves the first
// column empty and sits tight under the row, or the first line of a label
// too long for its column, which fills the first column alone and is
// followed by a line that goes on in lower case.
func mergeContinuations(words []Word, lines []row, cols int, size float64) []row {
	var out []row
	for _, l := range lines {
		n := len(out)
		if n > 0 && l.y0-out[n-1].y1 < 0.35*size {
			prev := &out[n-1]
			if len(l.cells[0]) == 0 && l.filled()*2 <= cols && !secondFigure(words, *prev, l) {
				prev.absorb(l)
				continue
			}
			if prev.filled() == 1 && len(prev.cells[0]) > 0 && len(l.cells[0]) > 0 && l.filled() > 1 {
				first := []rune(words[l.cells[0][0]].Text)
				last := words[prev.cells[0][len(prev.cells[0])-1]].Text
				if len(first) > 0 && unicode.IsLower(first[0]) && !strings.HasSuffix(last, ":") {
					prev.absorb(l)
					continue
				}
			}
		}
		out = append(out, l)
	}
	return out
}

// secondFigure reports whether a line puts a figure into a cell where the
// row above already has a cell of figures: figures, currency signs and
// dashes, and nothing else. Such a cell holds one figure, so the line is a
// row of its own: a total under the rows, or the next row of a table whose
// first column is left empty, not the rest of the row above. A heading
// such as "December 30," with "2023" under it, or a line of prose that
// names "Title 2", goes on as before.
func secondFigure(words []Word, above, l row) bool {
	has := func(idx []int) bool {
		for _, i := range idx {
			if isFigure(words[i].Text) {
				return true
			}
		}
		return false
	}
	only := func(idx []int) bool {
		for _, i := range idx {
			if t := words[i].Text; !isFigure(t) && !isAmount(t) && !isDash(t) {
				return false
			}
		}
		return has(idx)
	}
	for c := range l.cells {
		if c < len(above.cells) && has(l.cells[c]) && only(above.cells[c]) {
			return true
		}
	}
	return false
}

// isFigure reports a figure as tables of figures set them, and nothing
// else: digits with their separators, a sign, brackets round a negative
// figure, a percent sign or a footnote star after it. Unlike isNumeric it
// takes no letters, so that "3x3" and "x2y3" are not figures.
func isFigure(s string) bool {
	digits := 0
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case strings.ContainsRune(",.'\u00a0\u2009\u202f()-−+%*", r):
		default:
			return false
		}
	}
	return digits > 0
}

// isDash reports a dash that stands for nothing in a column of figures.
func isDash(s string) bool {
	switch s {
	case "—", "–", "-", "−":
		return true
	}
	return false
}

// formulaLike reports a matrix, a vector or a formula set in rows and
// columns: words that are pieces of the large brackets a formula is set
// in, or cells that hold nothing but brackets and the signs of operations
// ("(", ")", "=", "·"). The pieces of a large bracket are glyphs of their
// own, stacked down its two sides, and make columns more regular than a
// table's.
func formulaLike(words []Word, t *Table) bool {
	pieces, signs, cells, total := 0, 0, 0, 0
	for _, c := range t.Cells {
		if len(c.Words) == 0 {
			continue
		}
		only := true
		for _, i := range c.Words {
			text := words[i].Text
			total++
			switch {
			case strings.IndexFunc(text, bracketPiece) >= 0:
				pieces++
			case mathSign(text):
				signs++
			case text == "+" || text == "−":
				// Signs of a formula too, but tables put them in cells
				// that have no figure: they count, but make no cell of
				// signs.
				signs++
				only = false
			default:
				only = false
			}
		}
		if only {
			cells++
		}
	}
	return pieces >= 2 || cells >= 2 || (cells == 1 && (pieces+signs)*5 >= total)
}

// bracketPiece reports a piece of a large bracket, brace or bar, from
// which formulas build the delimiters that span several lines. Text has
// no use for them.
func bracketPiece(r rune) bool {
	return r >= '\u239b' && r <= '\u23b3'
}

// mathSign reports a word that is a bracket or the sign of an operation
// standing alone, or such a sign beside a glyph that has no text: a large
// bracket drawn from a font with no map to Unicode reads as "�(". A word
// of such glyphs alone says nothing: whole tables are set in fonts that
// have no map.
func mathSign(s string) bool {
	signs := 0
	for _, r := range s {
		switch {
		case strings.ContainsRune("()[]{}|‖=·◦×∈⇒⇔→≤≥", r):
			signs++
		case r != unicode.ReplacementChar:
			return false
		}
	}
	return signs == 1 || (signs > 1 && strings.ContainsRune(s, unicode.ReplacementChar))
}

// formLike reports the fields of a form that stand in rows and columns: a
// label, a row of dots, and a box to tick or to write in. The boxes are
// drawn, not written, so the dots lead to nothing: the cell after them is
// empty, or holds the label of the next field. In a table, a row of dots
// leads from a label to its figure.
//
// Three cells or more must end a line in such dots, and four in five of
// the cells that end a line in dots at all.
func formLike(t *Table) bool {
	if t.Rows > maxRows || t.Cols > 60 {
		return false
	}
	// For each cell that ends a line in a row of dots, where the next cell
	// of its row is.
	type led struct{ row, col, end int }
	var leds []led
	figures := map[[2]int]bool{} // the cells that hold a figure
	for _, c := range t.Cells {
		if c.Text == "" {
			continue
		}
		dots := false
		for _, line := range strings.Split(c.Text, "\n") {
			line = strings.TrimSpace(line)
			if trimmed := strings.TrimRight(line, ". "); len(line)-len(trimmed) >= 3 && strings.Count(line[len(trimmed):], ".") >= 3 {
				dots = true
			}
			if isNumeric(strings.Trim(line, ". ")) {
				figures[[2]int{c.Row, c.Col}] = true
			}
		}
		if dots {
			leds = append(leds, led{c.Row, c.Col, c.Col + max(c.ColSpan, 1)})
		}
	}
	blind := 0
	for _, l := range leds {
		if !figures[[2]int{l.row, l.end}] {
			blind++
		}
	}
	return blind >= 3 && blind*5 >= len(leds)*4
}

// listLike reports a numbered or bulleted list whose markers stand apart
// from the text: two columns, the first holding nothing but markers.
func listLike(t *Table) bool {
	if t.Cols != 2 {
		return false
	}
	markers, rows := 0, 0
	for _, c := range t.Cells {
		if c.Col != 0 {
			continue
		}
		rows++
		text := strings.Trim(c.Text, "().")
		r := []rune(text)
		switch {
		case len(r) == 0 || len(r) > 3:
		case len(r) == 1 && !unicode.IsLetter(r[0]) && !unicode.IsDigit(r[0]):
			markers++ // a bullet
		case isPageNumber(text) || (len(r) == 1 && c.Text != text):
			markers++ // 1. 2. 3. or (a) (b) (c)
		}
	}
	return rows > 0 && markers*5 >= rows*4
}

// contentsLike reports a table of contents: titles on the left, page
// numbers that never go down on the right.
//
// A page number may carry the letter of its part of the document, "K-118"
// or "A-3". Where the lines of several entries have been put into one row,
// each line of the last column is the page number of one entry. And a row
// with one thing on it and no page number is a heading between groups of
// entries, "Part II": it does not count against the page numbers, and their
// rise is followed across it.
func contentsLike(t *Table) bool {
	if t.Cols > 3 || t.Rows < 4 {
		return false
	}
	filled := make([]int, t.Rows)  // the cells with text in each row
	last := make([]string, t.Rows) // the text of its last column
	for _, c := range t.Cells {
		if c.Text == "" || c.Row < 0 || c.Row >= t.Rows {
			continue
		}
		filled[c.Row]++
		if c.Col == t.Cols-1 {
			last[c.Row] = c.Text
		}
	}
	entries, pages, rising, pairs := 0, 0, 0, 0
	part, before := "", -1 // of the page number before
	for r, text := range last {
		if text == "" {
			if filled[r] > 1 {
				entries++
			}
			continue
		}
		for _, line := range strings.Split(text, "\n") {
			entries++
			p, n, ok := pageLabel(strings.TrimSpace(line))
			if !ok {
				continue
			}
			pages++
			if n < 0 {
				continue
			}
			if before >= 0 && p == part {
				pairs++
				if n >= before {
					rising++
				}
			}
			part, before = p, n
		}
	}
	if leaders(t)*2 >= t.Rows {
		return true
	}
	if pages < 4 || pages*5 < entries*4 || rising*10 < pairs*9 {
		return false
	}
	// The other columns hold words, not figures.
	for _, c := range t.Cells {
		if c.Col > 0 && c.Col < t.Cols-1 && isNumeric(c.Text) {
			return false
		}
	}
	return true
}

// leaders counts the rows that end as the lines of a table of contents do:
// a row of dots that leads to a page number, in one cell with the title.
func leaders(t *Table) int {
	last := make([]string, t.Rows) // the last cell with text in each row
	for _, c := range t.Cells {
		if c.Text != "" && c.Row >= 0 && c.Row < t.Rows {
			last[c.Row] = c.Text
		}
	}
	n := 0
	for _, text := range last {
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			cut := strings.LastIndex(line, "....")
			if cut < 0 {
				continue
			}
			page := strings.TrimLeft(line[cut:], ". ")
			if page != "" && len(page) <= 8 && !strings.ContainsRune(page, ' ') && strings.ContainsAny(page, "0123456789ivxlcIVXLC") {
				n++
				break
			}
		}
	}
	return n
}

// pageLabel reports a page number as a table of contents prints it: a
// short number in digits or roman numerals, or a number after the letters
// of a part of the document and a hyphen, "K-118". It returns the part,
// and the number, which is -1 for roman numerals.
func pageLabel(s string) (part string, n int, ok bool) {
	if i := strings.IndexByte(s, '-'); i >= 1 && i <= 3 {
		for _, r := range s[:i] {
			if !unicode.IsLetter(r) {
				return "", 0, false
			}
		}
		part, s = s[:i], s[i+1:]
	}
	if !isPageNumber(s) {
		return "", 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			// Roman numerals. After the letters of a part they would be a
			// word with a hyphen in it.
			return "", -1, part == ""
		}
		n = n*10 + int(r-'0')
	}
	return part, n, true
}

// isPageNumber reports a short number in digits or roman numerals.
func isPageNumber(s string) bool {
	if s == "" || len(s) > 5 {
		return false
	}
	digits, roman := true, true
	for _, r := range s {
		digits = digits && r >= '0' && r <= '9'
		roman = roman && strings.ContainsRune("ivxlcIVXLC", r)
	}
	return digits || roman
}
