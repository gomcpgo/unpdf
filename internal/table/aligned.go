// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package table

import (
	"math"
	"sort"
	"strings"
	"unicode"
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
	tabular := make([]bool, len(lines))
	for i, l := range lines {
		tabular[i] = gapped(words, l)
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
			if cur.Y0 < prev.Y0-sz || cur.Y0-prev.Y1 > rowPitch*sz {
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
			for i := start; i < end; i++ {
				member[i] = len(tables)
			}
			tables = append(tables, t)
		}
		start = end
	}
	return tables, member
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
	seps := separators(words, gapLines, x0, x1, size)
	if len(seps) == 0 || len(seps) > 60 {
		return Table{}, false
	}
	cols := len(seps) + 1

	// The cells of each line.
	rows := make([]row, len(lines))
	spanning := 0
	for li, l := range lines {
		cells := make([][]int, cols)
		crosses := false
		for _, i := range l.Words {
			w := words[i]
			for _, s := range seps {
				crosses = crosses || (w.X0 < s && w.X1 > s)
			}
		}
		at := -1 // the one cell of a line that names a group of rows
		if crosses && named[li] && len(l.Words) > 0 {
			w := words[l.Words[0]]
			at = sort.SearchFloat64s(seps, (w.X0+w.X1)/2)
		}
		for _, i := range l.Words {
			c := at
			if c < 0 {
				w := words[i]
				c = sort.SearchFloat64s(seps, (w.X0+w.X1)/2)
			}
			cells[c] = append(cells[c], i)
		}
		if crosses {
			spanning++
		}
		rows[li] = row{cells, l.Y0, l.Y1}
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
	for r, rw := range rows {
		at := len(t.Cells)
		for c, idx := range rw.cells {
			cell := Cell{Row: r, Col: c, RowSpan: 1, ColSpan: 1, X0: bounds[c], X1: bounds[c+1], Y0: rw.y0, Y1: rw.y1, Words: idx}
			cell.Text = cellText(words, idx)
			t.Cells = append(t.Cells, cell)
		}
		if !joined || r >= header {
			count(rw, func(c int) string { return t.Cells[at+c].Text })
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
	case unsure || listLike(&t) || contentsLike(&t):
		// Prose in columns with nothing to mark the rows; a numbered list
		// with its numbers set apart; a table of contents.
		conf = math.Min(conf, 0.3)
	case well*2 < lineCount, lineCount < 5 && well < lineCount && bounding < 2, cols == 2 && !figures && bounding < 2:
		// Too ragged; or small, not full and not ruled off, like the
		// labels of a chart; or two columns of words with nothing else to
		// say it is a table: labels and their values, most often.
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
func contentsLike(t *Table) bool {
	if t.Cols > 3 || t.Rows < 4 {
		return false
	}
	pages, rising, pairs := 0, 0, 0
	last := -1
	for _, c := range t.Cells {
		if c.Col != t.Cols-1 || !isPageNumber(c.Text) {
			continue
		}
		pages++
		n := 0
		for _, r := range c.Text {
			if r < '0' || r > '9' {
				n = -1
				break
			}
			n = n*10 + int(r-'0')
		}
		if n >= 0 {
			if last >= 0 {
				pairs++
				if n >= last {
					rising++
				}
			}
			last = n
		}
	}
	if leaders(t)*2 >= t.Rows {
		return true
	}
	if pages*5 < t.Rows*4 || rising*10 < pairs*9 {
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
