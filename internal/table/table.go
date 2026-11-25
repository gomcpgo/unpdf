// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

// Package table finds tables on a page from geometry alone: from ruling
// lines that form a grid, and from text that lines up in columns over
// several rows.
//
// Everything is in display space: points, origin at the top left, Y growing
// downward. Each table comes with a confidence, because neither method is
// certain: boxes drawn for decoration look like grids, and tidy lists look
// like columns.
package table

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// Word is a word of the page.
type Word struct {
	Text           string
	X0, Y0, X1, Y1 float64
	Size           float64
	Bold           bool
}

// Rule is a horizontal (Y0 == Y1) or vertical (X0 == X1) line.
type Rule struct {
	X0, Y0, X1, Y1 float64
}

// Cell is one cell of a table. A merged cell appears once, at its top-left
// position, with spans greater than one.
type Cell struct {
	Row, Col         int
	RowSpan, ColSpan int
	X0, Y0, X1, Y1   float64
	// Words are indices into the words the table was built from.
	Words []int
	// Text is the cell's text, with a line break between the lines of a
	// cell that holds several.
	Text string
}

// Table is a detected table.
type Table struct {
	X0, Y0, X1, Y1 float64
	Rows, Cols     int
	Cells          []Cell
	// HeaderRows is the number of rows at the top that are headings.
	HeaderRows int
	// Confidence is between 0 and 1.
	Confidence float64
	// Method is "rules" or "alignment".
	Method string
}

const (
	snap       = 2.5 // rules this close share a grid line
	minRule    = 6.0 // shorter rules are dashes and tick marks
	edgeCover  = 0.6 // share of a cell edge a rule must cover to close it
	maxGridDim = 400 // rows or columns beyond this are not a table
	// maxGridCells and maxRules bound the work on a page drawn to be slow.
	maxGridCells = 20000
	maxRules     = 4000
)

type hline struct{ y, x0, x1 float64 }
type vline struct{ x, y0, y1 float64 }

// Ruled finds the tables drawn with ruling lines: every group of horizontal
// and vertical rules that cross each other and enclose at least two cells.
func Ruled(words []Word, rules []Rule) []Table {
	if len(rules) > maxRules {
		rules = rules[:maxRules]
	}
	hs, vs := split(rules)
	if len(hs) < 2 || len(vs) < 2 {
		return nil
	}
	// Rules that cross belong to the same table.
	parent := make([]int, len(hs)+len(vs))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	for i, h := range hs {
		for j, v := range vs {
			if v.x >= h.x0-snap && v.x <= h.x1+snap && h.y >= v.y0-snap && h.y <= v.y1+snap {
				parent[find(i)] = find(len(hs) + j)
			}
		}
	}
	groups := map[int][2][]int{}
	for i := range hs {
		g := groups[find(i)]
		g[0] = append(g[0], i)
		groups[find(i)] = g
	}
	for j := range vs {
		g := groups[find(len(hs)+j)]
		g[1] = append(g[1], j)
		groups[find(len(hs)+j)] = g
	}

	var tables []Table
	for _, g := range groups {
		if len(g[0]) < 2 || len(g[1]) < 2 {
			continue
		}
		gh := make([]hline, len(g[0]))
		for i, k := range g[0] {
			gh[i] = hs[k]
		}
		gv := make([]vline, len(g[1]))
		for i, k := range g[1] {
			gv[i] = vs[k]
		}
		if t, ok := grid(words, gh, gv); ok {
			tables = append(tables, t)
		}
	}
	sort.Slice(tables, func(i, j int) bool {
		if tables[i].Y0 != tables[j].Y0 {
			return tables[i].Y0 < tables[j].Y0
		}
		return tables[i].X0 < tables[j].X0
	})
	return tables
}

