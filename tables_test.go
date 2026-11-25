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
