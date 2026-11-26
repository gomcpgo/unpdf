// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package unpdf

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/gomcpgo/unpdf/internal/pdftest"
)

// cells writes one row of a table: text at each of the given x positions.
func cells(y int, at ...any) string {
	var b strings.Builder
	for i := 0; i+1 < len(at); i += 2 {
		fmt.Fprintf(&b, "BT /F1 10 Tf %d %d Td (%s) Tj ET ", at[i].(int), y, at[i+1].(string))
	}
	return b.String()
}

const wrapped = "BT /F1 10 Tf 72 %d Td (This paragraph is long enough to be wrapped over several lines by the) Tj " +
	"0 -12 Td (program that made the file, which is what makes it read as prose and) Tj " +
	"0 -12 Td (not as the row of a table or the lines of an address.) Tj ET "

func pagesOf(t *testing.T, contents ...string) *Document {
	t.Helper()
	b := pdftest.New()
	res := fmt.Sprintf("<< /Font << /F1 %d 0 R /F2 %d 0 R >> >>", b.Add(helvetica), b.Add(helveticaBold))
	var specs []pdftest.PageSpec
	for _, c := range contents {
		specs = append(specs, pdftest.PageSpec{Content: c, Resources: res})
	}
	b.Pages(specs...)
	return openBytes(t, b.Bytes(), Options{})
}

func TestRuledTable(t *testing.T) {
	// A grid of three columns and three rows, with a paragraph above and
	// one below. The top-left cell's text takes two lines.
	grid := "0.5 w 72 640 m 372 640 l S 72 610 m 372 610 l S 72 590 m 372 590 l S 72 570 m 372 570 l S " +
		"72 570 m 72 640 l S 172 570 m 172 640 l S 272 570 m 272 640 l S 372 570 m 372 640 l S "
	doc := pagesOf(t, fmt.Sprintf(wrapped, 720)+grid+
		cells(628, 76, "Sales", 176, "Second quarter", 276, "Third | quarter")+cells(616, 76, "region")+
		cells(596, 76, "Europe", 176, "982,300", 276, "1,045,750")+
		cells(576, 76, "Asia", 176, "611,020", 276, "702,440")+
		fmt.Sprintf(wrapped, 520))
	p, _ := doc.Page(ctx, 1)
	tables, err := p.Tables(ctx)
	if err != nil || len(tables) != 1 {
		t.Fatalf("tables = %+v, %v", tables, err)
	}
	tb := tables[0]
	if tb.Method != TableFromRules || tb.Rows != 3 || tb.Cols != 3 || tb.Confidence < 0.9 {
		t.Errorf("table: %s, %d x %d, confidence %.2f", tb.Method, tb.Rows, tb.Cols, tb.Confidence)
	}
	want := [][]string{{"Sales\nregion", "Second quarter", "Third | quarter"}, {"Europe", "982,300", "1,045,750"}, {"Asia", "611,020", "702,440"}}
	if got := tb.Grid(); !reflect.DeepEqual(got, want) {
		t.Errorf("grid = %q", got)
	}
	// The table's box is the grid, in display space: measured from the top.
	if want := (Box{72, 792 - 640, 372, 792 - 570}); tb.Box != want {
		t.Errorf("box = %+v, want %+v", tb.Box, want)
	}
	if c := tb.Cells[4]; c.Row != 1 || c.Col != 1 || c.Text != "982,300" || c.RowSpan != 1 || c.ColSpan != 1 || c.Box.X0 != 172 || c.Box.X1 != 272 {
		t.Errorf("cell = %+v", c)
	}
	wantMD := "| Sales region | Second quarter | Third \\| quarter |\n| --- | --- | --- |\n| Europe | 982,300 | 1,045,750 |\n| Asia | 611,020 | 702,440 |"
	if got := tb.Markdown(); got != wantMD {
		t.Errorf("Markdown:\n%s", got)
	}
	// In the page's Markdown the table stands between the two paragraphs,
	// and its text is not repeated as lines.
	md, _ := doc.Markdown(ctx, MarkdownOptions{OmitPageMarkers: true})
	para := "This paragraph is long enough to be wrapped over several lines by the program that made the file, " +
		"which is what makes it read as prose and not as the row of a table or the lines of an address."
	if want := para + "\n\n" + wantMD + "\n\n" + para + "\n"; md != want {
		t.Errorf("page Markdown:\n%s", md)
	}
	// Blocks and Text still hold the table's text.
	if text, _ := p.Text(ctx); !strings.Contains(text, "Europe 982,300 1,045,750") {
		t.Errorf("text = %q", text)
	}
	// Asking again gives the same tables.
	if again, _ := p.Tables(ctx); !reflect.DeepEqual(again, tables) {
		t.Error("a second call gave different tables")
	}
}