// split sorts rules into horizontal and vertical ones and joins the pieces
// of a line drawn in several strokes.
func split(rules []Rule) ([]hline, []vline) {
	var hs []hline
	var vs []vline
	for _, r := range rules {
		switch {
		case math.Abs(r.Y1-r.Y0) <= 0.01 && r.X1-r.X0 >= 1:
			hs = append(hs, hline{r.Y0, r.X0, r.X1})
		case math.Abs(r.X1-r.X0) <= 0.01 && r.Y1-r.Y0 >= 1:
			vs = append(vs, vline{r.X0, r.Y0, r.Y1})
		}
	}
	sort.Slice(hs, func(i, j int) bool {
		if math.Abs(hs[i].y-hs[j].y) > snap/2 {
			return hs[i].y < hs[j].y
		}
		return hs[i].x0 < hs[j].x0
	})
	mergedH := hs[:0]
	for _, h := range hs {
		if n := len(mergedH); n > 0 && math.Abs(mergedH[n-1].y-h.y) <= snap/2 && h.x0 <= mergedH[n-1].x1+snap {
			mergedH[n-1].x1 = math.Max(mergedH[n-1].x1, h.x1)
			continue
		}
		mergedH = append(mergedH, h)
	}
	sort.Slice(vs, func(i, j int) bool {
		if math.Abs(vs[i].x-vs[j].x) > snap/2 {
			return vs[i].x < vs[j].x
		}
		return vs[i].y0 < vs[j].y0
	})
	mergedV := vs[:0]
	for _, v := range vs {
		if n := len(mergedV); n > 0 && math.Abs(mergedV[n-1].x-v.x) <= snap/2 && v.y0 <= mergedV[n-1].y1+snap {
			mergedV[n-1].y1 = math.Max(mergedV[n-1].y1, v.y1)
			continue
		}
		mergedV = append(mergedV, v)
	}
	outH := mergedH[:0]
	for _, h := range mergedH {
		if h.x1-h.x0 >= minRule {
			outH = append(outH, h)
		}
	}
	outV := mergedV[:0]
	for _, v := range mergedV {
		if v.y1-v.y0 >= minRule {
			outV = append(outV, v)
		}
	}
	return outH, outV
}

// distinct returns the sorted values with those closer than snap merged.
func distinct(vals []float64) []float64 {
	sort.Float64s(vals)
	out := vals[:0]
	for _, v := range vals {
		if n := len(out); n > 0 && v-out[n-1] <= snap {
			continue
		}
		out = append(out, v)
	}
	return out
}

// nearest returns the index of the value in sorted vals closest to v, or -1
// when none is within snap.
func nearest(vals []float64, v float64) int {
	i := sort.SearchFloat64s(vals, v)
	best := -1
	for _, k := range []int{i - 1, i} {
		if k >= 0 && k < len(vals) && math.Abs(vals[k]-v) <= snap && (best < 0 || math.Abs(vals[k]-v) < math.Abs(vals[best]-v)) {
			best = k
		}
	}
	return best
}

