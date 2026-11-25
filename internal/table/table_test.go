// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package table

import (
	"encoding/binary"
	"math"
	"strings"
	"testing"
)

// page collects the words and rules of a made-up page.
type page struct {
	words []Word
	lines []Line
	rules []Rule
}

// text puts words on a baseline, each at its own x. Words are 5 points wide
// per character, in 10 point type. They form one line for Aligned.
func (p *page) text(y float64, cells ...any) {
	l := Line{X0: math.Inf(1), X1: math.Inf(-1), Y0: y - 8, Y1: y + 2}
	for i := 0; i+1 < len(cells); i += 2 {
		x := float64(cells[i].(int))
		for _, w := range strings.Fields(cells[i+1].(string)) {
			wd := Word{Text: w, X0: x, X1: x + 5*float64(len([]rune(w))), Y0: y - 8, Y1: y + 2, Size: 10}
			l.Words = append(l.Words, len(p.words))
			p.words = append(p.words, wd)
			l.X0, l.X1 = math.Min(l.X0, wd.X0), math.Max(l.X1, wd.X1)
			x = wd.X1 + 3
		}
	}
	p.lines = append(p.lines, l)
}

func (p *page) h(y, x0, x1 float64) { p.rules = append(p.rules, Rule{x0, y, x1, y}) }
func (p *page) v(x, y0, y1 float64) { p.rules = append(p.rules, Rule{x, y0, x, y1}) }

// grid rules off every line of a grid.
func (p *page) grid(xs, ys []float64) {
	for _, y := range ys {
		p.h(y, xs[0], xs[len(xs)-1])
	}
	for _, x := range xs {
		p.v(x, ys[0], ys[len(ys)-1])
	}
}

// rowsOf returns the table as rows of cell text, merged cells once.
func rowsOf(t Table) [][]string {
	out := make([][]string, t.Rows)
	for r := range out {
		out[r] = make([]string, t.Cols)
	}
	for _, c := range t.Cells {
		out[c.Row][c.Col] = c.Text
	}
	return out
}

func same(a, b [][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if strings.Join(a[i], "|") != strings.Join(b[i], "|") {
			return false
		}
	}
	return true
}

func TestRuledGrid(t *testing.T) {
	p := &page{}
	p.grid([]float64{50, 150, 250, 350}, []float64{100, 120, 140, 160})
	p.text(114, 55, "Region", 155, "Q2", 255, "Q3")
	p.text(134, 55, "North America", 155, "1,204", 255, "1,318")
	p.text(154, 55, "Europe", 155, "982", 255, "1,045")
	p.text(300, 55, "A paragraph well below the table.")
	tables := Ruled(p.words, p.rules)
	if len(tables) != 1 {
		t.Fatalf("%d tables", len(tables))
	}
	tb := tables[0]
	want := [][]string{{"Region", "Q2", "Q3"}, {"North America", "1,204", "1,318"}, {"Europe", "982", "1,045"}}
	if !same(rowsOf(tb), want) || tb.Method != "rules" || tb.Confidence < 0.9 {
		t.Errorf("table = %v, method %s, confidence %.2f", rowsOf(tb), tb.Method, tb.Confidence)
	}
	if tb.X0 != 50 || tb.Y0 != 100 || tb.X1 != 350 || tb.Y1 != 160 {
		t.Errorf("extent %v %v %v %v", tb.X0, tb.Y0, tb.X1, tb.Y1)
	}
	// The same grid drawn piecemeal, one cell edge at a time and a little
	// off, is the same table.
	q := &page{words: p.words}
	xs, ys := []float64{50, 150, 250, 350}, []float64{100, 120, 140, 160}
	for i, y := range ys {
		for c := 0; c+1 < len(xs); c++ {
			q.h(y+0.3*float64(i%2), xs[c], xs[c+1])
		}
	}
	for _, x := range xs {
		for r := 0; r+1 < len(ys); r++ {
			q.v(x, ys[r], ys[r+1])
		}
	}
	if got := Ruled(q.words, q.rules); len(got) != 1 || !same(rowsOf(got[0]), want) {
		t.Errorf("piecemeal grid: %d tables", len(got))
	}
}

