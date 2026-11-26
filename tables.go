// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package unpdf

import (
	"context"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/gomcpgo/unpdf/internal/layout"
	"github.com/gomcpgo/unpdf/internal/table"
)

// How a table was found.
const (
	// TableFromStructure: the document's own structure tree marks the
	// table, its rows and its cells.
	TableFromStructure = "structure"
	// TableFromRules: ruling lines on the page form a grid.
	TableFromRules = "rules"
	// TableFromAlignment: text lines up in columns over several rows, with
	// no lines drawn.
	TableFromAlignment = "alignment"
)

// Table is a table on a page.
type Table struct {
	// Box is the table's extent on the page.
	Box Box
	// Rows and Cols are the size of the table's grid.
	Rows, Cols int
	// Cells holds the cells row by row. A cell merged over several rows or
	// columns appears once, at its top-left position.
	Cells []TableCell
	// HeaderRows is the number of rows at the top known to be headings: one
	// for each level of headings, a heading set over several lines being
	// one cell. It is 0 when nothing sets them apart.
	HeaderRows int
	// Confidence says how sure the detection is, from 0 to 1. A table the
	// document itself marks, or one drawn as a full grid, is near 1. One
	// inferred from text that lines up is lower, and lower still when it is
	// small or ragged. Markdown shows a table as a table only from 0.5.
	Confidence float64
	// Method says how the table was found: TableFromStructure,
	// TableFromRules or TableFromAlignment.
	Method string
}

// TableCell is one cell of a table.
type TableCell struct {
	// Row and Col are the cell's position, counting from 0.
	Row, Col int
	// RowSpan and ColSpan are the number of rows and columns the cell
	// covers: 1 unless it is merged.
	RowSpan, ColSpan int
	Box              Box
	// Text is the cell's text, with a line break between its lines.
	Text string
}

// Grid returns the text of the table as rows of cells. The text of a merged
// cell is in its top-left position, and the other positions it covers are
// empty.
func (t Table) Grid() [][]string {
	if t.Rows <= 0 || t.Cols <= 0 {
		return nil
	}
	grid := make([][]string, t.Rows)
	for r := range grid {
		grid[r] = make([]string, t.Cols)
	}
	for _, c := range t.Cells {
		if c.Row >= 0 && c.Row < t.Rows && c.Col >= 0 && c.Col < t.Cols {
			grid[c.Row][c.Col] = c.Text
		}
	}
	return grid
}

// Markdown returns the table as a Markdown pipe table. Markdown has one
// header row. Where the table's header rows are known, they are that row:
// each column's heading is what the header rows hold over it, from the top
// down, joined with " / ", and a heading merged across several columns is
// repeated over each. Where they are not known, the first row is the header
// row. Rows and columns with no text are left out, and the lines of a cell
// are joined.
func (t Table) Markdown() string {
	header, body := t.headed()
	if header == nil {
		return ""
	}
	return pipeTable(header, body)
}

// headed returns the table's text ready for Markdown, as a header row and
// the rows under it: each cell on one line, with empty rows and columns
// removed. The header is nil when the table has no text.
func (t Table) headed() (header []string, body [][]string) {
	if t.HeaderRows < 2 || t.Rows < 2 {
		rows := t.rows()
		if len(rows) == 0 {
			return nil, nil
		}
		return rows[0], rows[1:]
	}
	levels := min(t.HeaderRows, t.Rows-1)
	grid := t.Grid()
	// The headings over each column, from the top level down.
	over := make([][]string, t.Cols)
	for r := 0; r < levels; r++ {
		for _, c := range t.Cells {
			text := cellLine(c.Text)
			if c.Row != r || text == "" || c.Col < 0 || c.Col >= t.Cols {
				continue
			}
			span := max(1, c.ColSpan)
			if span >= t.Cols {
				span = 1 // a line across the whole table heads no one column
			}
			for col := c.Col; col < min(c.Col+span, t.Cols); col++ {
				if n := len(over[col]); n == 0 || over[col][n-1] != text {
					over[col] = append(over[col], text)
				}
			}
		}
	}
	// Rows and columns are kept as rows does it: a column stays when a cell
	// that stands in it has text.
	keepCol := make([]bool, t.Cols)
	for r, row := range grid {
		empty := true
		for c, text := range row {
			row[c] = cellLine(text)
			if row[c] != "" {
				keepCol[c], empty = true, false
			}
		}
		if r >= levels && !empty {
			body = append(body, row)
		}
	}
	for c := range over {
		if keepCol[c] {
			header = append(header, strings.Join(over[c], " / "))
		}
	}
	if len(header) == 0 {
		return nil, nil
	}
	for r, row := range body {
		out := row[:0]
		for c, text := range row {
			if keepCol[c] {
				out = append(out, text)
			}
		}
		body[r] = out
	}
	return header, body
}