// grid builds a table from one group of crossing rules.
func grid(words []Word, hs []hline, vs []vline) (Table, bool) {
	var ys, xs []float64
	for _, h := range hs {
		ys = append(ys, h.y)
	}
	for _, v := range vs {
		xs = append(xs, v.x)
	}
	ys, xs = distinct(ys), distinct(xs)
	// Rules that run on past the outermost crossing rule mark a column or a
	// row with no border of its own: the row labels down the left of many
	// statistical tables, for one.
	const stub = 12.0
	left, right, top, bottom := xs[0], xs[len(xs)-1], ys[0], ys[len(ys)-1]
	for _, h := range hs {
		left, right = math.Min(left, h.x0), math.Max(right, h.x1)
	}
	for _, v := range vs {
		top, bottom = math.Min(top, v.y0), math.Max(bottom, v.y1)
	}
	if left < xs[0]-stub {
		xs = append([]float64{left}, xs...)
	}
	if right > xs[len(xs)-1]+stub {
		xs = append(xs, right)
	}
	if top < ys[0]-stub {
		ys = append([]float64{top}, ys...)
	}
	if bottom > ys[len(ys)-1]+stub {
		ys = append(ys, bottom)
	}
	if len(ys) < 2 || len(xs) < 2 || len(ys) > maxGridDim || len(xs) > maxGridDim {
		return Table{}, false
	}
	// Rows that no rule separates: see splitBands.
	ys, virtual := splitBands(words, xs, ys)
	rows, cols := len(ys)-1, len(xs)-1
	if rows < 1 || cols < 1 || rows*cols < 2 || rows > maxGridDim || cols > maxGridDim || rows*cols > maxGridCells {
		return Table{}, false
	}

	// How much of each cell's right and bottom edge is drawn.
	id := func(r, c int) int { return r*cols + c }
	rightEdge := make([]float64, rows*cols)  // the edge at xs[c+1] between ys[r] and ys[r+1]
	bottomEdge := make([]float64, rows*cols) // the edge at ys[r+1] between xs[c] and xs[c+1]
	for _, v := range vs {
		c := nearest(xs, v.x) - 1
		if c < 0 || c >= cols {
			continue
		}
		for r := 0; r < rows; r++ {
			if d := math.Min(v.y1, ys[r+1]) - math.Max(v.y0, ys[r]); d > 0 {
				rightEdge[id(r, c)] += d
			}
		}
	}
	for _, h := range hs {
		r := nearest(ys, h.y) - 1
		if r < 0 || r >= rows {
			continue
		}
		for c := 0; c < cols; c++ {
			if d := math.Min(h.x1, xs[c+1]) - math.Max(h.x0, xs[c]); d > 0 {
				bottomEdge[id(r, c)] += d
			}
		}
	}
	hasRight := func(r, c int) bool {
		// The rows of a band split by its lines of text are rows of
		// figures; there the columns hold even where their rules stop at
		// the header.
		return virtual[r] || virtual[r+1] || rightEdge[id(r, c)] >= edgeCover*(ys[r+1]-ys[r])
	}
	hasBottom := func(r, c int) bool {
		return virtual[r+1] || bottomEdge[id(r, c)] >= edgeCover*(xs[c+1]-xs[c])
	}

	// Grid cells with no rule between them are one merged cell.
	parent := make([]int, rows*cols)
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			if c+1 < cols && !hasRight(r, c) {
				parent[find(id(r, c))] = find(id(r, c+1))
			}
			if r+1 < rows && !hasBottom(r, c) {
				parent[find(id(r, c))] = find(id(r+1, c))
			}
		}
	}
	type extent struct{ r0, c0, r1, c1, n int }
	extents := map[int]*extent{}
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			root := find(id(r, c))
			e := extents[root]
			if e == nil {
				e = &extent{r, c, r, c, 0}
				extents[root] = e
			}
			e.r0, e.c0 = min(e.r0, r), min(e.c0, c)
			e.r1, e.c1 = max(e.r1, r), max(e.c1, c)
			e.n++
		}
	}

	t := Table{X0: xs[0], Y0: ys[0], X1: xs[cols], Y1: ys[rows], Rows: rows, Cols: cols, Method: "rules"}
	owner := make([]int, rows*cols) // grid cell -> index into t.Cells
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			e := extents[find(id(r, c))]
			rect := e.n == (e.r1-e.r0+1)*(e.c1-e.c0+1)
			switch {
			case rect && (r != e.r0 || c != e.c0):
				owner[id(r, c)] = owner[id(e.r0, e.c0)] // covered by a merged cell
			case rect:
				owner[id(r, c)] = len(t.Cells)
				t.Cells = append(t.Cells, Cell{Row: r, Col: c, RowSpan: e.r1 - e.r0 + 1, ColSpan: e.c1 - e.c0 + 1,
					X0: xs[c], Y0: ys[r], X1: xs[e.c1+1], Y1: ys[e.r1+1]})
			default:
				// A merged region that is not a rectangle: leave its cells
				// as they are in the grid.
				owner[id(r, c)] = len(t.Cells)
				t.Cells = append(t.Cells, Cell{Row: r, Col: c, RowSpan: 1, ColSpan: 1, X0: xs[c], Y0: ys[r], X1: xs[c+1], Y1: ys[r+1]})
			}
		}
	}

	// Put the words in their cells, by where the middle of each falls.
	inside := 0
	type rowWord struct {
		mid, height float64
		cell        int
	}
	rowWords := make([][]rowWord, rows)
	for i, w := range words {
		mx, my := (w.X0+w.X1)/2, (w.Y0+w.Y1)/2
		if mx < t.X0 || mx > t.X1 || my < t.Y0 || my > t.Y1 {
			continue
		}
		c := sort.SearchFloat64s(xs, mx) - 1
		r := sort.SearchFloat64s(ys, my) - 1
		if r < 0 || c < 0 || r >= rows || c >= cols {
			continue
		}
		cell := &t.Cells[owner[id(r, c)]]
		cell.Words = append(cell.Words, i)
		rowWords[r] = append(rowWords[r], rowWord{my, w.Y1 - w.Y0, owner[id(r, c)]})
		inside++
	}
	if inside == 0 {
		return Table{}, false
	}
	filled := 0
	for i := range t.Cells {
		t.Cells[i].Text = cellText(words, t.Cells[i].Words)
		if t.Cells[i].Text != "" {
			filled++
		}
	}
	// One merged cell holding everything is a box around text, not a table.
	if len(t.Cells) < 2 {
		return Table{}, false
	}

	// Confidence. A full grid of several rows and columns is a table.
	// Other things drawn with crossing lines are not: a form, whose boxes
	// differ from row to row, so that few cells are a plain grid square; a
	// chart or a diagram, which has a label here and there, so that few
	// rows hold two cells with text; a strip of boxes.
	t.Confidence = 0.95
	plain, busy := 0, 0
	perRow := make([]int, rows)
	for _, c := range t.Cells {
		if c.RowSpan == 1 && c.ColSpan == 1 {
			plain++
		}
		if c.Text != "" {
			perRow[c.Row]++
		}
	}
	for _, n := range perRow {
		if n >= 2 {
			busy++
		}
	}
	if share := float64(filled) / float64(len(t.Cells)); share < 0.3 {
		t.Confidence -= 0.35
	} else if share < 0.6 {
		t.Confidence -= 0.15
	}
	// How full the table is, leaving aside rows and columns that are empty
	// from end to end: a table for twelve months is still a table in May.
	// A map or a plan with a grid over it has words dotted about.
	rowHas, colHas := make([]bool, rows), make([]bool, cols)
	covered := 0
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			if t.Cells[owner[id(r, c)]].Text != "" {
				rowHas[r], colHas[c] = true, true
				covered++
			}
		}
	}
	usedRows, usedCols := 0, 0
	for _, has := range rowHas {
		if has {
			usedRows++
		}
	}
	for _, has := range colHas {
		if has {
			usedCols++
		}
	}
	switch {
	case busy < 2:
		t.Confidence = math.Min(t.Confidence, 0.3)
	case rows == 1 || cols == 1 || plain*2 < rows*cols:
		t.Confidence = math.Min(t.Confidence, 0.4)
	case covered*2 < usedRows*usedCols:
		t.Confidence = math.Min(t.Confidence, 0.45)
	}
	// A cell merged down several rows whose text stands on the last of
	// them, on the line of that row's own text, belongs to that row: a
	// heading set at the foot of its box, or a label under a stretch with
	// no rule. Where it is, not where its box starts, is its row.
	for i := range t.Cells {
		c := &t.Cells[i]
		last := c.Row + c.RowSpan - 1
		if c.RowSpan < 2 || len(c.Words) == 0 {
			continue
		}
		mine, lowest, others := math.Inf(-1), math.Inf(-1), 0
		height := 0.0
		elsewhere := false
		for r := c.Row; r <= last; r++ {
			for _, w := range rowWords[r] {
				switch {
				case w.cell != i && r == last:
					others++
					lowest = math.Max(lowest, w.mid)
				case w.cell == i && r == last:
					mine, height = math.Max(mine, w.mid), w.height
				case w.cell == i:
					elsewhere = true
				}
			}
		}
		if elsewhere || math.IsInf(mine, -1) || (others > 0 && math.Abs(mine-lowest) > 0.5*height) {
			continue
		}
		c.Row, c.RowSpan, c.Y0 = last, 1, ys[last]
	}
	sort.SliceStable(t.Cells, func(i, j int) bool {
		if t.Cells[i].Row != t.Cells[j].Row {
			return t.Cells[i].Row < t.Cells[j].Row
		}
		return t.Cells[i].Col < t.Cells[j].Col
	})
	t.HeaderRows = headerRows(words, &t)
	return t, true
}

