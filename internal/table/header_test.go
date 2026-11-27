// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package table

import (
	"strings"
	"testing"
)

// flat returns the rows of a table with the lines of each cell joined.
func flat(t Table) [][]string {
	rows := rowsOf(t)
	for _, r := range rows {
		for c := range r {
			r[c] = strings.ReplaceAll(r[c], "\n", " ")
		}
	}
	return rows
}

// figures adds three rows of figures under a header, from y down.
func (p *page) figures(y float64) {
	p.text(y, 50, "October", 150, "1,815", 250, "522.10", 350, "900")
	p.text(y+14, 50, "November", 150, "2,204", 250, "531.75", 350, "850")
	p.text(y+28, 50, "December", 150, "1,960", 250, "540.20", 350, "800")
}

var figureRows = [][]string{{"October", "1,815", "522.10", "900"}, {"November", "2,204", "531.75", "850"}, {"December", "1,960", "540.20", "800"}}

func TestHeaderOfSeveralLines(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header func(p *page)
		want   [][]string // the header rows
	}{
		{"one line", func(p *page) {
			p.text(124, 50, "Period", 150, "Shares", 250, "Price", 350, "Left")
		}, [][]string{{"Period", "Shares", "Price", "Left"}}},
		// Headings of different lengths, set from the bottom up: the
		// short ones leave the lines above them empty.
		{"three ragged lines", func(p *page) {
			p.text(96, 150, "Total number", 350, "Number")
			p.text(110, 150, "of shares", 250, "Average price", 350, "that may")
			p.text(124, 50, "Period", 150, "purchased", 250, "paid per share", 350, "yet be bought")
		}, [][]string{{"Period", "Total number of shares purchased", "Average price paid per share", "Number that may yet be bought"}}},
		// Every heading of two lines: nothing but the rule under them
		// says that the second line is no row of data.
		{"two full lines with a rule under each column", func(p *page) {
			p.text(110, 50, "Fiscal", 150, "Shares", 250, "Average", 350, "Shares")
			p.text(124, 50, "period", 150, "purchased", 250, "price", 350, "left")
			for _, x := range []float64{50, 150, 250, 350} {
				p.h(128, x, x+45)
			}
		}, [][]string{{"Fiscal period", "Shares purchased", "Average price", "Shares left"}}},
		{"two full lines with one rule under them", func(p *page) {
			p.text(110, 50, "Fiscal", 150, "Shares", 250, "Average", 350, "Shares")
			p.text(124, 50, "period", 150, "purchased", 250, "price", 350, "left")
			p.h(128, 50, 400)
		}, [][]string{{"Fiscal period", "Shares purchased", "Average price", "Shares left"}}},
		// The same two lines with no rule may be a heading and a row of
		// words: only the first is known to be a heading.
		{"two full lines and no rule", func(p *page) {
			p.text(110, 50, "Fiscal", 150, "Shares", 250, "Average", 350, "Shares")
			p.text(124, 50, "period", 150, "purchased", 250, "price", 350, "left")
		}, [][]string{{"Fiscal", "Shares", "Average", "Shares"}}},
		// A heading over two columns, ruled off from theirs: two levels.
		// The columns beside the group have one heading, in the lower row.
		{"a group of columns under a rule", func(p *page) {
			p.text(110, 50, "Fiscal", 150, "Shares", 350, "Shares")
			p.h(114, 150, 300)
			p.text(124, 50, "period", 150, "Bought", 250, "Sold", 350, "left")
		}, [][]string{{"", "Shares", "", ""}, {"Fiscal period", "Bought", "Sold", "Shares left"}}},
		// Years stand in headings, over a rule.
		{"a line of years over a rule", func(p *page) {
			p.text(110, 150, "Carrying", 250, "Fair", 350, "Carrying")
			p.text(124, 150, "2023", 250, "2023", 350, "2022")
			p.h(128, 50, 400)
		}, [][]string{{"", "Carrying 2023", "Fair 2023", "Carrying 2022"}}},
		// And with no rule, where rows of amounts follow and the first
		// column is left empty: a row of data has its label.
		{"a line of years and no rule", func(p *page) {
			p.text(110, 150, "Carrying", 250, "Fair", 350, "Carrying")
			p.text(124, 150, "2023", 250, "2023", 350, "2022")
		}, [][]string{{"", "Carrying 2023", "Fair 2023", "Carrying 2022"}}},
		{"a line of years alone", func(p *page) {
			p.text(124, 150, "2023", 250, "2022", 350, "2021")
		}, [][]string{{"", "2023", "2022", "2021"}}},
	} {
		p := &page{}
		tc.header(p)
		p.figures(140)
		tables, _ := Aligned(p.words, p.lines, p.rules)
		if len(tables) != 1 {
			t.Errorf("%s: %d tables", tc.name, len(tables))
			continue
		}
		tb := tables[0]
		got := flat(tb)
		if tb.HeaderRows != len(tc.want) || len(got) < len(tc.want) || !same(got[:len(tc.want)], tc.want) {
			t.Errorf("%s: %d header rows, rows %q", tc.name, tb.HeaderRows, got)
			continue
		}
		// The rows of figures follow, untouched.
		if rest := got[len(tc.want):]; !same(rest[len(rest)-3:], figureRows) {
			t.Errorf("%s: rows of figures %q", tc.name, rest)
		}
	}
}