func TestRuledMergedCells(t *testing.T) {
	p := &page{}
	xs, ys := []float64{50, 150, 250, 350}, []float64{100, 120, 140, 160}
	// The outline and every inner rule, except that the first row's last
	// two cells are one and so are the first column's last two.
	p.h(100, 50, 350)
	p.h(120, 50, 350)
	p.h(140, 150, 350) // stops short of the first column
	p.h(160, 50, 350)
	p.v(50, 100, 160)
	p.v(150, 100, 160)
	p.v(250, 120, 160) // starts below the first row
	p.v(350, 100, 160)
	_ = xs
	_ = ys
	p.text(114, 55, "Name", 155, "Sales in both halves")
	p.text(134, 55, "Total", 155, "10", 255, "20")
	p.text(154, 155, "30", 255, "40")
	tables := Ruled(p.words, p.rules)
	if len(tables) != 1 {
		t.Fatalf("%d tables", len(tables))
	}
	spans := map[string][2]int{}
	for _, c := range tables[0].Cells {
		spans[c.Text] = [2]int{c.RowSpan, c.ColSpan}
	}
	if spans["Sales in both halves"] != [2]int{1, 2} || spans["Total"] != [2]int{2, 1} || spans["Name"] != [2]int{1, 1} || len(tables[0].Cells) != 7 {
		t.Errorf("spans = %v, %d cells", spans, len(tables[0].Cells))
	}
}

func TestRuledRowsWithoutRules(t *testing.T) {
	// A header ruled off, columns ruled off, and five rows of figures with
	// nothing between them: a statistical table. The row labels stand to
	// the left of the first upright rule.
	p := &page{}
	p.h(100, 40, 350)
	p.h(120, 40, 350)
	p.h(220, 40, 350)
	for _, x := range []float64{150, 250, 350} {
		p.v(x, 100, 220)
	}
	p.text(114, 45, "State", 155, "2023", 255, "2024")
	for i, row := range [][3]string{{"Alabama", "2,120", "2,020"}, {"Alaska", "26", "31"}, {"Arizona", "629", "562"}, {"Arkansas", "6,990", "7,053"}, {"Colorado", "5,651", "5,933"}} {
		p.text(134+18*float64(i), 45, row[0], 155, row[1], 255, row[2])
	}
	tables := Ruled(p.words, p.rules)
	if len(tables) != 1 {
		t.Fatalf("%d tables", len(tables))
	}
	got := rowsOf(tables[0])
	if len(got) != 6 || strings.Join(got[0], "|") != "State|2023|2024" || strings.Join(got[3], "|") != "Arizona|629|562" || tables[0].Confidence < 0.9 {
		t.Errorf("rows = %v, confidence %.2f", got, tables[0].Confidence)
	}

	// A cell of prose that wraps over several lines is one cell.
	q := &page{}
	q.grid([]float64{50, 200, 400}, []float64{100, 120, 200})
	q.text(114, 55, "Function", 205, "What it is for")
	q.text(134, 55, "Govern", 205, "Policies and procedures are in place and")
	q.text(146, 205, "understood across the whole organisation")
	q.text(158, 205, "and are reviewed at planned intervals so")
	q.text(170, 205, "that they stay current.")
	tables = Ruled(q.words, q.rules)
	if len(tables) != 1 || tables[0].Rows != 2 || !strings.HasPrefix(tables[0].Cells[3].Text, "Policies and procedures are in place and\nunderstood") {
		t.Errorf("wrapped cell: %+v", tables)
	}
}

func TestRuledThingsThatAreNotTables(t *testing.T) {
	// A box around a paragraph.
	p := &page{}
	p.grid([]float64{50, 400}, []float64{100, 160})
	p.text(120, 60, "A note set in a box, which is not a table.")
	if got := Ruled(p.words, p.rules); len(got) != 0 {
		t.Errorf("a box gave %d tables", len(got))
	}

	// A form: boxes that differ from row to row.
	f := &page{}
	f.h(100, 50, 450)
	f.h(130, 50, 450)
	f.h(160, 50, 450)
	f.h(190, 50, 450)
	f.v(50, 100, 190)
	f.v(450, 100, 190)
	f.v(180, 100, 130)
	f.v(320, 100, 130)
	f.v(250, 130, 160)
	f.v(120, 160, 190)
	f.v(390, 160, 190)
	f.text(112, 55, "First name", 185, "Last name", 325, "Date of birth")
	f.text(142, 55, "Home address", 255, "Apartment")
	f.text(172, 55, "City", 125, "State or province", 395, "Code")
	got := Ruled(f.words, f.rules)
	if len(got) != 1 || got[0].Confidence >= 0.5 {
		t.Errorf("a form: %d tables, confidence %.2f", len(got), got[0].Confidence)
	}

	// A chart: a frame, grid lines and a label or two.
	c := &page{}
	c.grid([]float64{50, 150, 250, 350}, []float64{100, 150, 200, 250})
	c.text(120, 60, "plain-34")
	c.text(230, 260, "ResNet-34")
	got = Ruled(c.words, c.rules)
	if len(got) != 1 || got[0].Confidence >= 0.5 {
		t.Errorf("a chart: %d tables, confidence %.2f", len(got), got[0].Confidence)
	}

	// Rules with no text at all, and too few rules.
	if got := Ruled(nil, c.rules); len(got) != 0 {
		t.Errorf("an empty grid gave %d tables", len(got))
	}
	if got := Ruled(p.words, []Rule{{0, 10, 100, 10}}); got != nil {
		t.Errorf("one rule gave %v", got)
	}
}