// cellText joins the words of a cell: the words of one line with spaces,
// the lines with line breaks. Words on one line keep the order they were
// given in, which is reading order, so that right-to-left text stays right.
func cellText(words []Word, idx []int) string {
	if len(idx) == 0 {
		return ""
	}
	sorted := append([]int(nil), idx...)
	mid := func(i int) float64 { return (words[i].Y0 + words[i].Y1) / 2 }
	sort.SliceStable(sorted, func(i, j int) bool { return mid(sorted[i]) < mid(sorted[j]) })
	var b strings.Builder
	for from := 0; from < len(sorted); {
		// The words whose middles are within half a word's height of the
		// first one's are on its line.
		first := words[sorted[from]]
		to := from + 1
		for to < len(sorted) && mid(sorted[to])-mid(sorted[from]) <= 0.5*(first.Y1-first.Y0) {
			to++
		}
		row := sorted[from:to]
		sort.Ints(row)
		if from > 0 {
			b.WriteByte('\n')
		}
		for k, i := range row {
			if k > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(words[i].Text)
		}
		from = to
	}
	return b.String()
}

// headerRows guesses how many rows at the top are headings: one, when the
// first row is set in bold and the rows below are not.
func headerRows(words []Word, t *Table) int {
	if t.Rows < 2 {
		return 0
	}
	firstBold, firstAll, restBold, restAll := 0, 0, 0, 0
	for _, c := range t.Cells {
		for _, i := range c.Words {
			if c.Row == 0 {
				firstAll++
				if words[i].Bold {
					firstBold++
				}
			} else {
				restAll++
				if words[i].Bold {
					restBold++
				}
			}
		}
	}
	if firstAll > 0 && firstBold == firstAll && restBold*2 < restAll {
		return 1
	}
	return 0
}

