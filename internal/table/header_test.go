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

	// Where every row is ruled off, nothing says which are headings.
	r := &page{}
	r.grid([]float64{50, 150, 250, 350}, []float64{100, 120, 140, 160, 180})
	r.text(114, 55, "Region", 155, "Second", 255, "Third")
	r.text(134, 55, "North", 155, "1,204", 255, "1,318")
	r.text(154, 55, "South", 155, "982", 255, "1,045")
	r.text(174, 55, "West", 155, "611", 255, "702")
	tables = Ruled(r.words, r.rules)
	if len(tables) != 1 || tables[0].HeaderRows != 0 {
		t.Errorf("a full grid: %+v", tables)
	}
}