func TestAligned(t *testing.T) {
	p := &page{}
	p.text(80, 50, "Staffing by team at the start and at the end of the quarter was as follows.")
	p.text(110, 50, "Team", 250, "Start", 350, "End")
	p.text(125, 50, "Engineering", 250, "120", 350, "134")
	p.text(140, 50, "Support", 250, "88", 350, "97")
	p.text(155, 50, "Sales and marketing", 250, "204", 350, "224")
	p.text(190, 50, "The figures count people, not posts, and leave out contractors.")
	tables, member := Aligned(p.words, p.lines, nil)
	if len(tables) != 1 {
		t.Fatalf("%d tables", len(tables))
	}
	want := [][]string{{"Team", "Start", "End"}, {"Engineering", "120", "134"}, {"Support", "88", "97"}, {"Sales and marketing", "204", "224"}}
	if !same(rowsOf(tables[0]), want) || tables[0].Method != "alignment" || tables[0].Confidence < 0.6 {
		t.Errorf("table = %v, confidence %.2f", rowsOf(tables[0]), tables[0].Confidence)
	}
	if got := []int{member[0], member[1], member[4], member[5]}; got[0] != -1 || got[1] != 0 || got[2] != 0 || got[3] != -1 {
		t.Errorf("membership = %v", member)
	}
	// Rules above and below add confidence.
	p.h(98, 50, 370)
	p.h(160, 50, 370)
	ruled, _ := Aligned(p.words, p.lines, p.rules)
	if len(ruled) != 1 || ruled[0].Confidence <= tables[0].Confidence {
		t.Errorf("with rules: confidence %.2f, without %.2f", ruled[0].Confidence, tables[0].Confidence)
	}
}

func TestAlignedRows(t *testing.T) {
	// The second line of a description, a label that takes two lines, and
	// currency signs in a column of their own.
	p := &page{}
	p.text(100, 50, "Item", 200, "Description", 400, "", 420, "Amount")
	p.text(115, 50, "MTR-075", 200, "Three-phase motor", 400, "$", 420, "636.00")
	p.text(126, 200, "with mounting kit")
	p.text(141, 50, "Exchange rate gains on the")
	p.text(152, 50, "senior notes", 200, "Unrealised", 400, "$", 420, "211.00")
	p.text(167, 50, "SVC-INST", 200, "Installation", 400, "$", 420, "960.00")
	tables, _ := Aligned(p.words, p.lines, nil)
	if len(tables) != 1 {
		t.Fatalf("%d tables", len(tables))
	}
	want := [][]string{
		{"Item", "Description", "Amount"},
		{"MTR-075", "Three-phase motor\nwith mounting kit", "$ 636.00"},
		{"Exchange rate gains on the\nsenior notes", "Unrealised", "$ 211.00"},
		{"SVC-INST", "Installation", "$ 960.00"},
	}
	if !same(rowsOf(tables[0]), want) {
		t.Errorf("rows = %q", rowsOf(tables[0]))
	}
}

