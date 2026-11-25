// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package unpdf

import (
	"context"
	"math"
	"slices"
	"strings"

	"github.com/gomcpgo/unpdf/internal/layout"
	"github.com/gomcpgo/unpdf/internal/pdfobj"
)

// A tagged PDF carries a structure tree: its paragraphs, headings, lists
// and tables as a tree of elements in reading order, each leaf pointing at
// the marked content on a page that shows it. Where a page has one, it
// says what the layout can only guess: which text belongs together, in
// what order it reads, and what it is.
//
// The tree is read from the page upward. A page's entry in the parent tree
// lists, for each marked-content identifier on the page, the element that
// owns it; following the elements' parent links gives each one's place in
// the tree. Nothing of the tree outside the page's own branches is read,
// so a long document costs no more than its pages.

const (
	// maxStructDepth caps how far up the tree an element's parents are
	// followed.
	maxStructDepth = 64
	// maxKidIndex caps the remembered positions of elements among their
	// siblings, over the whole document.
	maxKidIndex = 1 << 20
	// maxPageMCIDs caps the marked-content identifiers looked up on a page.
	maxPageMCIDs = 200_000
	// treeBudget caps the nodes visited in one search of a name tree or a
	// number tree, so that a tree made to branch without end costs little.
	treeBudget = 20_000
)

// structTree is what the document says about its structure tree.
type structTree struct {
	parents pdfobj.Dict // the parent tree
	roleMap pdfobj.Dict
	// kids gives, for an element's object number, the position of each of
	// its children by object number.
	kids     map[int]map[int]int32
	kidCount int
}

// structure returns the document's structure tree, or nil when it has none
// that can be used.
func (d *Document) structure(ctx context.Context) *structTree {
	if d.tagsChecked {
		return d.tagTree
	}
	f := d.f
	root := f.Dict(ctx, d.catalog["StructTreeRoot"])
	parents := f.Dict(ctx, root["ParentTree"])
	// A document that calls its own tags suspect is taken at its word.
	suspect, _ := f.Bool(ctx, f.Dict(ctx, d.catalog["MarkInfo"])["Suspects"])
	if ctx.Err() != nil {
		return nil
	}
	d.tagsChecked = true
	if parents != nil && !suspect {
		d.tagTree = &structTree{parents: parents, roleMap: f.Dict(ctx, root["RoleMap"]), kids: map[int]map[int]int32{}}
	}
	return d.tagTree
}

// numberTree looks a key up in a number tree.
func (d *Document) numberTree(ctx context.Context, node pdfobj.Dict, key int64, depth int, budget *int) pdfobj.Object {
	f := d.f
	if node == nil || depth > maxNameTreeDepth || *budget <= 0 || ctx.Err() != nil {
		return nil
	}
	*budget--
	if nums := f.Array(ctx, node["Nums"]); nums != nil {
		for i := 0; i+1 < len(nums); i += 2 {
			if k, ok := f.Int(ctx, nums[i]); ok && k == key {
				return nums[i+1]
			}
		}
	}
	for _, kid := range f.Array(ctx, node["Kids"]) {
		kd := f.Dict(ctx, kid)
		if limits := f.Array(ctx, kd["Limits"]); len(limits) >= 2 {
			lo, ok1 := f.Int(ctx, limits[0])
			hi, ok2 := f.Int(ctx, limits[1])
			if ok1 && ok2 && (key < lo || key > hi) {
				continue
			}
		}
		if v := d.numberTree(ctx, kd, key, depth+1, budget); v != nil {
			return v
		}
	}
	return nil
}