func TestHeaderRuledOffLineByLine(t *testing.T) {
	// Each group of columns has a heading of two lines with a rule under
	// each line, and each column a year with a rule under it. The rows of
	// figures have no rule between them. The two lines over a group are
	// one heading, as the rules under them cover the same columns, and
	// the years are the level under it.
	header := func() *page {
		p := &page{}
		p.text(96, 150, "Carrying Value", 350, "Fair Value")
		p.h(100, 150, 300)
		p.h(100, 350, 500)
		p.text(110, 150, "December 31,", 350, "December 31,")
		p.h(114, 150, 300)
		p.h(114, 350, 500)
		p.text(124, 150, "2023", 250, "2022", 350, "2023", 450, "2022")
		for _, x := range []float64{150, 250, 350, 450} {
			p.h(128, x, x+45)
		}
		return p
	}
	p := header()
	p.text(140, 50, "Bonds", 150, "13,230", 250, "12,937", 350, "12,035", 450, "13,249")
	p.text(154, 50, "Shares", 150, "15,410", 250, "11,484", 350, "14,552", 450, "12,242")
	p.text(168, 50, "Other", 150, "426", 250, "3,629", 350, "511", 450, "3,702")
	tables, _ := Aligned(p.words, p.lines, p.rules)
	if len(tables) != 1 {
		t.Fatalf("%d tables", len(tables))
	}
	tb := tables[0]
	want := [][]string{{"", "Carrying Value December 31,", "", "Fair Value December 31,", ""}, {"", "2023", "2022", "2023", "2022"}}
	if got := flat(tb); tb.HeaderRows != 2 || tb.Rows != 5 || !same(got[:2], want) {
		t.Errorf("%d header rows of %d: %q", tb.HeaderRows, tb.Rows, got)
	}

	// The same lines over rows that are ruled off as well: a table ruled
	// row by row, where the rules say nothing about the header, and the
	// first line, which stands over two of the five columns, is not known
	// to be all of it.
	q := header()
	for i, row := range [][5]string{{"Bonds", "13,230", "12,937", "12,035", "13,249"}, {"Shares", "15,410", "11,484", "14,552", "12,242"}, {"Other", "426", "3,629", "511", "3,702"}} {
		y := 140 + 14*float64(i)
		q.text(y, 50, row[0], 150, row[1], 250, row[2], 350, row[3], 450, row[4])
		q.h(y+4, 150, 500)
	}
	tables, _ = Aligned(q.words, q.lines, q.rules)
	if len(tables) != 1 || tables[0].HeaderRows != 0 || tables[0].Rows != 6 {
		t.Errorf("ruled row by row: %+v", tables)
	}
}

func TestYearsThatAreData(t *testing.T) {
	// Lines of years one under the other are rows of a table of years.
	p := &page{}
	p.text(100, 50, "Plant", 150, "Opened", 250, "Closed")
	p.text(114, 150, "1961", 250, "1998")
	p.text(128, 150, "1974", 250, "2003")
	p.text(142, 150, "1988", 250, "2011")
	tables, _ := Aligned(p.words, p.lines, nil)
	if len(tables) != 1 || tables[0].HeaderRows != 1 || tables[0].Rows != 4 {
		t.Errorf("%+v", tables)
	}
}