func TestAlignedProseInColumns(t *testing.T) {
	// Two columns of prose that wraps. With rules between the rows they
	// are rows of a table; without, nothing says where a row ends.
	build := func(rules bool) *page {
		p := &page{}
		y := 100.0
		for r := 0; r < 3; r++ {
			if rules {
				p.h(y-12, 50, 420)
			}
			p.text(y, 50, "Govern", 150, "Policies, processes and procedures are in place")
			p.text(y+12, 50, "the risk", 150, "and are understood by everyone who has to act on")
			p.text(y+24, 50, "process", 150, "them, and they are reviewed every year in full.")
			y += 40
		}
		if rules {
			p.h(y-12, 50, 420)
		}
		return p
	}
	with := build(true)
	tables, _ := Aligned(with.words, with.lines, with.rules)
	if len(tables) != 1 || tables[0].Rows != 3 || tables[0].Confidence < 0.5 ||
		!strings.HasPrefix(tables[0].Cells[1].Text, "Policies, processes and procedures are in place\nand are understood") {
		t.Errorf("with rules: %+v", tables)
	}
	without := build(false)
	tables, _ = Aligned(without.words, without.lines, nil)
	if len(tables) != 1 || tables[0].Confidence >= 0.5 {
		t.Errorf("without rules: %d tables, confidence %.2f", len(tables), tables[0].Confidence)
	}
}

func TestAlignedCellOfManyLines(t *testing.T) {
	// The right-hand cell of each row runs on for several lines after the
	// left-hand one is done. Those lines have no gap in them, yet they are
	// part of the table: they keep within its second column. The prose
	// after the table runs across both columns and is not part of it.
	p := &page{}
	y := 100.0
	p.h(y-12, 50, 430)
	for r, name := range []string{"Govern", "Map", "Measure"} {
		p.text(y, 50, name+" the", 150, "Policies, processes and procedures are in place")
		p.text(y+12, 50, "risk", 150, "and are understood by everyone who has to act on")
		for i := 0; i < 5; i++ {
			p.text(y+24+12*float64(i), 150, "them, and they are reviewed in full every year so")
		}
		p.text(y+84, 150, "that they stay current.")
		y += 108
		p.h(y-12, 50, 430)
		_ = r
	}
	p.text(y+6, 50, "The functions are described at length in the chapters that follow this table.")
	p.text(y+18, 50, "Each of them is divided into categories, and those into subcategories again.")
	tables, member := Aligned(p.words, p.lines, p.rules)
	if len(tables) != 1 {
		t.Fatalf("%d tables", len(tables))
	}
	tb := tables[0]
	if tb.Rows != 3 || tb.Cols != 2 || tb.Confidence < 0.5 {
		t.Fatalf("table of %d x %d, confidence %.2f", tb.Rows, tb.Cols, tb.Confidence)
	}
	for r, name := range []string{"Govern", "Map", "Measure"} {
		left, right := tb.Cells[2*r].Text, tb.Cells[2*r+1].Text
		if left != name+" the\nrisk" || !strings.HasPrefix(right, "Policies, processes") || !strings.HasSuffix(right, "that they stay current.") || strings.Count(right, "\n") != 7 {
			t.Errorf("row %d: %q | %q", r, left, right)
		}
	}
	if n := len(member); member[n-1] != -1 || member[n-2] != -1 || member[n-3] != 0 {
		t.Errorf("the prose after the table: membership %v", member[n-3:])
	}
}

func TestAlignedThingsThatAreNotTables(t *testing.T) {
	low := func(name string, p *page) {
		t.Helper()
		tables, _ := Aligned(p.words, p.lines, p.rules)
		for _, tb := range tables {
			if tb.Confidence >= 0.5 {
				t.Errorf("%s: a table with confidence %.2f: %v", name, tb.Confidence, rowsOf(tb))
			}
		}
	}
	// A table of contents.
	toc := &page{}
	for i, title := range []string{"Introduction", "Framing risk", "Audience", "Core functions", "Profiles"} {
		toc.text(100+15*float64(i), 50, title, 400, []string{"1", "4", "9", "20", "33"}[i])
	}
	low("contents", toc)
	// A numbered list with its numbers set apart.
	list := &page{}
	for i, item := range []string{"Subregion emission rates", "Subregion resource mix", "State emission rates", "State resource mix"} {
		list.text(100+15*float64(i), 50, []string{"1.", "2.", "3.", "4."}[i], 90, item)
	}
	low("list", list)
	// Labels and their values.
	pairs := &page{}
	for i, pair := range [][2]string{{"Invoice no.", "INV-0417"}, {"Customer", "Acme Tooling"}, {"Terms", "Net thirty"}} {
		pairs.text(100+15*float64(i), 50, pair[0], 200, pair[1])
	}
	low("label and value", pairs)
	// The labels of a chart: a word here and there.
	chart := &page{}
	chart.text(100, 50, "20", 150, "20")
	chart.text(130, 300, "56-layer")
	chart.text(160, 50, "10", 150, "10", 300, "20-layer")
	low("chart", chart)

	// Ordinary prose has no columns at all.
	prose := &page{}
	for i := 0; i < 6; i++ {
		prose.text(100+12*float64(i), 50, "Ordinary running text that fills its line from one margin to the other without gaps.")
	}
	if tables, _ := Aligned(prose.words, prose.lines, nil); len(tables) != 0 {
		t.Errorf("prose gave %d tables", len(tables))
	}
	if tables, member := Aligned(nil, nil, nil); tables != nil || len(member) != 0 {
		t.Errorf("nothing gave %v", tables)
	}
}