func TestRuledHeadingsThatRepeatFromPageToPage(t *testing.T) {
	// Tables of one layout on pages that follow each other, high on the
	// page: their headings repeat in the same place, as a running head
	// does. Inside the rules they are the table's all the same. A box
	// drawn round the running head itself is no table.
	head := "0.5 w 72 770 m 540 770 l S 72 750 m 540 750 l S 72 750 m 72 770 l S 400 750 m 400 770 l S 540 750 m 540 770 l S " +
		cells(756, 76, "Annual Data Book", 404, "Download")
	grid := "0.5 w 72 735 m 372 735 l S 72 715 m 372 715 l S 72 695 m 372 695 l S 72 675 m 372 675 l S " +
		"72 675 m 72 735 l S 172 675 m 172 735 l S 272 675 m 272 735 l S 372 675 m 372 735 l S "
	var pages []string
	for n := 1; n <= 5; n++ {
		pages = append(pages, head+grid+cells(721, 76, "Year", 176, "Exports", 276, "Imports")+
			cells(701, 76, fmt.Sprint(2000+2*n), 176, fmt.Sprint(100+n), 276, fmt.Sprint(200+n))+
			cells(681, 76, fmt.Sprint(2001+2*n), 176, fmt.Sprint(300+n), 276, fmt.Sprint(400+n))+
			fmt.Sprintf(wrapped, 600))
	}
	doc := pagesOf(t, pages...)
	p, _ := doc.Page(ctx, 3)
	blocks, _ := p.Blocks(ctx)
	furniture := 0
	for _, b := range blocks {
		if b.Furniture {
			furniture++
		}
	}
	if furniture < 2 {
		t.Fatalf("%d blocks of furniture: the test needs the head and the headings to repeat", furniture)
	}
	tables, err := p.Tables(ctx)
	if err != nil || len(tables) != 1 {
		t.Fatalf("tables = %+v, %v", tables, err)
	}
	want := [][]string{{"Year", "Exports", "Imports"}, {"2006", "103", "203"}, {"2007", "303", "403"}}
	if got := tables[0].Grid(); !reflect.DeepEqual(got, want) {
		t.Errorf("grid = %q", got)
	}
}