func TestHeaderWithAPhraseOverAGroup(t *testing.T) {
	// The heading of a group of columns written across them, with no rule
	// under it: a phrase, where a row would have its cells set apart. It
	// is a level of its own, and the heading beside it, which runs down
	// both levels, stands in the lower one.
	p := &page{}
	p.text(110, 50, "Fiscal", 165, "Bought and sold in the quarter", 350, "Shares")
	p.text(124, 50, "period", 150, "Bought", 250, "Sold", 350, "left")
	for i := 0; i < 9; i++ {
		p.text(140+14*float64(i), 50, "Month", 150, "1,815", 250, "522.10", 350, "900")
	}
	tables, _ := Aligned(p.words, p.lines, nil)
	if len(tables) != 1 {
		t.Fatalf("%d tables", len(tables))
	}
	tb := tables[0]
	want := [][]string{{"", "Bought and sold in the quarter", "", ""}, {"Fiscal period", "Bought", "Sold", "Shares left"}}
	if got := flat(tb); tb.HeaderRows != 2 || tb.Rows != 11 || !same(got[:2], want) {
		t.Errorf("%d header rows of %d: %q", tb.HeaderRows, tb.Rows, got[:3])
	}
}

func TestHeaderThatIsNotThere(t *testing.T) {
	// A rule above the totals is not the rule under the headings: what
	// stands above it are rows of figures.
	p := &page{}
	p.text(100, 50, "Bonds", 150, "$", 162, "10,308", 250, "14", 350, "10,269")
	p.text(114, 50, "Shares", 150, "11,788", 250, "58", 350, "11,805")
	p.text(128, 50, "Other", 150, "1,212", 250, "241", 350, "1,449")
	p.h(132, 50, 400)
	p.text(142, 50, "Total", 150, "$", 162, "23,308", 250, "313", 350, "23,523")
	tables, _ := Aligned(p.words, p.lines, p.rules)
	if len(tables) != 1 || tables[0].HeaderRows != 0 || tables[0].Rows != 4 {
		t.Errorf("rows of figures over a rule: %+v", tables)
	}

	// A line that names the rows under it, above the first of them.
	p = &page{}
	p.text(96, 50, "Period", 150, "Shares", 250, "Price", 350, "Left")
	p.text(110, 50, "Fourth quarter")
	p.figures(124)
	tables, _ = Aligned(p.words, p.lines, nil)
	if len(tables) != 1 || tables[0].HeaderRows != 1 || tables[0].Rows != 5 || flat(tables[0])[1][0] != "Fourth quarter" {
		t.Errorf("a line naming a group of rows: %+v", tables)
	}

	// A table of words with nothing drawn: no telling where headings end.
	p = &page{}
	p.text(100, 50, "Name", 150, "Office", 250, "Town")
	p.text(114, 50, "Okafor", 150, "Chair", 250, "Leeds")
	p.text(128, 50, "Lindqvist", 150, "Treasurer", 250, "York")
	p.text(142, 50, "Marchetti", 150, "Secretary", 250, "Hull")
	tables, _ = Aligned(p.words, p.lines, nil)
	if len(tables) != 1 || tables[0].HeaderRows != 0 || tables[0].Rows != 4 {
		t.Errorf("a table of words: %+v", tables)
	}
	// With a rule under its first line, that line is the heading.
	p.h(104, 50, 300)
	tables, _ = Aligned(p.words, p.lines, p.rules)
	if len(tables) != 1 || tables[0].HeaderRows != 1 || tables[0].Rows != 4 {
		t.Errorf("a table of words with a rule: %+v", tables)
	}
	// Ruled off row by row, the first rule says nothing.
	p.h(118, 50, 300)
	p.h(132, 50, 300)
	tables, _ = Aligned(p.words, p.lines, p.rules)
	if len(tables) != 1 || tables[0].HeaderRows != 0 {
		t.Errorf("a table of words ruled row by row: %+v", tables)
	}
}

func TestHeaderJoinKeepsConfidence(t *testing.T) {
	// The same table with its headings on one line and on three: joining
	// the three must not make the table less of a table.
	one, three := &page{}, &page{}
	one.text(124, 50, "Period", 150, "Shares", 250, "Price", 350, "Left")
	one.figures(140)
	three.text(96, 150, "Total number", 350, "Number")
	three.text(110, 150, "of shares", 250, "Average price", 350, "that may")
	three.text(124, 50, "Period", 150, "purchased", 250, "paid per share", 350, "yet be bought")
	three.figures(140)
	a, _ := Aligned(one.words, one.lines, nil)
	b, _ := Aligned(three.words, three.lines, nil)
	if len(a) != 1 || len(b) != 1 || b[0].Confidence < 0.5 || b[0].Rows != a[0].Rows {
		t.Fatalf("tables: %+v, %+v", a, b)
	}
}