// rows returns the table's text ready for Markdown: each cell on one line,
// with empty rows and columns removed.
func (t Table) rows() [][]string {
	grid := t.Grid()
	keepCol := make([]bool, t.Cols)
	var rows [][]string
	for _, row := range grid {
		empty := true
		for c, text := range row {
			row[c] = cellLine(text)
			if row[c] != "" {
				keepCol[c], empty = true, false
			}
		}
		if !empty {
			rows = append(rows, row)
		}
	}
	for r, row := range rows {
		out := row[:0]
		for c, text := range row {
			if keepCol[c] {
				out = append(out, text)
			}
		}
		rows[r] = out
	}
	return rows
}

// cellLine puts the text of a cell on one line.
func cellLine(text string) string {
	lines := strings.Split(text, "\n")
	out := ""
	for _, l := range lines {
		l = strings.TrimSpace(l)
		switch {
		case l == "":
		case out == "":
			out = l
		default:
			// A word hyphenated across the lines of a cell.
			last := out[strings.LastIndexByte(out, ' ')+1:]
			first, rest, _ := strings.Cut(l, " ")
			if stem, ok := layout.Hyphenated(last, first); ok {
				out = out[:len(out)-len(last)] + stem + first
				if rest != "" {
					out += " " + rest
				}
			} else {
				out += " " + l
			}
		}
	}
	return strings.ReplaceAll(out, "|", "\\|")
}