// standardRoles are the structure types the PDF standard defines. Any other
// type is looked up in the document's role map.
var standardRoles = func() map[pdfobj.Name]bool {
	m := map[pdfobj.Name]bool{}
	for _, r := range strings.Fields(`Document DocumentFragment Part Art Sect Div BlockQuote Caption TOC TOCI Index
		NonStruct Private Aside Title P H H1 H2 H3 H4 H5 H6 L LI Lbl LBody Table TR TH TD THead TBody TFoot
		Span Quote Note FENote Reference BibEntry Code Link Annot Ruby RB RT RP Warichu WT WP Sub Em Strong
		Figure Formula Form Artifact`) {
		m[pdfobj.Name(r)] = true
	}
	return m
}()

// role returns the standard structure type an element's type stands for.
func (st *structTree) role(ctx context.Context, d *Document, name pdfobj.Name) string {
	for i := 0; i < 8 && name != ""; i++ {
		if standardRoles[name] || headingNumber(string(name)) > 0 {
			return string(name)
		}
		next := d.f.Name(ctx, st.roleMap[name])
		if next == "" || next == name {
			break
		}
		name = next
	}
	return string(name)
}

// headingNumber returns n for the role Hn, and 0 for any other.
func headingNumber(role string) int {
	if len(role) < 2 || len(role) > 3 || role[0] != 'H' {
		return 0
	}
	n := 0
	for _, c := range role[1:] {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// blockRole reports whether elements of this type are blocks of text in
// their own right: what they hold is read as one piece.
func blockRole(role string) bool {
	switch role {
	case "P", "H", "Title", "LI", "TD", "TH", "Caption", "TOCI", "Note", "FENote", "Figure", "Formula", "BibEntry":
		return true
	}
	return headingNumber(role) > 0
}

// tagGroup is a block of a page as the structure tree gives it.
type tagGroup struct {
	elem  int     // object number of the block's element
	path  []int32 // the element's place in the tree: its position at each level
	role  string
	kind  BlockKind
	level int
	// The table cell the block is in: the object numbers of the table, the
	// row and the cell, and how long the part of path is that leads to
	// each. All zero outside tables.
	table, row, cell                int
	tableDepth, rowDepth, cellDepth int
	header                          bool
	rowSpan, colSpan                int
	// note marks a footnote. The tree puts it where the text refers to it;
	// on the page it stands at the foot, and it is read there.
	note bool
}

// pageTags is the structure tree's account of one page.
type pageTags struct {
	// group gives, for each marked-content identifier, the number of the
	// block it is part of, counting from 1 in reading order; 0 for none.
	group  []int32
	groups []tagGroup
}

// structNode is an element on the way up from a page's content.
type structNode struct {
	ref    pdfobj.Ref
	dict   pdfobj.Dict
	role   string
	parent *structNode // nil for a child of the tree's root
	root   pdfobj.Ref  // the tree's root, for a child of it
	path   []int32
	depth  int
}

// tagReader reads the branches of the structure tree that reach one page.
type tagReader struct {
	ctx   context.Context
	d     *Document
	st    *structTree
	nodes map[int]*structNode
}

// node returns the element with the given reference, with its parents.
func (r *tagReader) node(ref pdfobj.Ref, depth int) *structNode {
	if n, ok := r.nodes[ref.Num]; ok {
		return n
	}
	r.nodes[ref.Num] = nil // cuts a loop of parents
	f := r.d.f
	dict := f.Dict(r.ctx, ref)
	s := f.Name(r.ctx, dict["S"])
	if dict == nil || s == "" || depth > maxStructDepth {
		return nil
	}
	n := &structNode{ref: ref, dict: dict, role: r.st.role(r.ctx, r.d, s)}
	if up, ok := dict["P"].(pdfobj.Ref); ok {
		n.parent = r.node(up, depth+1)
		position := r.position(up, ref.Num)
		if n.parent != nil {
			n.path = append(slices.Clip(n.parent.path), position)
			n.depth = n.parent.depth + 1
		} else {
			n.path = []int32{position}
		}
	}
	r.nodes[ref.Num] = n
	return n
}

// position returns where a child stands among the children of its parent.
func (r *tagReader) position(parent pdfobj.Ref, child int) int32 {
	st := r.st
	index, ok := st.kids[parent.Num]
	if !ok {
		f := r.d.f
		index = map[int]int32{}
		kids := f.Dict(r.ctx, parent)["K"]
		if ref, isRef := kids.(pdfobj.Ref); isRef {
			if arr := f.Array(r.ctx, ref); arr != nil {
				kids = arr
			} else {
				index[ref.Num] = 0
			}
		}
		if arr, isArray := kids.(pdfobj.Array); isArray {
			for i, k := range arr {
				if ref, isRef := k.(pdfobj.Ref); isRef {
					if _, seen := index[ref.Num]; !seen {
						index[ref.Num] = int32(min(i, 1<<30))
					}
				}
			}
		}
		if r.ctx.Err() != nil {
			return 0
		}
		if st.kidCount+len(index) > maxKidIndex {
			clear(st.kids)
			st.kidCount = 0
		}
		st.kids[parent.Num] = index
		st.kidCount += len(index)
	}
	return index[child]
}

// group works out the block that the content owned by an element is part
// of.
func (r *tagReader) group(leaf pdfobj.Ref) (tagGroup, bool) {
	n := r.node(leaf, 0)
	if n == nil {
		return tagGroup{}, false
	}
	var chain []*structNode
	for e := n; e != nil; e = e.parent {
		chain = append(chain, e)
	}
	// The block is the nearest element of a block type, or the element
	// itself when it sits loose in a section.
	g := 0
	for i, e := range chain {
		if blockRole(e.role) {
			g = i
			break
		}
	}
	// A list item is one block, label and body together, even where its
	// body is tagged as a paragraph of its own.
	if chain[g].role != "LI" {
	up:
		for i := g + 1; i < len(chain); i++ {
			switch chain[i].role {
			case "LI":
				g = i
				break up
			case "L", "Table", "TR", "TD", "TH", "TOC":
				break up
			}
		}
	}
	e := chain[g]
	out := tagGroup{elem: e.ref.Num, path: e.path, role: e.role}
	for _, a := range chain {
		out.note = out.note || a.role == "Note" || a.role == "FENote"
	}
	switch {
	case headingNumber(e.role) > 0:
		out.kind, out.level = Heading, min(headingNumber(e.role), 6)
	case e.role == "Title":
		out.kind, out.level = Heading, 1
	case e.role == "H":
		// An unnumbered heading takes its level from the sections it is in.
		out.kind = Heading
		for _, a := range chain[g+1:] {
			if a.role == "Sect" || a.role == "Art" || a.role == "Part" {
				out.level++
			}
		}
		out.level = max(1, min(out.level, 6))
	case e.role == "LI":
		out.kind, out.level = ListItem, -1
		for _, a := range chain[g+1:] {
			if a.role == "L" {
				out.level++
			}
		}
		out.level = max(0, out.level)
	}
	// The table cell, row and table the block is in.
	f := r.d.f
	for i := g; i < len(chain); i++ {
		a := chain[i]
		switch {
		case a.role == "Table" && out.cell == 0:
			return out, true // in a table but not in a cell: its caption
		case (a.role == "TD" || a.role == "TH") && out.cell == 0:
			out.cell, out.cellDepth, out.header = a.ref.Num, len(a.path), a.role == "TH"
			out.rowSpan, out.colSpan = 1, 1
			attrs := resolveQuiet(r.ctx, f, a.dict["A"])
			list, _ := attrs.(pdfobj.Array)
			if dict, ok := attrs.(pdfobj.Dict); ok {
				list = pdfobj.Array{dict}
			}
			for _, item := range list {
				dict := f.Dict(r.ctx, item)
				if v, ok := f.Int(r.ctx, dict["RowSpan"]); ok && v >= 1 && v <= 1000 {
					out.rowSpan = int(v)
				}
				if v, ok := f.Int(r.ctx, dict["ColSpan"]); ok && v >= 1 && v <= 1000 {
					out.colSpan = int(v)
				}
			}
		case a.role == "TR" && out.cell != 0 && out.row == 0:
			out.row, out.rowDepth = a.ref.Num, len(a.path)
		case a.role == "THead" && out.cell != 0:
			out.header = true
		case a.role == "Table" && out.row != 0:
			out.table, out.tableDepth = a.ref.Num, len(a.path)
			return out, true
		case a.role == "Table":
			i = len(chain) // a cell with no row: not a table to rely on
		}
	}
	// A cell with no row or no table around it is no cell.
	out.table, out.row, out.cell = 0, 0, 0
	return out, true
}

// readTags returns the structure tree's account of the page, or nil when
// the document has no structure tree or the page has no entry in it.
func (p *Page) readTags(ctx context.Context) *pageTags {
	d := p.doc
	st := d.structure(ctx)
	if st == nil {
		return nil
	}
	key, ok := d.f.Int(ctx, p.dict["StructParents"])
	if !ok {
		return nil
	}
	budget := treeBudget
	owners := d.f.Array(ctx, d.numberTree(ctx, st.parents, key, 0, &budget))
	if len(owners) == 0 {
		return nil
	}
	if len(owners) > maxPageMCIDs {
		owners = owners[:maxPageMCIDs]
	}
	r := &tagReader{ctx: ctx, d: d, st: st, nodes: map[int]*structNode{}}
	tags := &pageTags{group: make([]int32, len(owners))}
	byElem := map[int]int32{} // a block's element to its number
	byLeaf := map[int]int32{} // an owning element to its block's number
	for mcid, o := range owners {
		ref, ok := o.(pdfobj.Ref)
		if !ok {
			continue
		}
		number, seen := byLeaf[ref.Num]
		if !seen {
			if g, ok := r.group(ref); ok {
				if number, ok = byElem[g.elem]; !ok {
					tags.groups = append(tags.groups, g)
					number = int32(len(tags.groups))
					byElem[g.elem] = number
				}
			}
			byLeaf[ref.Num] = number
		}
		tags.group[mcid] = number
	}
	if ctx.Err() != nil || len(tags.groups) == 0 {
		return nil
	}
	// Number the blocks in the order of the tree, which is reading order.
	order := make([]int32, len(tags.groups))
	for i := range order {
		order[i] = int32(i)
	}
	slices.SortStableFunc(order, func(a, b int32) int {
		ga, gb := &tags.groups[a], &tags.groups[b]
		if ga.note != gb.note {
			if gb.note {
				return -1
			}
			return 1
		}
		return slices.Compare(ga.path, gb.path)
	})
	renumber := make([]int32, len(order)+1)
	sorted := make([]tagGroup, len(order))
	for to, from := range order {
		renumber[from+1] = int32(to + 1)
		sorted[to] = tags.groups[from]
	}
	tags.groups = sorted
	for i, g := range tags.group {
		tags.group[i] = renumber[g]
	}
	return tags
}

// taggedTables builds the tables that the structure tree marks on the
// page, from the blocks laid out for its cells.
func (p *Page) taggedTables(laid []layout.Block) []Table {
	if p.tags == nil {
		return nil
	}
	type cell struct {
		g    *tagGroup
		text []string
		box  Box
	}
	type tableParts struct {
		cells map[int]*cell
		order []int // cell elements, as met
	}
	parts := map[int]*tableParts{}
	var order []int
	for _, b := range laid {
		if b.Group <= 0 || b.Group > len(p.tags.groups) || b.Furniture {
			continue
		}
		g := &p.tags.groups[b.Group-1]
		if g.table == 0 || b.Angle != 0 {
			continue
		}
		t := parts[g.table]
		if t == nil {
			t = &tableParts{cells: map[int]*cell{}}
			parts[g.table] = t
			order = append(order, g.table)
		}
		c := t.cells[g.cell]
		if c == nil {
			c = &cell{g: g, box: Box(b.Box)}
			t.cells[g.cell] = c
			t.order = append(t.order, g.cell)
		}
		c.text = append(c.text, b.Text())
		c.box.X0, c.box.Y0 = min(c.box.X0, b.Box.X0), min(c.box.Y0, b.Box.Y0)
		c.box.X1, c.box.Y1 = max(c.box.X1, b.Box.X1), max(c.box.Y1, b.Box.Y1)
	}

	var out []Table
	for _, id := range order {
		t := parts[id]
		cells := make([]*cell, 0, len(t.order))
		for _, c := range t.order {
			cells = append(cells, t.cells[c])
		}
		// Row by row, and along each row, in the order of the tree.
		slices.SortStableFunc(cells, func(a, b *cell) int {
			return slices.Compare(a.g.path[:a.g.cellDepth], b.g.path[:b.g.cellDepth])
		})
		// The tree's order along a row should be the order across the
		// page. Some producers put a cell that spans several rows at the
		// end of its row's cells; where a row's order and its positions on
		// the page disagree, the page decides.
		forward, backward := 0, 0
		for i := 1; i < len(cells); i++ {
			if cells[i].g.row == cells[i-1].g.row {
				if cells[i].box.X0 >= cells[i-1].box.X0 {
					forward++
				} else {
					backward++
				}
			}
		}
		rtl := backward > forward
		moved := map[int]bool{} // rows put in order by position
		for from := 0; from < len(cells); {
			to := from + 1
			for to < len(cells) && cells[to].g.row == cells[from].g.row {
				to++
			}
			row := cells[from:to]
			before := func(a, b *cell) int {
				switch d := a.box.X0 - b.box.X0; {
				case d == 0:
					return 0
				case (d < 0) != rtl:
					return -1
				}
				return 1
			}
			if !slices.IsSortedFunc(row, before) {
				slices.SortStableFunc(row, before)
				moved[row[0].g.row] = true
			}
			from = to
		}

		table := Table{Method: TableFromStructure, Confidence: 0.95, Box: cells[0].box}
		taken := map[[2]int]bool{}
		elems := map[[2]int]int{} // the element of the cell placed at a row and column
		row, col, last := -1, 0, int32(-1)
		rowElem := 0
		headers := true
		for _, c := range cells {
			g := c.g
			if g.row != rowElem {
				if row >= 0 && headers {
					table.HeaderRows = row + 1
				}
				row, col, last, rowElem = row+1, 0, -1, g.row
			}
			headers = headers && g.header
			// Cells with nothing in them are not on the page, but each
			// still takes its place in the row.
			if g.cellDepth == g.rowDepth+1 && !moved[g.row] {
				position := g.path[g.cellDepth-1]
				for skip := min(position-last-1, 100); skip > 0; skip-- {
					for taken[[2]int{row, col}] {
						col++
					}
					col++
				}
				last = position
			}
			for taken[[2]int{row, col}] {
				col++
			}
			if row >= maxTableSide || col >= maxTableSide {
				continue
			}
			rowSpan, colSpan := min(g.rowSpan, maxTableSide-row), min(g.colSpan, maxTableSide-col)
			for r := row; r < row+rowSpan; r++ {
				for k := col; k < col+colSpan; k++ {
					taken[[2]int{r, k}] = true
				}
			}
			table.Cells = append(table.Cells, TableCell{Row: row, Col: col, RowSpan: rowSpan, ColSpan: colSpan,
				Box: c.box, Text: cleanCell(strings.Join(c.text, "\n"))})
			elems[[2]int{row, col}] = g.cell
			table.Rows, table.Cols = max(table.Rows, row+1), max(table.Cols, col+colSpan)
			table.Box.X0, table.Box.Y0 = min(table.Box.X0, c.box.X0), min(table.Box.Y0, c.box.Y0)
			table.Box.X1, table.Box.Y1 = max(table.Box.X1, c.box.X1), max(table.Box.Y1, c.box.Y1)
			col += colSpan
		}
		if table.HeaderRows == table.Rows {
			table.HeaderRows = 0 // every row a heading: no word on which are
		}
		// A title and notes tagged as rows of the table are text around it.
		// Their places are looked up before the cells are moved about.
		for _, c := range trimRows(&table) {
			if p.looseCells == nil {
				p.looseCells = map[int]bool{}
			}
			p.looseCells[elems[[2]int{c.Row, c.Col}]] = true
		}
		if !rtl {
			settleColumns(&table)
		}
		switch {
		case table.Cols < 2:
			continue // one column: a table used to set out a page
		case table.Rows < 2:
			table.Confidence = 0.4
		}
		out = append(out, table)
	}
	return out
}

// settleColumns moves the cells of a short row under the columns they
// stand in. The tree gives a row's cells in order but, unless it also says
// how many columns each one spans, not which columns they are in: a
// heading over two columns, or a row that starts under a label spanning
// several rows, leaves the cells after it one column out. Where the table
// has a row with a cell in every column, that row shows where the columns
// are, and a row with fewer cells and no spans of its own is set by it.
func settleColumns(t *Table) {
	rows := make([][]int, t.Rows) // the cells of each row, by index
	spanned := make([]bool, t.Rows)
	for i, c := range t.Cells {
		rows[c.Row] = append(rows[c.Row], i)
		for r := c.Row; r < min(c.Row+c.RowSpan, t.Rows); r++ {
			spanned[r] = spanned[r] || c.RowSpan > 1 || c.ColSpan > 1
		}
	}
	// Where each column's text lies, from the rows that fill every column.
	type extent struct{ x0, x1 float64 }
	columns := make([]extent, t.Cols)
	guides := 0
	for r, cells := range rows {
		if len(cells) != t.Cols || spanned[r] {
			continue
		}
		for k, i := range cells {
			box := t.Cells[i].Box
			if guides == 0 {
				columns[k] = extent{box.X0, box.X1}
			}
			columns[k].x0, columns[k].x1 = math.Min(columns[k].x0, box.X0), math.Max(columns[k].x1, box.X1)
		}
		guides++
	}
	if guides == 0 || t.Cols < 2 {
		return
	}
	for r, cells := range rows {
		if len(cells) == 0 || len(cells) >= t.Cols || spanned[r] {
			continue
		}
		// Each cell goes under the first column, after the cell before
		// it, whose text it stands over; or failing that the nearest.
		cols := make([]int, len(cells))
		next, ok := 0, true
		for k, i := range cells {
			box := t.Cells[i].Box
			found, nearest, distance := -1, -1, math.Inf(1)
			for c := next; c < t.Cols; c++ {
				if box.X0 < columns[c].x1 && box.X1 > columns[c].x0 {
					found = c
					break
				}
				if d := math.Abs((box.X0+box.X1)/2 - (columns[c].x0+columns[c].x1)/2); d < distance {
					nearest, distance = c, d
				}
			}
			if found < 0 {
				found = nearest
			}
			if found < 0 {
				ok = false // more cells than columns left
				break
			}
			cols[k], next = found, found+1
		}
		if !ok {
			continue
		}
		for k, i := range cells {
			t.Cells[i].Col = cols[k]
		}
	}
}

// maxTableSide caps the rows and columns of a table read from tags.
const maxTableSide = 1000

// foldRows puts back together what the structure tree takes apart further
// than a reader of the page would: the cells of a table, each a block of
// its own, become one block per table with a line for each row, and
// paragraphs of one line that stand side by side become one line. That is
// how such text reads on a page without tags, and it keeps a row of
// figures on one line of the text.
func (p *Page) foldRows(blocks []layout.Block) []layout.Block {
	tag := func(b *layout.Block) *tagGroup {
		if b.Group <= 0 || b.Group > len(p.tags.groups) {
			return nil
		}
		g := &p.tags.groups[b.Group-1]
		if p.looseCells[g.cell] {
			// A title or a note tagged as a cell: text like any other.
			loose := *g
			loose.table, loose.row, loose.cell = 0, 0, 0
			return &loose
		}
		return g
	}
	mid := func(l layout.Line) float64 { return (l.Box.Y0 + l.Box.Y1) / 2 }
	level := func(a, b layout.Line) bool {
		return math.Abs(mid(a)-mid(b)) <= 0.5*math.Min(a.Box.Y1-a.Box.Y0, b.Box.Y1-b.Box.Y0)
	}
	out := make([]layout.Block, 0, len(blocks))
	// The table row that the last block's final lines belong to, and where
	// in the block those lines start.
	lastRow, rowStart := 0, 0
	for _, b := range blocks {
		g := tag(&b)
		if n := len(out); n > 0 && g != nil && b.Angle == 0 && !b.Furniture && len(b.Lines) > 0 {
			prev := &out[n-1]
			pg := tag(prev)
			ok := pg != nil && prev.Angle == 0 && !prev.Furniture
			switch {
			case ok && g.table != 0 && g.table == pg.table && g.row == lastRow:
				// Another cell of the same row: its lines join the row's
				// lines that are level with them.
				for _, l := range b.Lines {
					at := rowStart
					for at < len(prev.Lines) && !level(prev.Lines[at], l) && mid(prev.Lines[at]) < mid(l) {
						at++
					}
					if at < len(prev.Lines) && level(prev.Lines[at], l) {
						joinLine(&prev.Lines[at], l)
					} else {
						prev.Lines = slices.Insert(prev.Lines, at, l)
					}
				}
				grow(prev, b.Box)
				continue
			case ok && g.table != 0 && g.table == pg.table:
				// The first cell of the table's next row.
				rowStart = len(prev.Lines)
				prev.Lines = append(prev.Lines, b.Lines...)
				grow(prev, b.Box)
				lastRow = g.row
				continue
			case ok && g.table == 0 && pg.table == 0 && g.kind == Paragraph && pg.kind == Paragraph &&
				len(b.Lines) == 1 && len(prev.Lines) == 1 && level(prev.Lines[0], b.Lines[0]) &&
				(b.Box.X0 > prev.Box.X1 || b.Box.X1 < prev.Box.X0):
				joinLine(&prev.Lines[0], b.Lines[0])
				grow(prev, b.Box)
				continue
			}
		}
		lastRow, rowStart = 0, 0
		if g != nil {
			lastRow = g.row
		}
		// The lines are about to be added to: give the block its own.
		b.Lines = slices.Clone(b.Lines)
		out = append(out, b)
	}
	return out
}

// joinLine adds a line's words to another line on the same baseline, in
// reading order.
func joinLine(to *layout.Line, l layout.Line) {
	words := make([]layout.Word, 0, len(to.Words)+len(l.Words))
	if before := l.Box.X1 < to.Box.X0; before != to.RTL {
		words = append(append(words, l.Words...), to.Words...)
	} else {
		words = append(append(words, to.Words...), l.Words...)
	}
	to.Words = words
	to.Box.X0, to.Box.Y0 = math.Min(to.Box.X0, l.Box.X0), math.Min(to.Box.Y0, l.Box.Y0)
	to.Box.X1, to.Box.Y1 = math.Max(to.Box.X1, l.Box.X1), math.Max(to.Box.Y1, l.Box.Y1)
}

func grow(b *layout.Block, box layout.Box) {
	b.Box.X0, b.Box.Y0 = math.Min(b.Box.X0, box.X0), math.Min(b.Box.Y0, box.Y0)
	b.Box.X1, b.Box.Y1 = math.Max(b.Box.X1, box.X1), math.Max(b.Box.Y1, box.Y1)
}