func TestRuledHeaderBand(t *testing.T) {
	// A header ruled off, with headings of three lines, over rows of
	// figures that no rule separates. The headings keep step as rows of
	// figures do, and are one row all the same.
	p := &page{}
	p.h(100, 40, 350)
	p.h(150, 40, 350)
	p.h(250, 40, 350)
	for _, x := range []float64{150, 250, 350} {
		p.v(x, 100, 250)
	}
	p.text(114, 45, "State", 155, "Cases", 255, "Cases")
	p.text(126, 45, "or", 155, "received", 255, "closed")
	p.text(138, 45, "territory", 155, "in year", 255, "in year")
	rows := [][3]string{{"Alabama", "2,120", "2,020"}, {"Alaska", "26", "31"}, {"Arizona", "629", "562"}, {"Arkansas", "6,990", "7,053"}, {"Colorado", "5,651", "5,933"}}
	for i, row := range rows {
		p.text(164+18*float64(i), 45, row[0], 155, row[1], 255, row[2])
	}
	tables := Ruled(p.words, p.rules)
	if len(tables) != 1 {
		t.Fatalf("%d tables", len(tables))
	}
	tb := tables[0]
	got := flat(tb)
	if tb.Rows != 6 || tb.HeaderRows != 1 || strings.Join(got[0], "|") != "State or territory|Cases received in year|Cases closed in year" ||
		strings.Join(got[5], "|") != "Colorado|5,651|5,933" {
		t.Errorf("%d rows, %d header rows: %q", tb.Rows, tb.HeaderRows, got)
	}

	// Two levels of headings, each ruled off, are two header rows.
	q := &page{}
	q.h(100, 40, 350)
	q.h(120, 150, 350)
	q.h(140, 40, 350)
	q.h(240, 40, 350)
	for _, x := range []float64{150, 250, 350} {
		q.v(x, 120, 240)
	}
	q.v(150, 100, 120)
	q.v(350, 100, 120)
	q.text(114, 155, "Cases in the year")
	q.text(134, 45, "State", 155, "Received", 255, "Closed")
	for i, row := range rows {
		q.text(154+18*float64(i), 45, row[0], 155, row[1], 255, row[2])
	}
	tables = Ruled(q.words, q.rules)
	if len(tables) != 1 || tables[0].Rows != 7 || tables[0].HeaderRows != 2 {
		t.Fatalf("two levels: %+v", tables)
	}

}

// ruledRows rules off every row of a grid of three columns and sets a row
// of text in each: the first column's cell, then the other two.
func ruledRows(rows ...[3]string) *page {
	p := &page{}
	ys := []float64{100}
	for i, row := range rows {
		y := 100 + 20*float64(i)
		ys = append(ys, y+20)
		p.text(y+14, 55, row[0], 155, row[1], 255, row[2])
	}
	p.grid([]float64{50, 150, 250, 350}, ys)
	return p
}