// splitBands adds row boundaries inside the bands of a grid whose rows no
// rule separates. Many tables rule off the columns and the header and
// leave the rows of figures to white space; between two of its horizontal
// rules such a table has a whole stack of rows.
//
// A band is split, one row per line of text, when its columns keep step:
// at least two of them, and half of those with text, have a word on nearly
// every line. Three lines are enough, or two if they hold figures. A band
// of cells whose text merely wraps does not keep step like that, or reads
// as prose and is left whole. Within a band that is split, a line that
// holds one column's text only and is set closer to the next line than rows
// are is the second line of a label, and stays with its row.
//
// A band that does not keep step may still hold two rows: a line that
// names a group ("Farm income:") above the first row of the group.
//
// It returns the boundaries and, for each, whether it was added here.
func splitBands(words []Word, xs, ys []float64) ([]float64, []bool) {
	cols := len(xs) - 1
	type placed struct {
		mid, top, bottom float64
		col, word        int
	}
	bands := make([][]placed, len(ys)-1)
	for i, w := range words {
		mx, my := (w.X0+w.X1)/2, (w.Y0+w.Y1)/2
		if mx < xs[0] || mx > xs[cols] || my < ys[0] || my > ys[len(ys)-1] {
			continue
		}
		c, r := sort.SearchFloat64s(xs, mx)-1, sort.SearchFloat64s(ys, my)-1
		if c < 0 || c >= cols || r < 0 || r >= len(bands) {
			continue
		}
		bands[r] = append(bands[r], placed{my, w.Y0, w.Y1, c, i})
	}
	// textLine is one line of text across a band.
	type textLine struct {
		top, bottom float64
		cols        map[int]bool
		from, to    map[int]float64 // extent of the text in each column
		first, last map[int]int     // leftmost and rightmost word in each column
	}
	mid := func(l *textLine) float64 { return (l.top + l.bottom) / 2 }
	out := []float64{ys[0]}
	virtual := []bool{false}
	for r, band := range bands {
		var lines []*textLine
		numeric := 0
		if len(band) >= 2 {
			sort.Slice(band, func(i, j int) bool { return band[i].mid < band[j].mid })
			for _, pw := range band {
				n := len(lines)
				if n == 0 || pw.mid > mid(lines[n-1])+0.6*(pw.bottom-pw.top) {
					lines = append(lines, &textLine{pw.top, pw.bottom, map[int]bool{}, map[int]float64{}, map[int]float64{}, map[int]int{}, map[int]int{}})
					n++
				}
				l := lines[n-1]
				l.top, l.bottom = math.Min(l.top, pw.top), math.Max(l.bottom, pw.bottom)
				w := words[pw.word]
				if !l.cols[pw.col] || w.X0 < l.from[pw.col] {
					l.from[pw.col], l.first[pw.col] = w.X0, pw.word
				}
				if !l.cols[pw.col] || w.X1 > l.to[pw.col] {
					l.to[pw.col], l.last[pw.col] = w.X1, pw.word
				}
				l.cols[pw.col] = true
				if isNumeric(w.Text) {
					numeric++
				}
			}
		}
		k := len(lines)
		cut := make([]bool, k) // cut[i]: a row boundary above line i
		if k >= 2 && k <= maxGridDim {
			perCol := make([]int, cols)
			for _, l := range lines {
				for c := range l.cols {
					perCol[c]++
				}
			}
			withText, inStep, wrapped := 0, 0, false
			for c, n := range perCol {
				if n == 0 {
					continue
				}
				withText++
				if n*10 < k*7 {
					continue
				}
				inStep++
				// Prose that wraps fills the width of its column on
				// most lines.
				full := 0
				for _, l := range lines[:k-1] {
					if l.cols[c] && l.to[c]-l.from[c] >= 0.8*(xs[c+1]-xs[c]) {
						full++
					}
				}
				wrapped = wrapped || full*2 >= k-1
			}
			figures := numeric*10 >= len(band)*4
			inStepEnough := inStep >= 2 && inStep*2 >= withText && len(band) >= 6
			switch {
			case inStepEnough && (k >= 3 && (figures || !wrapped) || k == 2 && figures):
				for i := 1; i < k; i++ {
					cut[i] = true
				}
				// The usual distance from one line to the next.
				gaps := make([]float64, 0, k-1)
				for i := 1; i < k; i++ {
					gaps = append(gaps, mid(lines[i])-mid(lines[i-1]))
				}
				sort.Float64s(gaps)
				pitch := gaps[len(gaps)/2]
				for i, l := range lines {
					if len(l.cols) != 1 {
						continue
					}
					above, below := math.Inf(1), math.Inf(1)
					if i > 0 {
						above = mid(l) - mid(lines[i-1])
					}
					if i+1 < k {
						below = mid(lines[i+1]) - mid(l)
					}
					switch {
					case above < 0.75*pitch && above <= below:
						cut[i] = false
					case below < 0.75*pitch:
						cut[i+1] = false
					}
				}
			default:
				// One line with text in several columns, under lines with
				// text in one column only, the same one, where that line
				// has text of its own: a heading for the rows that follow,
				// unless it reads as the start of the line's own label.
				f := -1
				for i, l := range lines {
					if len(l.cols) >= 2 {
						if f >= 0 {
							f = -2
							break
						}
						f = i
					}
				}
				if f < 1 {
					break
				}
				label := lines[f-1]
				c := -1
				for col := range label.cols {
					c = col
				}
				// Only in the first column with text, where row labels
				// are: elsewhere it is a column heading of two lines.
				ok := lines[f].cols[c]
				for col := range lines[f].cols {
					ok = ok && col >= c
				}
				for _, l := range lines[:f] {
					ok = ok && len(l.cols) == 1 && l.cols[c]
				}
				if !ok {
					break
				}
				start := []rune(words[lines[f].first[c]].Text)
				fills := label.to[c]-label.from[c] >= 0.8*(xs[c+1]-xs[c])
				if strings.HasSuffix(words[label.last[c]].Text, ":") ||
					(len(start) > 0 && (unicode.IsUpper(start[0]) || unicode.IsDigit(start[0])) && !fills) {
					cut[f] = true
				}
			}
		}
		for i := 1; i < k; i++ {
			if cut[i] {
				out = append(out, (lines[i-1].bottom+lines[i].top)/2)
				virtual = append(virtual, true)
			}
		}
		out = append(out, ys[r+1])
		virtual = append(virtual, false)
	}
	// Boundaries must stay in order for the searches that follow.
	for i := 1; i < len(out); i++ {
		if out[i] < out[i-1] {
			out[i] = out[i-1]
		}
	}
	return out, virtual
}

// isNumeric reports whether a cell holds a number, an amount or a
// percentage.
func isNumeric(s string) bool {
	digits, other := 0, 0
	for _, r := range s {
		switch {
		case unicode.IsDigit(r):
			digits++
		case unicode.IsLetter(r):
			other++
		}
	}
	return digits > 0 && digits >= other
}
