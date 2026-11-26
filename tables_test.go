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