func TestRuledHeaderWhereEveryRowIsRuled(t *testing.T) {
	// Nothing drawn sets the header apart. The text does: words over rows
	// of figures.
	for _, tc := range []struct {
		name string
		rows [][3]string
		want int
	}{
		{"words over figures", [][3]string{{"Region", "Second", "Third"},
			{"North", "1,204", "1,318"}, {"South", "982", "1,045"}, {"West", "611", "702"}}, 1},
		// A row that numbers the columns is the last of the header, and
		// headings that hold amounts are headings all the same.
		{"numbered columns", [][3]string{{"Item", "Under $15,000", "$15,000 or more"}, {"", "(1)", "(2)"},
			{"Returns", "760,088", "495,215"}, {"Amount", "65,622", "786,764"}}, 2},
		// Labels of periods are not figures, and years are headings.
		{"years and quarters", [][3]string{{"Series", "2024", ""}, {"", "Q3", "Q4"},
			{"Goods", "5.6", "6.2"}, {"Services", "2.8", "3.0"}}, 2},
		// A row that names the rows under it is no heading.
		{"a group of rows", [][3]string{{"Item", "Returns", "Amount"}, {"Income:", "", ""},
			{"Wages", "1,204", "1,318"}, {"Interest", "982", "1,045"}}, 1},
		// A table set on its side: the first row of data holds words.
		// The first column has one heading, and here not even that.
		{"rows of words first", [][3]string{{"", "Alpha", "Beta"}, {"Kind", "wood", "metal"},
			{"Count", "12", "15"}, {"Cost", "7", "9"}}, 1},
		{"two rows of words, each with a label", [][3]string{{"Name", "Alpha", "Beta"}, {"Kind", "wood", "metal"},
			{"Count", "12", "15"}, {"Cost", "7", "9"}}, 1},
		{"no figures", [][3]string{{"Name", "Kind", "Place"}, {"Alpha", "wood", "north"},
			{"Beta", "metal", "south"}}, 0},
		{"figures from the top", [][3]string{{"1", "1,204", "1,318"}, {"2", "982", "1,045"},
			{"3", "611", "702"}}, 0},
	} {
		p := ruledRows(tc.rows...)
		tables := Ruled(p.words, p.rules)
		if len(tables) != 1 || tables[0].HeaderRows != tc.want {
			t.Errorf("%s: %d header rows, want %d (%d tables)", tc.name, tables[0].HeaderRows, tc.want, len(tables))
		}
	}
}

func TestRuledHeaderOfGroupsWhereEveryRowIsRuled(t *testing.T) {
	// The first row's heading is merged across two columns, and the row
	// under it has the first column's heading: two levels.
	p := &page{}
	for _, y := range []float64{100, 120, 140, 160, 180, 200} {
		p.h(y, 50, 350)
	}
	for _, x := range []float64{50, 150, 350} {
		p.v(x, 100, 200)
	}
	p.v(250, 120, 200)
	p.text(114, 155, "Cases in the year")
	p.text(134, 55, "State", 155, "Received", 255, "Closed")
	p.text(154, 55, "Alabama", 155, "2,120", 255, "2,020")
	p.text(174, 55, "Alaska", 155, "26", 255, "31")
	p.text(194, 55, "Arizona", 155, "629", 255, "562")
	tables := Ruled(p.words, p.rules)
	if len(tables) != 1 || tables[0].Rows != 5 || tables[0].HeaderRows != 2 {
		t.Fatalf("%+v", tables)
	}
}

func TestRuledHeaderInBold(t *testing.T) {
	// Three levels of headings in bold, and a row of totals in bold under
	// them: the header ends where the figures start.
	p := ruledRows([3]string{"Series", "Share", "Change"}, [3]string{"", "2024", "2024"}, [3]string{"", "Q3", "Q4"},
		[3]string{"Total", "100.0", "2.4"})
	for i := range p.words {
		p.words[i].Bold = true
	}
	rest := [][3]string{{"Goods", "21.4", "6.2"}, {"Durable", "7.5", "12.4"}, {"Other", "13.9", "3.1"}, {"Services", "46.8", "3.0"}}
	for i, row := range rest {
		p.text(194+20*float64(i), 55, row[0], 155, row[1], 255, row[2])
	}
	p.rules = nil
	p.grid([]float64{50, 150, 250, 350}, []float64{100, 120, 140, 160, 180, 200, 220, 240, 260})
	tables := Ruled(p.words, p.rules)
	if len(tables) != 1 || tables[0].Rows != 8 || tables[0].HeaderRows != 3 {
		t.Fatalf("%d tables, %d rows, %d header rows", len(tables), tables[0].Rows, tables[0].HeaderRows)
	}
}