// pipeTable writes a header row and data rows as a Markdown table. With a
// nil header it writes the data rows alone, to continue a table.
func pipeTable(header []string, rows [][]string) string {
	var b strings.Builder
	line := func(cells []string) {
		b.WriteString("|")
		for _, c := range cells {
			b.WriteString(" ")
			b.WriteString(c)
			b.WriteString(" |")
		}
		b.WriteString("\n")
	}
	if header != nil {
		line(header)
		b.WriteString("|")
		for range header {
			b.WriteString(" --- |")
		}
		b.WriteString("\n")
	}
	for _, r := range rows {
		line(r)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// Tables returns the tables on the page, from the top down.
//
// A tagged document marks its tables itself. Otherwise a table is found
// from ruling lines that form a grid or, where there are none, from text
// that lines up in columns over three or more rows. Neither of those is
// certain, so each table carries a confidence. The text of every table
// is also part of Blocks and Text, as lines read row by row, which is what
// to use when the confidence is low.
func (p *Page) Tables(ctx context.Context) ([]Table, error) {
	if p.tabled {
		return p.tables, nil
	}
	laid, err := p.layout(ctx, true)
	if err != nil {
		return nil, err
	}
	// The tables the document marks itself come first; the search by rules
	// and alignment is for the rest of the page.
	out := slices.Clone(p.structTables)
	marked := func(x, y float64) bool {
		for _, t := range out {
			if x >= t.Box.X0 && x <= t.Box.X1 && y >= t.Box.Y0 && y <= t.Box.Y1 {
				return true
			}
		}
		return false
	}
	var words []table.Word
	var lines []table.Line
	rtl := 0
	for _, b := range laid {
		if b.Furniture || b.Angle != 0 {
			continue
		}
		if p.tags != nil && b.Group > 0 && b.Group <= len(p.tags.groups) && p.tags.groups[b.Group-1].table != 0 {
			continue
		}
		for _, l := range b.Lines {
			tl := table.Line{X0: l.Box.X0, Y0: l.Box.Y0, X1: l.Box.X1, Y1: l.Box.Y1, Words: make([]int, len(l.Words))}
			for k, w := range l.Words {
				tl.Words[k] = len(words)
				words = append(words, table.Word{Text: w.Text, X0: w.Box.X0, Y0: w.Box.Y0, X1: w.Box.X1, Y1: w.Box.Y1, Size: w.Size, Bold: w.Bold})
			}
			if l.RTL {
				// A right-to-left line has its words in reading order; the
				// search for columns wants them left to right.
				rtl++
				sort.SliceStable(tl.Words, func(i, j int) bool { return words[tl.Words[i]].X0 < words[tl.Words[j]].X0 })
			}
			lines = append(lines, tl)
		}
	}

	var found []table.Table
	var rules []table.Rule
	if p.content != nil && len(words) > 0 {
		rules = make([]table.Rule, 0, len(p.content.rules))
		for _, r := range p.content.rules {
			b := p.displayBox(Rect{r.X0, r.Y0, r.X1, r.Y1})
			// Rotation keeps a rule horizontal or vertical; make the pair
			// of coordinates that should be equal exactly so.
			if b.X1-b.X0 >= b.Y1-b.Y0 {
				b.Y0, b.Y1 = (b.Y0+b.Y1)/2, (b.Y0+b.Y1)/2
			} else {
				b.X0, b.X1 = (b.X0+b.X1)/2, (b.X0+b.X1)/2
			}
			rules = append(rules, table.Rule(b))
		}
		for _, t := range table.Ruled(words, rules) {
			// A grid of one column is a list in a box. If its text lines up
			// in columns, the search below finds them. A grid around a
			// table already known from the document's tags adds nothing.
			if t.Cols > 1 && !marked((t.X0+t.X1)/2, (t.Y0+t.Y1)/2) {
				found = append(found, t)
			}
		}
	}
	rest := lines[:0:0]
	for _, l := range lines {
		mx, my := (l.X0+l.X1)/2, (l.Y0+l.Y1)/2
		in := false
		for _, t := range found {
			in = in || (mx >= t.X0 && mx <= t.X1 && my >= t.Y0 && my <= t.Y1)
		}
		if !in {
			rest = append(rest, l)
		}
	}
	aligned, _ := table.Aligned(words, rest, rules)
	found = append(found, aligned...)
	for _, t := range found {
		if rtl*2 > len(lines) {
			mirror(&t)
		}
		made := Table{Box: Box{t.X0, t.Y0, t.X1, t.Y1}, Rows: t.Rows, Cols: t.Cols, HeaderRows: t.HeaderRows,
			Confidence: math.Round(t.Confidence*100) / 100, Method: t.Method, Cells: make([]TableCell, len(t.Cells))}
		for k, c := range t.Cells {
			made.Cells[k] = TableCell{Row: c.Row, Col: c.Col, RowSpan: c.RowSpan, ColSpan: c.ColSpan, Box: Box{c.X0, c.Y0, c.X1, c.Y1}, Text: cleanCell(c.Text)}
		}
		trimRows(&made)
		if made.Rows > 0 {
			out = append(out, made)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if math.Abs(out[i].Box.Y0-out[j].Box.Y0) > 1 {
			return out[i].Box.Y0 < out[j].Box.Y0
		}
		return out[i].Box.X0 < out[j].Box.X0
	})
	p.tables, p.tabled = out, true
	return out, nil
}

// cleanCell takes out of a cell's text what leads the eye and says nothing:
// the row of dots that runs from a label to its figures, or stands in a
// cell that has no figure.
func cleanCell(text string) string {
	lines := strings.Split(text, "\n")
	out := lines[:0]
	for _, l := range lines {
		if l = strings.TrimSpace(trimLeaders(l)); l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// trimLeaders removes a run of three or more dots, with or without spaces
// between them, from the end of a line and from its start.
func trimLeaders(s string) string {
	isDot := func(r byte) bool { return r == '.' || r == ' ' }
	end, dots := len(s), 0
	for end > 0 && isDot(s[end-1]) {
		if s[end-1] == '.' {
			dots++
		}
		end--
	}
	if dots >= 3 {
		s = s[:end]
	}
	start, dots := 0, 0
	for start < len(s) && isDot(s[start]) {
		if s[start] == '.' {
			dots++
		}
		start++
	}
	if dots >= 3 {
		s = s[start:]
	}
	return s
}

// trimRows takes off the top and the bottom of a table the rows that are
// not rows of it: a title above the header and notes below the last row,
// which some documents draw inside the table's outer box or tag as rows.
// Such a row has text in its first cell only, and that cell runs the width
// of the table or is the only one the row has. The table's box shrinks to
// the rows that are left, so that the text taken off reads as text around
// the table. It returns the cells taken off.
func trimRows(t *Table) []TableCell {
	if t.Cols < 2 {
		return nil
	}
	type rowInfo struct {
		cells, filled int
		first         *TableCell // the cell with text, when there is one
	}
	info := make([]rowInfo, t.Rows)
	for i := range t.Cells {
		c := &t.Cells[i]
		if c.Row < 0 || c.Row >= t.Rows {
			continue
		}
		info[c.Row].cells++
		if c.Text != "" {
			info[c.Row].filled++
			info[c.Row].first = c
		}
	}
	apart := func(r int) bool {
		in := info[r]
		return in.filled == 1 && in.first.Col == 0 && (in.first.ColSpan >= t.Cols || in.cells == 1)
	}
	empty := func(r int) bool { return info[r].filled == 0 }
	// At least two rows must be left for there to be a table. Empty rows
	// go with a title or a note beside them, and are no reason on their
	// own to cut anything.
	top, bottom := 0, t.Rows
	title, note := false, false
	for top < bottom-2 && (apart(top) || empty(top)) {
		title = title || apart(top)
		top++
	}
	if !title {
		top = 0
	}
	for bottom > top+2 && (apart(bottom-1) || empty(bottom-1)) {
		note = note || apart(bottom-1)
		bottom--
	}
	if !note {
		bottom = t.Rows
	}
	if top == 0 && bottom == t.Rows {
		return nil
	}
	var kept, cut []TableCell
	box := Box{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	for _, c := range t.Cells {
		if c.Row < top || c.Row >= bottom {
			cut = append(cut, c)
			continue
		}
		c.Row -= top
		c.RowSpan = min(c.RowSpan, bottom-top-c.Row)
		kept = append(kept, c)
		box.X0, box.Y0 = math.Min(box.X0, c.Box.X0), math.Min(box.Y0, c.Box.Y0)
		box.X1, box.Y1 = math.Max(box.X1, c.Box.X1), math.Max(box.Y1, c.Box.Y1)
	}
	if len(kept) == 0 {
		return nil
	}
	t.Cells, t.Rows, t.Box = kept, bottom-top, box
	t.HeaderRows = max(0, min(t.HeaderRows-top, t.Rows-1))
	return cut
}

// mirror renumbers the columns of a table on a right-to-left page, where
// the first column is the rightmost.
func mirror(t *table.Table) {
	for i := range t.Cells {
		c := &t.Cells[i]
		c.Col = t.Cols - c.Col - c.ColSpan
	}
	sort.SliceStable(t.Cells, func(i, j int) bool {
		if t.Cells[i].Row != t.Cells[j].Row {
			return t.Cells[i].Row < t.Cells[j].Row
		}
		return t.Cells[i].Col < t.Cells[j].Col
	})
}