// FuzzTables checks that no arrangement of words and rules makes the
// search for tables panic or run away.
func FuzzTables(f *testing.F) {
	seed := func(p *page) {
		var b []byte
		for _, w := range p.words {
			b = append(b, 0)
			for _, v := range []float64{w.X0, w.Y0, w.X1 - w.X0} {
				b = binary.LittleEndian.AppendUint16(b, uint16(v*8))
			}
		}
		for _, r := range p.rules {
			b = append(b, 1)
			for _, v := range []float64{r.X0, r.Y0, r.X1, r.Y1} {
				b = binary.LittleEndian.AppendUint16(b, uint16(v*8))
			}
		}
		f.Add(b)
	}
	p := &page{}
	p.grid([]float64{50, 150, 250, 350}, []float64{100, 120, 140, 160})
	p.text(114, 55, "Region", 155, "Q2", 255, "Q3")
	p.text(134, 55, "North America", 155, "1,204", 255, "1,318")
	p.text(154, 55, "Europe", 155, "982", 255, "1,045")
	seed(p)
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		var words []Word
		var rules []Rule
		num := func() float64 {
			if len(data) < 2 {
				data = nil
				return 0
			}
			v := float64(binary.LittleEndian.Uint16(data)) / 8
			data = data[2:]
			return v
		}
		for len(data) > 0 && len(words) < 400 && len(rules) < 400 {
			kind := data[0]
			data = data[1:]
			switch kind % 4 {
			case 0:
				x, y, w := num(), num(), num()
				words = append(words, Word{Text: []string{"12", "word", "$", "3.5%", "iv", "a longer cell"}[int(kind/4)%6], X0: x, X1: x + w/16, Y0: y - 8, Y1: y + 2, Size: 10})
			case 1:
				rules = append(rules, Rule{num(), num(), num(), num()})
			case 2:
				x, y, w := num(), num(), num()
				rules = append(rules, Rule{x, y, x + w, y})
			default:
				x, y, h := num(), num(), num()
				rules = append(rules, Rule{x, y, x, y + h})
			}
		}
		// Lines: words in the order given, a new line when y changes.
		var lines []Line
		for i, w := range words {
			if n := len(lines); n > 0 && lines[n-1].Y0 == w.Y0 {
				lines[n-1].Words = append(lines[n-1].Words, i)
				lines[n-1].X0, lines[n-1].X1 = math.Min(lines[n-1].X0, w.X0), math.Max(lines[n-1].X1, w.X1)
				continue
			}
			lines = append(lines, Line{Words: []int{i}, X0: w.X0, X1: w.X1, Y0: w.Y0, Y1: w.Y1})
		}
		check := func(tables []Table) {
			for _, tb := range tables {
				if tb.Rows < 1 || tb.Cols < 1 || tb.Confidence < 0 || tb.Confidence > 1 || math.IsNaN(tb.Confidence) {
					t.Fatalf("table %d x %d, confidence %v", tb.Rows, tb.Cols, tb.Confidence)
				}
				for _, c := range tb.Cells {
					if c.Row < 0 || c.Col < 0 || c.Row+c.RowSpan > tb.Rows || c.Col+c.ColSpan > tb.Cols || c.RowSpan < 1 || c.ColSpan < 1 {
						t.Fatalf("cell %+v outside a table of %d x %d", c, tb.Rows, tb.Cols)
					}
				}
			}
		}
		check(Ruled(words, rules))
		aligned, member := Aligned(words, lines, rules)
		check(aligned)
		if len(member) != len(lines) {
			t.Fatalf("%d memberships for %d lines", len(member), len(lines))
		}
	})
}