func TestRuledHeaderWithALineOfUnits(t *testing.T) {
	// Two levels of headings, each ruled off, and under them a line of
	// units with no rule of its own: it stands in the band of the figures
	// and is the third header row.
	p := &page{}
	p.h(100, 40, 350)
	p.h(120, 150, 350)
	p.h(140, 40, 350)
	p.h(260, 40, 350)
	for _, x := range []float64{150, 250, 350} {
		p.v(x, 120, 260)
	}
	p.v(150, 100, 120)
	p.v(350, 100, 120)
	p.text(114, 155, "Area planted")
	p.text(134, 45, "State", 155, "2023", 255, "2024")
	p.text(154, 155, "(1,000 acres)", 255, "(1,000 acres)")
	rows := [][3]string{{"Alabama", "2,120", "2,020"}, {"Alaska", "26", "31"}, {"Arizona", "629", "562"}, {"Arkansas", "6,990", "7,053"}, {"Colorado", "5,651", "5,933"}}
	for i, row := range rows {
		p.text(172+18*float64(i), 45, row[0], 155, row[1], 255, row[2])
	}
	tables := Ruled(p.words, p.rules)
	if len(tables) != 1 {
		t.Fatalf("%d tables", len(tables))
	}
	tb := tables[0]
	if got := flat(tb); tb.Rows != 8 || tb.HeaderRows != 3 || strings.Join(got[2], "|") != "|(1,000 acres)|(1,000 acres)" {
		t.Errorf("%d rows, %d header rows: %q", tb.Rows, tb.HeaderRows, got)
	}
	// A first row of the band that has its label is a row of figures.
	q := &page{}
	q.rules = p.rules
	q.text(114, 155, "Area planted")
	q.text(134, 45, "State", 155, "2023", 255, "2024")
	q.text(154, 45, "Total", 155, "(D)", 255, "(D)")
	for i, row := range rows {
		q.text(172+18*float64(i), 45, row[0], 155, row[1], 255, row[2])
	}
	if tables = Ruled(q.words, q.rules); len(tables) != 1 || tables[0].HeaderRows != 2 {
		t.Errorf("a row with a label: %+v", tables)
	}
}

func TestTextHeader(t *testing.T) {
	cells := func(rows ...[]string) *Table {
		tb := &Table{Rows: len(rows), Cols: len(rows[0])}
		for r, row := range rows {
			for c, text := range row {
				tb.Cells = append(tb.Cells, Cell{Row: r, Col: c, RowSpan: 1, ColSpan: 1, Text: text})
			}
		}
		return tb
	}
	for _, tc := range []struct {
		name string
		tb   *Table
		want int
	}{
		{"numbered", cells([]string{"Type of case", "Cases received", "Cases closed"}, []string{"", "(1)", "(2)"},
			[]string{"Total", "60,933", "52,997"}, []string{"Other", "4,772", "4,741"}), 2},
		// Figures in brackets that do not count up are figures.
		{"numbers that do not count up", cells([]string{"Type", "First", "Second"}, []string{"", "(4)", "(2)"},
			[]string{"Total", "60,933", "52,997"}, []string{"Other", "4,772", "4,741"}), 1},
		// The heading of a group, with no word on the columns it covers,
		// leaves the column beside it empty.
		{"a group and its columns", cells([]string{"", "Multiply by:", ""}, []string{"", "Feet", "Yards"},
			[]string{"1 inch =", "0.083 333", "0.027 777"}, []string{"1 foot =", "1", "0.333 333"}), 2},
		{"one row", cells([]string{"Total", "60,933", "52,997"}), 0},
	} {
		if got := TextHeader(tc.tb); got != tc.want {
			t.Errorf("%s: %d header rows, want %d", tc.name, got, tc.want)
		}
	}
}

// cellAt returns the cell that starts at a row and column.
func cellAt(t Table, row, col int) Cell {
	for _, c := range t.Cells {
		if c.Row == row && c.Col == col {
			return c
		}
	}
	return Cell{}
}