func TestTableOnRotatedPage(t *testing.T) {
	b := pdftest.New()
	res := fmt.Sprintf("<< /Font << /F1 %d 0 R >> >>", b.Add(helvetica))
	// A page turned a quarter: the text and the rules are drawn sideways
	// in page space, and upright as displayed.
	content := "0 1 -1 0 612 0 cm 0.5 w " +
		"72 540 m 372 540 l S 72 520 m 372 520 l S 72 500 m 372 500 l S 72 480 m 372 480 l S " +
		"72 480 m 72 540 l S 172 480 m 172 540 l S 272 480 m 272 540 l S 372 480 m 372 540 l S " +
		cells(526, 76, "Team", 176, "Start", 276, "End") + cells(506, 76, "Support", 176, "88", 276, "97") + cells(486, 76, "Sales", 176, "204", 276, "224")
	b.Pages(pdftest.PageSpec{Content: content, Resources: res, Extra: "/Rotate 90"})
	doc := openBytes(t, b.Bytes(), Options{})
	p, _ := doc.Page(ctx, 1)
	tables, _ := p.Tables(ctx)
	if len(tables) != 1 || tables[0].Method != TableFromRules {
		t.Fatalf("tables = %+v", tables)
	}
	if got, want := tables[0].Grid(), [][]string{{"Team", "Start", "End"}, {"Support", "88", "97"}, {"Sales", "204", "224"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("grid = %q", got)
	}
}

func TestAlignedTableAndConfidence(t *testing.T) {
	doc := pagesOf(t, fmt.Sprintf(wrapped, 720)+
		cells(660, 72, "Team", 250, "Start", 350, "End")+
		cells(645, 72, "Engineering", 250, "120", 350, "134")+
		cells(630, 72, "Support", 250, "88", 350, "97")+
		cells(615, 72, "Sales and marketing", 250, "204", 350, "224")+
		// Labels and values: lined up, but no table to rely on.
		cells(540, 72, "Invoice number", 250, "INV-0417")+
		cells(525, 72, "Customer", 250, "Acme Tooling")+
		cells(510, 72, "Terms", 250, "Net thirty"))
	p, _ := doc.Page(ctx, 1)
	tables, _ := p.Tables(ctx)
	if len(tables) != 2 {
		t.Fatalf("%d tables: %+v", len(tables), tables)
	}
	if tb := tables[0]; tb.Method != TableFromAlignment || tb.Rows != 4 || tb.Cols != 3 || tb.Confidence < 0.5 || tb.Confidence > 0.9 {
		t.Errorf("first table: %s, %d x %d, confidence %.2f", tb.Method, tb.Rows, tb.Cols, tb.Confidence)
	}
	if tb := tables[1]; tb.Confidence >= 0.5 || tb.Rows != 3 || tb.Cols != 2 {
		t.Errorf("second table: %d x %d, confidence %.2f", tb.Rows, tb.Cols, tb.Confidence)
	}
	md, _ := doc.Markdown(ctx, MarkdownOptions{OmitPageMarkers: true})
	if !strings.Contains(md, "| Team | Start | End |\n| --- | --- | --- |\n| Engineering | 120 | 134 |") {
		t.Errorf("the confident table is missing:\n%s", md)
	}
	// The unsure one stays as lines of text.
	if !strings.Contains(md, "Invoice number INV-0417\nCustomer Acme Tooling\nTerms Net thirty") {
		t.Errorf("the unsure table is not plain lines:\n%s", md)
	}
}

func TestMarkdownTableConfidence(t *testing.T) {
	// One table Markdown is sure of and one it is not: see the test above.
	doc := pagesOf(t, cells(660, 72, "Team", 250, "Start", 350, "End")+
		cells(645, 72, "Engineering", 250, "120", 350, "134")+
		cells(630, 72, "Support", 250, "88", 350, "97")+
		cells(615, 72, "Sales and marketing", 250, "204", 350, "224")+
		cells(540, 72, "Invoice number", 250, "INV-0417")+
		cells(525, 72, "Customer", 250, "Acme Tooling")+
		cells(510, 72, "Terms", 250, "Net thirty"))
	p, _ := doc.Page(ctx, 1)
	tables, _ := p.Tables(ctx)
	if len(tables) != 2 || tables[0].Confidence < 0.5 || tables[1].Confidence >= 0.5 || tables[1].Confidence <= 0 {
		t.Fatalf("tables: %+v", tables)
	}
	const sure, unsure = "| Team | Start | End |", "| Invoice number | INV-0417 |"
	between := (tables[0].Confidence + tables[1].Confidence) / 2
	for _, tc := range []struct {
		name               string
		min                float64
		wantSure, wantBoth bool
	}{
		{"the zero value is the default", 0, true, false},
		{"the default by name", DefaultTableConfidence, true, false},
		{"a negative value shows every table", -1, true, true},
		{"just the confidence of the unsure table", tables[1].Confidence, true, true},
		{"between the two", between, true, false},
		{"just the confidence of the sure table", tables[0].Confidence, true, false},
		{"above both", tables[0].Confidence + 0.01, false, false},
		{"above 1 shows none", 1.5, false, false},
	} {
		md, err := doc.Markdown(ctx, MarkdownOptions{OmitPageMarkers: true, MinTableConfidence: tc.min})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		gotSure, gotUnsure := strings.Contains(md, sure), strings.Contains(md, unsure)
		if gotSure != tc.wantSure || gotUnsure != tc.wantBoth {
			t.Errorf("%s (%.2f): sure table shown %v, unsure table shown %v:\n%s", tc.name, tc.min, gotSure, gotUnsure, md)
		}
		// Whatever is not a table is still there as text.
		if !strings.Contains(md, "Engineering") || !strings.Contains(md, "Acme Tooling") {
			t.Errorf("%s: text was lost:\n%s", tc.name, md)
		}
	}
}

func TestTableAcrossPages(t *testing.T) {
	header := cells(100, 72, "Year", 250, "Low", 350, "High")
	rowsA := cells(85, 72, "2023", 250, "6.1", 350, "19.7") + cells(70, 72, "2024", 250, "6.1", 350, "19.9") + cells(55, 72, "2025", 250, "6.2", 350, "20.1")
	rowsB := cells(730, 72, "2026", 250, "6.2", 350, "20.3") + cells(715, 72, "2027", 250, "6.3", 350, "20.6") + cells(700, 72, "2028", 250, "6.4", 350, "20.8")
	after := fmt.Sprintf(wrapped, 640)
	first := fmt.Sprintf(wrapped, 720) + header + rowsA
	head := "| Year | Low | High |\n| --- | --- | --- |\n"
	bodyA := "| 2023 | 6.1 | 19.7 |\n| 2024 | 6.1 | 19.9 |\n| 2025 | 6.2 | 20.1 |\n"
	bodyB := "| 2026 | 6.2 | 20.3 |\n| 2027 | 6.3 | 20.6 |\n| 2028 | 6.4 | 20.8 |\n"

	// The table runs on with no header on the second page.
	doc := pagesOf(t, first, rowsB+after)
	md, _ := doc.Markdown(ctx, MarkdownOptions{})
	if !strings.Contains(md, head+bodyA+"\n<!-- page 2 -->\n\n"+head+bodyB) {
		t.Errorf("continued under the header of the page before:\n%s", md)
	}
	md, _ = doc.Markdown(ctx, MarkdownOptions{OmitPageMarkers: true})
	if !strings.Contains(md, head+bodyA+bodyB+"\nThis paragraph") {
		t.Errorf("joined into one table:\n%s", md)
	}

	// The second page repeats the header.
	doc = pagesOf(t, first, strings.ReplaceAll(header, " 100 Td", " 745 Td")+rowsB+after)
	md, _ = doc.Markdown(ctx, MarkdownOptions{})
	if !strings.Contains(md, head+bodyA+"\n<!-- page 2 -->\n\n"+head+bodyB) {
		t.Errorf("with a repeated header:\n%s", md)
	}
	md, _ = doc.Markdown(ctx, MarkdownOptions{OmitPageMarkers: true})
	if !strings.Contains(md, head+bodyA+bodyB+"\nThis paragraph") || strings.Count(md, "| Year |") != 1 {
		t.Errorf("repeated header not dropped:\n%s", md)
	}

	// A table that only looks alike is a table of its own: other columns,
	// or text between, or a page in between left out.
	doc = pagesOf(t, first, cells(730, 72, "Team", 250, "Start", 350, "End")+cells(715, 72, "Support", 250, "88", 350, "97")+
		cells(700, 72, "Sales", 250, "204", 350, "224")+after)
	md, _ = doc.Markdown(ctx, MarkdownOptions{OmitPageMarkers: true})
	if !strings.Contains(md, bodyA+"\n| Team | Start | End |\n| --- | --- | --- |\n| Support | 88 | 97 |") {
		t.Errorf("a different table was joined on:\n%s", md)
	}
	doc = pagesOf(t, first, "", rowsB+after)
	md, _ = doc.Markdown(ctx, MarkdownOptions{OmitPageMarkers: true, Pages: "1,3"})
	if !strings.Contains(md, bodyA+"\n| 2026 | 6.2 | 20.3 |\n| --- | --- | --- |\n") {
		t.Errorf("joined across a page that was left out:\n%s", md)
	}
}

func TestTableMarkdownEdges(t *testing.T) {
	if got := (Table{}).Markdown(); got != "" {
		t.Errorf("empty table: %q", got)
	}
	if got := (Table{}).Grid(); got != nil {
		t.Errorf("empty grid: %v", got)
	}
	tb := Table{Rows: 3, Cols: 3, Cells: []TableCell{
		{Row: 0, Col: 0, Text: "Name"}, {Row: 0, Col: 2, Text: "Notes"},
		{Row: 2, Col: 0, Text: "Ada"}, {Row: 2, Col: 2, Text: "writes pro-\ngrammes\n\nand a|b"},
		{Row: 9, Col: 9, Text: "outside"},
	}}
	// The empty row and the empty column go; the lines of a cell are
	// joined, across a hyphen where there is one.
	if got, want := tb.Markdown(), "| Name | Notes |\n| --- | --- |\n| Ada | writes programmes and a\\|b |"; got != want {
		t.Errorf("got %q", got)
	}
}

func TestTableMarkdownHeaderRows(t *testing.T) {
	// Two levels of headings: a group over two columns, a heading that
	// runs down both levels, a heading of two lines.
	tb := Table{Rows: 4, Cols: 4, HeaderRows: 2, Cells: []TableCell{
		{Row: 0, Col: 0, RowSpan: 2, ColSpan: 1, Text: "Year"},
		{Row: 0, Col: 1, RowSpan: 1, ColSpan: 2, Text: "Coal"},
		{Row: 0, Col: 3, RowSpan: 2, ColSpan: 1, Text: "Percent\nchange"},
		{Row: 1, Col: 1, RowSpan: 1, ColSpan: 1, Text: "Nominal"},
		{Row: 1, Col: 2, RowSpan: 1, ColSpan: 1, Text: "Real"},
		{Row: 2, Col: 0, Text: "2010"}, {Row: 2, Col: 1, Text: "35.61"}, {Row: 2, Col: 2, Text: "32.11"}, {Row: 2, Col: 3, Text: "6.9"},
		{Row: 3, Col: 0, Text: "2011"}, {Row: 3, Col: 1, Text: "36.91"}, {Row: 3, Col: 2, Text: "32.56"}, {Row: 3, Col: 3, Text: "1.4"},
	}}
	want := "| Year | Coal / Nominal | Coal / Real | Percent change |\n| --- | --- | --- | --- |\n" +
		"| 2010 | 35.61 | 32.11 | 6.9 |\n| 2011 | 36.91 | 32.56 | 1.4 |"
	if got := tb.Markdown(); got != want {
		t.Errorf("two levels:\n%s", got)
	}
	// The rows are what they were: Grid joins nothing.
	if got := tb.Grid(); got[0][1] != "Coal" || got[1][1] != "Nominal" || got[1][0] != "" || len(got) != 4 {
		t.Errorf("grid = %q", got)
	}
	// With the header rows not known, the first row is the header row.
	plain := tb
	plain.HeaderRows = 0
	if got := plain.Markdown(); !strings.HasPrefix(got, "| Year | Coal |  | Percent change |\n| --- | --- | --- | --- |\n|  | Nominal | Real |  |\n| 2010 |") {
		t.Errorf("no header rows known:\n%s", got)
	}
	plain.HeaderRows = 1
	if got := plain.Markdown(); !strings.HasPrefix(got, "| Year | Coal |  | Percent change |\n| --- | --- | --- | --- |\n|  | Nominal | Real |  |\n| 2010 |") {
		t.Errorf("one header row:\n%s", got)
	}

	// A line across the whole table is the heading of no one column; the
	// same heading twice over a column is said once; a column with no text
	// goes, though a group stands over it.
	tb = Table{Rows: 4, Cols: 4, HeaderRows: 3, Cells: []TableCell{
		{Row: 0, Col: 0, RowSpan: 1, ColSpan: 4, Text: "Millions of dollars"},
		{Row: 1, Col: 0, RowSpan: 1, ColSpan: 1, Text: "Year"}, {Row: 1, Col: 1, RowSpan: 1, ColSpan: 3, Text: "Sales"},
		{Row: 2, Col: 1, RowSpan: 1, ColSpan: 1, Text: "Sales"}, {Row: 2, Col: 3, RowSpan: 1, ColSpan: 1, Text: "a|b"},
		{Row: 3, Col: 0, Text: "2010"}, {Row: 3, Col: 1, Text: "12"}, {Row: 3, Col: 3, Text: "14"},
	}}
	if got, want := tb.Markdown(), "| Millions of dollars / Year | Sales | Sales / a\\|b |\n| --- | --- | --- |\n| 2010 | 12 | 14 |"; got != want {
		t.Errorf("a line across, a heading twice, an empty column:\n%s", got)
	}

	// Header rows that would leave no row under them: the last row stays.
	tb = Table{Rows: 2, Cols: 2, HeaderRows: 2, Cells: []TableCell{
		{Row: 0, Col: 0, Text: "Name"}, {Row: 0, Col: 1, Text: "Town"}, {Row: 1, Col: 0, Text: "Ada"}, {Row: 1, Col: 1, Text: "Leeds"},
	}}
	if got, want := tb.Markdown(), "| Name | Town |\n| --- | --- |\n| Ada | Leeds |"; got != want {
		t.Errorf("all rows headers:\n%s", got)
	}
	if got := (Table{Rows: 3, Cols: 2, HeaderRows: 2}).Markdown(); got != "" {
		t.Errorf("no text: %q", got)
	}
}

func TestHeaderOfSeveralLinesInMarkdown(t *testing.T) {
	// Column headings of one, two and three lines over rows of figures,
	// and the same table going on over the page under the same headings.
	header := func(y int) string {
		return cells(y, 250, "Total number", 430, "Number") +
			cells(y-12, 250, "of shares", 340, "Average price", 430, "that may") +
			cells(y-24, 72, "Period", 250, "purchased", 340, "paid per share", 430, "yet be bought")
	}
	rowsA := cells(640, 72, "October", 250, "1,815", 340, "522.10", 430, "900") +
		cells(626, 72, "November", 250, "2,204", 340, "531.75", 430, "850") +
		cells(612, 72, "December", 250, "1,960", 340, "540.20", 430, "800")
	rowsB := cells(640, 72, "January", 250, "1,422", 340, "548.00", 430, "760") +
		cells(626, 72, "February", 250, "1,507", 340, "551.30", 430, "720") +
		cells(612, 72, "March", 250, "1,633", 340, "559.90", 430, "690")
	head := "| Period | Total number of shares purchased | Average price paid per share | Number that may yet be bought |\n| --- | --- | --- | --- |\n"
	bodyA := "| October | 1,815 | 522.10 | 900 |\n| November | 2,204 | 531.75 | 850 |\n| December | 1,960 | 540.20 | 800 |\n"
	bodyB := "| January | 1,422 | 548.00 | 760 |\n| February | 1,507 | 551.30 | 720 |\n| March | 1,633 | 559.90 | 690 |\n"

	doc := pagesOf(t, header(680)+rowsA, header(680)+rowsB)
	p, _ := doc.Page(ctx, 1)
	tables, err := p.Tables(ctx)
	if err != nil || len(tables) != 1 {
		t.Fatalf("tables = %+v, %v", tables, err)
	}
	if tb := tables[0]; tb.Rows != 4 || tb.Cols != 4 || tb.HeaderRows != 1 || tb.Cells[1].Text != "Total number\nof shares\npurchased" {
		t.Errorf("table: %d x %d, %d header rows, cells %+v", tb.Rows, tb.Cols, tb.HeaderRows, tb.Cells[:4])
	}
	md, _ := doc.Markdown(ctx, MarkdownOptions{})
	if want := "<!-- page 1 -->\n\n" + head + bodyA + "\n<!-- page 2 -->\n\n" + head + bodyB; md != want {
		t.Errorf("Markdown:\n%s", md)
	}
	// With nothing between the pages the header is said once.
	md, _ = doc.Markdown(ctx, MarkdownOptions{OmitPageMarkers: true})
	if want := head + bodyA + bodyB; md != want {
		t.Errorf("Markdown without page markers:\n%s", md)
	}
}

func TestGroupHeadingInMarkdown(t *testing.T) {
	// The heading of the three columns of figures stands on a line of its
	// own above the years, with a rule under it over those columns. It is
	// a header row of the table, merged across them, and Markdown says it
	// over each. It is not also a paragraph before the table.
	doc := pagesOf(t, fmt.Sprintf(wrapped, 760)+
		cells(700, 300, "Year ended December 31,")+"0.5 w 250 696 m 480 696 l S "+
		cells(684, 250, "2023", 350, "2022", 450, "2021")+
		cells(670, 72, "Revenue", 250, "97,147", 350, "21,998", 450, "90,949")+
		cells(656, 72, "Costs", 250, "61,020", 350, "18,410", 450, "70,244")+
		cells(642, 72, "Taxes", 250, "8,300", 350, "1,150", 450, "5,906"))
	p, _ := doc.Page(ctx, 1)
	tables, err := p.Tables(ctx)
	if err != nil || len(tables) != 1 {
		t.Fatalf("tables = %+v, %v", tables, err)
	}
	tb := tables[0]
	if tb.Rows != 5 || tb.Cols != 4 || tb.HeaderRows != 2 || tb.Cells[1].Text != "Year ended December 31," || tb.Cells[1].ColSpan != 3 {
		t.Errorf("table: %d x %d, %d header rows, cells %+v", tb.Rows, tb.Cols, tb.HeaderRows, tb.Cells[:3])
	}
	if got, want := tb.Grid()[0], []string{"", "Year ended December 31,", "", ""}; !reflect.DeepEqual(got, want) {
		t.Errorf("grid = %q", got)
	}
	want := "|  | Year ended December 31, / 2023 | Year ended December 31, / 2022 | Year ended December 31, / 2021 |\n| --- | --- | --- | --- |\n" +
		"| Revenue | 97,147 | 21,998 | 90,949 |\n| Costs | 61,020 | 18,410 | 70,244 |\n| Taxes | 8,300 | 1,150 | 5,906 |\n"
	md, _ := doc.Markdown(ctx, MarkdownOptions{OmitPageMarkers: true})
	if !strings.HasSuffix(md, "an address.\n\n"+want) {
		t.Errorf("Markdown:\n%s", md)
	}
}

func TestCleanCell(t *testing.T) {
	for in, want := range map[string]string{
		"Alabama ............................": "Alabama",
		"Alaska . . . . . . . .":               "Alaska",
		"................":                     "",
		"..... 12":                             "12",
		"St. Louis":                            "St. Louis",
		"p. 5..":                               "p. 5..",
		"3.5":                                  "3.5",
		"etc.":                                 "etc.",
		"New York .........\n(city) .....":     "New York\n(city)",
		"first\n.......\nlast":                 "first\nlast",
		"":                                     "",
	} {
		if got := cleanCell(in); got != want {
			t.Errorf("cleanCell(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTitleAndNotesAreNotRows(t *testing.T) {
	// A grid whose top row is one cell across the whole width, holding the
	// table's title, and whose bottom row is one cell holding a note. The
	// table is the three rows between; the title and the note are text
	// before and after it.
	grid := "0.5 w 72 660 300 -100 re S 72 640 m 372 640 l S 72 620 m 372 620 l S 72 600 m 372 600 l S 72 580 m 372 580 l S " +
		"172 580 m 172 640 l S 272 580 m 272 640 l S "
	doc := pagesOf(t, grid+
		cells(646, 76, "Table 3. Staff by team at the start and end of the quarter")+
		cells(626, 76, "Team", 176, "Start", 276, "End")+
		cells(606, 76, "Support ..........", 176, "88", 276, "97")+
		cells(586, 76, "Sales ............", 176, "204", 276, ".........")+
		cells(566, 76, "Note: the figures count people and leave out contractors."))
	p, _ := doc.Page(ctx, 1)
	tables, _ := p.Tables(ctx)
	if len(tables) != 1 {
		t.Fatalf("%d tables", len(tables))
	}
	tb := tables[0]
	// Dot leaders are gone as well, and the cell that held nothing else is
	// empty.
	if got, want := tb.Grid(), [][]string{{"Team", "Start", "End"}, {"Support", "88", "97"}, {"Sales", "204", ""}}; !reflect.DeepEqual(got, want) {
		t.Errorf("grid = %q", got)
	}
	if want := (Box{72, 792 - 640, 372, 792 - 580}); tb.Box != want {
		t.Errorf("box = %+v, want %+v", tb.Box, want)
	}
	md, _ := doc.Markdown(ctx, MarkdownOptions{OmitPageMarkers: true})
	want := "Table 3. Staff by team at the start and end of the quarter\n\n" +
		"| Team | Start | End |\n| --- | --- | --- |\n| Support | 88 | 97 |\n| Sales | 204 |  |\n\n" +
		"Note: the figures count people and leave out contractors.\n"
	if md != want {
		t.Errorf("Markdown:\n%s", md)
	}
}

func TestDiagramIsNotATable(t *testing.T) {
	// The labels of a block diagram: four rows of three, lined up.
	labels := cells(660, 72, "conv one", 250, "conv two", 400, "conv six") +
		cells(640, 72, "pool one", 250, "pool two", 400, "pool six") +
		cells(620, 72, "norm one", 250, "norm two", 400, "norm six") +
		cells(600, 72, "full one", 250, "full two", 400, "full six")
	const shown = "| conv one | conv two | conv six |"
	for _, tc := range []struct {
		name, drawing string
		table         bool
	}{
		{"nothing drawn", "", true},
		// An arc that skips from the first box to the third, as the
		// shortcut of a network diagram does.
		{"an arc from row to row", "0.5 w 130 664 m 160 650 160 630 130 624 c S ", false},
		{"a slanting arrow from row to row", "0.5 w 200 660 m 240 622 l S ", false},
		// What may be drawn in a table: rules, a frame with rounded
		// corners, a tick beside an entry.
		{"rules between the rows", "0.5 w 72 652 m 450 652 l S 72 632 m 450 632 l S 72 612 m 450 612 l S ", true},
		{"a frame with rounded corners", "0.5 w 70 590 m 446 590 l 452 590 452 590 452 596 c 452 668 l 452 674 452 674 446 674 c " +
			"70 674 l 64 674 64 674 64 668 c 64 596 l 64 590 64 590 70 590 c h S ", true},
		{"a tick in a row", "0.5 w 340 642 m 343 639 l 349 647 l S ", true},
	} {
		doc := pagesOf(t, tc.drawing+labels)
		p, _ := doc.Page(ctx, 1)
		tables, err := p.Tables(ctx)
		if err != nil || len(tables) != 1 || tables[0].Rows != 4 || tables[0].Cols != 3 {
			t.Fatalf("%s: tables = %+v, %v", tc.name, tables, err)
		}
		if got := tables[0].Confidence >= 0.5; got != tc.table {
			t.Errorf("%s: confidence %.2f", tc.name, tables[0].Confidence)
		}
		md, _ := doc.Markdown(ctx, MarkdownOptions{OmitPageMarkers: true})
		if got := strings.Contains(md, shown); got != tc.table {
			t.Errorf("%s: shown as a table: %v\n%s", tc.name, got, md)
		}
		// The text is there either way.
		if !strings.Contains(md, "norm two") {
			t.Errorf("%s: text was lost:\n%s", tc.name, md)
		}
	}
}

func TestLabelsOnAPictureAreNotATable(t *testing.T) {
	// The labels of a chart's two axes, with the plot, a picture, between
	// them; then the same text with the picture as a backdrop to it, and
	// as the whole page.
	labels := cells(660, 72, "Percent", 400, "Index") +
		cells(640, 72, "30", 400, "120") +
		cells(620, 72, "20", 400, "110") +
		cells(600, 72, "10", 400, "100")
	for _, tc := range []struct {
		name, place string
		invisible   bool
		table       bool
	}{
		{"no picture", "", false, true},
		{"a plot between the axes", "q 280 0 0 70 100 596 cm /Im Do Q ", false, false},
		{"a backdrop for the table", "q 380 0 0 90 60 590 cm /Im Do Q ", false, true},
		{"the page's background", "q 612 0 0 792 0 0 cm /Im Do Q ", false, true},
		// A scan in two strips with its text layer: the pictures are the
		// page, and the text over them is all there is to read.
		{"a scan with hidden text", "q 612 0 0 300 0 492 cm /Im Do Q q 612 0 0 300 0 192 cm /Im Do Q ", true, true},
	} {
		b := pdftest.New()
		img := b.AddStream("<< /Type /XObject /Subtype /Image /Width 1 /Height 1 /ColorSpace /DeviceGray /BitsPerComponent 8 >>", []byte{200})
		res := fmt.Sprintf("<< /Font << /F1 %d 0 R >> /XObject << /Im %d 0 R >> >>", b.Add(helvetica), img)
		text := labels
		if tc.invisible {
			text = "3 Tr " + labels
		}
		b.Pages(pdftest.PageSpec{Content: tc.place + text, Resources: res})
		doc := openBytes(t, b.Bytes(), Options{})
		p, _ := doc.Page(ctx, 1)
		tables, err := p.Tables(ctx)
		if err != nil || len(tables) != 1 || tables[0].Rows != 4 || tables[0].Cols != 2 {
			t.Fatalf("%s: tables = %+v, %v", tc.name, tables, err)
		}
		if got := tables[0].Confidence >= 0.5; got != tc.table {
			t.Errorf("%s: confidence %.2f", tc.name, tables[0].Confidence)
		}
	}
}

func TestTrimRows(t *testing.T) {
	cell := func(row, col, span int, text string) TableCell {
		return TableCell{Row: row, Col: col, RowSpan: 1, ColSpan: span, Text: text, Box: Box{float64(col), float64(row), float64(col + span), float64(row + 1)}}
	}
	// A title, an empty row, two rows, two notes.
	tb := Table{Rows: 6, Cols: 2, HeaderRows: 3, Cells: []TableCell{
		cell(0, 0, 2, "Title"), cell(1, 0, 1, ""), cell(1, 1, 1, ""),
		cell(2, 0, 1, "a"), cell(2, 1, 1, "b"), cell(3, 0, 1, "c"), cell(3, 1, 1, "d"),
		cell(4, 0, 2, "Note 1"), cell(5, 0, 2, "Note 2"),
	}}
	cut := trimRows(&tb)
	if tb.Rows != 2 || len(tb.Cells) != 4 || tb.Cells[0].Row != 0 || tb.Cells[3].Row != 1 || len(cut) != 5 || tb.HeaderRows != 1 {
		t.Errorf("after trimming: %d rows, %d cells, %d cut, %d header rows", tb.Rows, len(tb.Cells), len(cut), tb.HeaderRows)
	}
	if want := (Box{0, 2, 2, 4}); tb.Box != want {
		t.Errorf("box = %+v", tb.Box)
	}
	// Two rows are the least a table has: nothing is taken from this one,
	// though its first row is one cell across.
	small := Table{Rows: 2, Cols: 2, Cells: []TableCell{cell(0, 0, 2, "Heading over both"), cell(1, 0, 1, "a"), cell(1, 1, 1, "b")}}
	if cut := trimRows(&small); cut != nil || small.Rows != 2 {
		t.Errorf("a table of two rows was cut to %d", small.Rows)
	}
	// A heading over some of the columns is a heading, not a title, and a
	// label in the middle of the table is a row.
	kept := Table{Rows: 4, Cols: 3, Cells: []TableCell{
		cell(0, 1, 2, "Both years"), cell(1, 0, 1, "x"), cell(1, 1, 1, "1"), cell(1, 2, 1, "2"),
		cell(2, 0, 3, "Addenda:"), cell(3, 0, 1, "y"), cell(3, 1, 1, "3"), cell(3, 2, 1, "4"),
	}}
	if cut := trimRows(&kept); cut != nil || kept.Rows != 4 {
		t.Errorf("rows that belong were cut: %d left", kept.Rows)
	}
}

func TestCellContinuedOverThePage(t *testing.T) {
	// A table of wrapped text ruled off row by row. Its second page
	// repeats the header and starts with the rest of a cell from the first
	// page, which has text in the second column only.
	rule := func(y int) string { return fmt.Sprintf("0.5 w 66 %d m 500 %d l S ", y, y) }
	first := rule(712) + cells(700, 72, "Category", 200, "Subcategories") + rule(694) +
		cells(682, 72, "Govern", 200, "Policies and procedures are in place across the") +
		cells(670, 200, "organisation, and those who carry them out know") + rule(664)
	second := rule(712) + cells(700, 72, "Category", 200, "Subcategories") + rule(694) +
		cells(682, 200, "them well and have been trained in them, which") +
		cells(670, 200, "is checked every year by someone from outside") +
		cells(658, 200, "the team.") + rule(652) +
		cells(640, 72, "Map", 200, "The context is established and understood by") +
		cells(628, 200, "all who work on the system.") + rule(622) +
		cells(610, 72, "Measure", 200, "Risks are assessed with methods fit for them") +
		cells(598, 200, "and are tracked over time.") + rule(592)
	doc := pagesOf(t, first, second)
	p, _ := doc.Page(ctx, 2)
	tables, err := p.Tables(ctx)
	if err != nil || len(tables) != 1 {
		t.Fatalf("tables = %+v, %v", tables, err)
	}
	want := [][]string{{"Category", "Subcategories"},
		{"", "them well and have been trained in them, which\nis checked every year by someone from outside\nthe team."},
		{"Map", "The context is established and understood by\nall who work on the system."},
		{"Measure", "Risks are assessed with methods fit for them\nand are tracked over time."}}
	if got := tables[0].Grid(); !reflect.DeepEqual(got, want) || tables[0].Confidence < 0.5 {
		t.Errorf("grid = %q, confidence %.2f", got, tables[0].Confidence)
	}
	// In Markdown the rest of the cell is a row of the table, under the
	// header both pages have, and not a paragraph before it.
	md, _ := doc.Markdown(ctx, MarkdownOptions{OmitPageMarkers: true})
	wantMD := "| Category | Subcategories |\n| --- | --- |\n" +
		"| Govern | Policies and procedures are in place across the organisation, and those who carry them out know |\n" +
		"|  | them well and have been trained in them, which is checked every year by someone from outside the team. |\n" +
		"| Map | The context is established and understood by all who work on the system. |\n" +
		"| Measure | Risks are assessed with methods fit for them and are tracked over time. |\n"
	if md != wantMD {
		t.Errorf("Markdown:\n%s", md)
	}
}

func TestControlCharactersAreNotText(t *testing.T) {
	// A font that maps a glyph to a backspace, as one producer does for
	// the glyph that pulls a row of dots back, and another to a tab.
	b := pdftest.New()
	tu := b.AddStream("<< >>", []byte("3 beginbfchar <41> <0041> <42> <0008> <43> <0009> endbfchar"))
	f := b.Add(fmt.Sprintf(`<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /ToUnicode %d 0 R >>`, tu))
	b.Pages(pdftest.PageSpec{Content: "BT /F1 12 Tf 72 700 Td (ABACA) Tj ET", Resources: fmt.Sprintf("<< /Font << /F1 %d 0 R >> >>", f)})
	doc := openBytes(t, b.Bytes(), Options{})
	p, _ := doc.Page(ctx, 1)
	if text, _ := p.Text(ctx); text != "AA A" {
		t.Errorf("text = %q", text)
	}
}