func TestGroupHeadingIsMergedAcrossItsColumns(t *testing.T) {
	body := func(p *page, y float64) {
		p.text(y, 50, "October", 150, "1,815", 250, "522", 350, "900", 450, "410")
		p.text(y+14, 50, "November", 150, "2,204", 250, "531", 350, "850", 450, "395")
		p.text(y+28, 50, "December", 150, "1,960", 250, "540", 350, "800", 450, "380")
	}
	// The heading of three columns is centred over them, so its words
	// stand in the middle one. The rule under it shows how far it reaches:
	// it is one cell across the three, in the first.
	p := &page{}
	p.text(110, 50, "Fiscal", 230, "Shares in the year", 450, "Shares")
	p.h(114, 150, 395)
	p.text(124, 50, "period", 150, "Bought", 250, "Sold", 350, "Held", 450, "left")
	body(p, 140)
	tables, _ := Aligned(p.words, p.lines, p.rules)
	if len(tables) != 1 {
		t.Fatalf("%d tables", len(tables))
	}
	tb := tables[0]
	want := [][]string{{"", "Shares in the year", "", "", ""}, {"Fiscal period", "Bought", "Sold", "Held", "Shares left"}}
	if got := flat(tb); tb.HeaderRows != 2 || !same(got[:2], want) || cellAt(tb, 0, 1).ColSpan != 3 || cellAt(tb, 0, 2).Text != "" {
		t.Errorf("under a rule: %d header rows, span %d: %q", tb.HeaderRows, cellAt(tb, 0, 1).ColSpan, got[:2])
	}
	// A merged cell is there once, and the cells of the rows are whole.
	if n := len(tb.Cells); n != 5*5-2 {
		t.Errorf("%d cells", n)
	}

	// One heading over two columns of years, and nothing else over them:
	// the group's heading, though it fits over the first year and no rule
	// sets it apart from the years.
	q := &page{}
	q.text(96, 150, "Losses to December 31,", 400, "Open")
	q.text(110, 50, "Accident", 400, "claims")
	q.text(124, 50, "year", 200, "2022", 300, "2023", 400, "(number)")
	q.h(128, 50, 450)
	q.text(140, 50, "2021", 190, "14,138", 290, "13,699", 400, "9,161")
	q.text(154, 50, "2022", 190, "12,273", 290, "12,005", 400, "7,295")
	q.text(168, 50, "2023", 190, "11,567", 290, "11,340", 400, "6,810")
	tables, _ = Aligned(q.words, q.lines, q.rules)
	if len(tables) != 1 {
		t.Fatalf("%d tables", len(tables))
	}
	tb = tables[0]
	want = [][]string{{"", "Losses to December 31,", "", ""}, {"Accident year", "2022", "2023", "Open claims (number)"}}
	if got := flat(tb); tb.HeaderRows != 2 || !same(got[:2], want) || cellAt(tb, 0, 1).ColSpan != 2 {
		t.Errorf("over years: %d header rows, span %d: %q", tb.HeaderRows, cellAt(tb, 0, 1).ColSpan, got[:2])
	}

	// Each year with a heading of its own: no group.
	r := &page{}
	r.text(110, 150, "September 30,", 290, "December 31,", 400, "Open")
	r.text(124, 50, "Year", 150, "2023", 290, "2022", 400, "claims")
	r.h(128, 50, 450)
	r.text(140, 50, "2021", 150, "14,138", 290, "13,699", 400, "9,161")
	r.text(154, 50, "2022", 150, "12,273", 290, "12,005", 400, "7,295")
	r.text(168, 50, "2023", 150, "11,567", 290, "11,340", 400, "6,810")
	tables, _ = Aligned(r.words, r.lines, r.rules)
	if len(tables) != 1 {
		t.Fatalf("%d tables", len(tables))
	}
	tb = tables[0]
	want = [][]string{{"Year", "September 30, 2023", "December 31, 2022", "Open claims"}}
	if got := flat(tb); tb.HeaderRows != 1 || !same(got[:1], want) || cellAt(tb, 0, 1).ColSpan != 1 {
		t.Errorf("a heading for each year: %d header rows: %q", tb.HeaderRows, got[:2])
	}
}

func TestHeaderLinesAboveTheTable(t *testing.T) {
	// A table starts at the first line that shows its columns. The lines
	// of its header above that one hold one thing each.
	body := func(p *page, y float64) {
		p.text(y, 50, "Revenue", 250, "97,147", 350, "21,998", 450, "90,949")
		p.text(y+14, 50, "Costs", 250, "61,020", 350, "18,410", 450, "70,244")
		p.text(y+28, 50, "Taxes", 250, "8,300", 350, "1,150", 450, "5,906")
	}
	// The heading of a group of columns, ruled off over them.
	p := &page{}
	p.text(96, 300, "Year ended December 31,")
	p.h(100, 250, 480)
	p.text(110, 250, "2023", 350, "2022", 450, "2021")
	body(p, 124)
	tables, member := Aligned(p.words, p.lines, p.rules)
	if len(tables) != 1 {
		t.Fatalf("%d tables", len(tables))
	}
	tb := tables[0]
	want := [][]string{{"", "Year ended December 31,", "", ""}, {"", "2023", "2022", "2021"}}
	if got := flat(tb); tb.Rows != 5 || tb.HeaderRows != 2 || !same(got[:2], want) || cellAt(tb, 0, 1).ColSpan != 3 || member[0] != 0 || tb.Y0 != 88 {
		t.Errorf("a group ruled off: %d rows, %d header rows, span %d, member %v, top %v: %q", tb.Rows, tb.HeaderRows, cellAt(tb, 0, 1).ColSpan, member, tb.Y0, got[:2])
	}

	// The top line of one column's heading, a line longer than the rest.
	q := &page{}
	q.text(96, 250, "Common")
	q.text(108, 250, "stock", 350, "Retained")
	q.text(120, 50, "Period", 250, "issued", 350, "earnings", 450, "Total")
	body(q, 134)
	tables, member = Aligned(q.words, q.lines, nil)
	if len(tables) != 1 {
		t.Fatalf("%d tables", len(tables))
	}
	tb = tables[0]
	want = [][]string{{"Period", "Common stock issued", "Retained earnings", "Total"}}
	if got := flat(tb); tb.Rows != 4 || tb.HeaderRows != 1 || !same(got[:1], want) || member[0] != 0 || tb.Y0 != 88 {
		t.Errorf("the top line of a heading: %d rows, %d header rows, member %v: %q", tb.Rows, tb.HeaderRows, member, got[:1])
	}

	// What stands above a table and is no part of its header.
	for _, tc := range []struct {
		name  string
		above func(p *page)
	}{
		{"a title at the left", func(p *page) { p.text(96, 50, "Results of the year in brief") }},
		{"a title ruled off across the table", func(p *page) {
			p.text(96, 200, "Results of the year")
			p.h(100, 50, 480)
		}},
		{"a note in brackets", func(p *page) { p.text(98, 440, "(in millions)") }},
		{"a heading with no rule", func(p *page) { p.text(96, 300, "Year ended December 31,") }},
		{"a line far above", func(p *page) {
			p.text(70, 300, "Year ended December 31,")
			p.h(74, 250, 480)
		}},
	} {
		p := &page{}
		tc.above(p)
		p.text(110, 250, "2023", 350, "2022", 450, "2021")
		body(p, 124)
		tables, member := Aligned(p.words, p.lines, p.rules)
		if len(tables) != 1 || tables[0].Rows != 4 || tables[0].HeaderRows != 1 || member[0] != -1 || tables[0].Y0 != 102 {
			t.Errorf("%s: %d tables, member %v, %+v", tc.name, len(tables), member, flat(tables[0])[:2])
		}
	}
}

func TestLineThatNamesASectionOfTheRows(t *testing.T) {
	// A line merged across the columns of figures stands under the
	// headings, ruled off, and another like it further down, over the
	// second half of the rows. Both name the rows under them: neither is
	// a header row. With the second one gone the first is the line of
	// units of the whole table, and the last row of its header.
	build := func(second bool) *page {
		p := &page{}
		ys := []float64{100, 120, 140}
		p.text(114, 55, "Year", 155, "Goods", 255, "Services")
		p.text(134, 205, "Billions of dollars")
		y := 140.0
		row := func(cells ...any) {
			p.text(y+14, cells...)
			y += 20
			ys = append(ys, y)
		}
		row(55, "2021", 155, "3,363", 255, "6,686")
		row(55, "2022", 155, "3,180", 255, "6,711")
		if second {
			row(215, "Percent change")
		}
		row(55, "2021", 155, "4.1", 255, "2.2")
		row(55, "2022", 155, "5.4", 255, "0.4")
		for _, yy := range ys {
			p.h(yy, 50, 350)
		}
		// The columns of figures are parted but for the lines across them.
		for i := 0; i+1 < len(ys); i++ {
			p.v(50, ys[i], ys[i+1])
			p.v(150, ys[i], ys[i+1])
			p.v(350, ys[i], ys[i+1])
			if i != 1 && !(second && i == 4) {
				p.v(250, ys[i], ys[i+1])
			}
		}
		return p
	}
	for _, tc := range []struct {
		second     bool
		rows, want int
	}{{true, 7, 1}, {false, 6, 2}} {
		p := build(tc.second)
		tables := Ruled(p.words, p.rules)
		if len(tables) != 1 {
			t.Fatalf("%d tables", len(tables))
		}
		tb := tables[0]
		if c := cellAt(tb, 1, 1); tb.Rows != tc.rows || tb.HeaderRows != tc.want || c.Text != "Billions of dollars" || c.ColSpan != 2 {
			t.Errorf("a second line %v: %d rows, %d header rows, %+v", tc.second, tb.Rows, tb.HeaderRows, c)
		}
	}
}
