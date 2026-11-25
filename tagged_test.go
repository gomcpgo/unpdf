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

// taggedDoc builds a one-page tagged document. Its structure tree reads:
// a heading (tagged with a type of the document's own, mapped to H1, and
// set no larger than the text), a paragraph, a list of two items, a table,
// a paragraph with a footnote, and last a paragraph that stands at the top
// of the page. A running head is marked as an artifact.
func taggedDoc(t *testing.T, markInfo, extraContent string, damage func(b *pdftest.Builder, elems map[string]int)) *Document {
	t.Helper()
	return openBytes(t, taggedBytes(markInfo, extraContent, damage), Options{})
}

func taggedBytes(markInfo, extraContent string, damage func(b *pdftest.Builder, elems map[string]int)) []byte {
	b := pdftest.New()
	res := fmt.Sprintf("<< /Font << /F1 %d 0 R >> >>", b.Add(helvetica))
	e := map[string]int{}
	for _, name := range strings.Fields("root doc h p1 l li1 lbl1 lbody1 li2 lbody2 tb r1 th1 th2 th3 r2 td1 td2 td3 r3 tdspan tdtotal p2 note p3 parents") {
		e[name] = b.Reserve()
	}
	ref := func(names ...string) string {
		var out []string
		for _, n := range names {
			out = append(out, fmt.Sprintf("%d 0 R", e[n]))
		}
		return strings.Join(out, " ")
	}
	elem := func(name, role, parent, kids, more string) {
		b.Set(e[name], fmt.Sprintf("<< /Type /StructElem /S /%s /P %s /K [%s] %s >>", role, ref(parent), kids, more))
	}
	b.Set(e["root"], fmt.Sprintf("<< /Type /StructTreeRoot /K %s /ParentTree %s /RoleMap << /Heading1 /H1 /Chapter /Sect >> >>", ref("doc"), ref("parents")))
	elem("doc", "Document", "root", ref("h", "p1", "l", "tb", "p2", "p3"), "")
	elem("h", "Heading1", "doc", "0", "")
	elem("p1", "P", "doc", "1", "")
	elem("l", "L", "doc", ref("li1", "li2"), "")
	elem("li1", "LI", "l", ref("lbl1", "lbody1"), "")
	elem("lbl1", "Lbl", "li1", "2", "")
	elem("lbody1", "LBody", "li1", "3", "")
	elem("li2", "LI", "l", ref("lbody2"), "")
	elem("lbody2", "LBody", "li2", "4", "")
	elem("tb", "Table", "doc", ref("r1", "r2", "r3"), "")
	elem("r1", "TR", "tb", ref("th1", "th2", "th3"), "")
	elem("th1", "TH", "r1", "5", "")
	elem("th2", "TH", "r1", "6", "")
	elem("th3", "TH", "r1", "7", "")
	elem("r2", "TR", "tb", ref("td1", "td2", "td3"), "")
	elem("td1", "TD", "r2", "8", "")
	elem("td2", "TD", "r2", "", "") // an empty cell: nothing on the page
	elem("td3", "TD", "r2", "9", "")
	elem("r3", "TR", "tb", ref("tdspan", "tdtotal"), "")
	elem("tdspan", "TD", "r3", "10", "/A << /O /Table /ColSpan 2 >>")
	elem("tdtotal", "TD", "r3", "11", "")
	elem("p2", "P", "doc", "12 "+ref("note"), "")
	elem("note", "Note", "p2", "13", "")
	elem("p3", "P", "doc", "14", "")
	b.Set(e["parents"], fmt.Sprintf("<< /Nums [0 [%s]] >>",
		ref("h", "p1", "lbl1", "lbody1", "lbody2", "th1", "th2", "th3", "td1", "td3", "tdspan", "tdtotal", "p2", "note", "p3")))
	if damage != nil {
		damage(b, e)
	}

	mc := func(tag string, id, size, x, y int, text string) string {
		return fmt.Sprintf("/%s << /MCID %d >> BDC BT /F1 %d Tf %d %d Td (%s) Tj ET EMC ", tag, id, size, x, y, text)
	}
	content := "/Artifact BMC BT /F1 8 Tf 72 770 Td (Running head) Tj ET EMC " +
		mc("P", 14, 10, 72, 740, "Set at the top of the page but read last.") +
		mc("Heading1", 0, 10, 72, 700, "Annual report") +
		mc("P", 1, 10, 72, 680, "Revenue grew in every region.") +
		mc("Lbl", 2, 10, 72, 660, "1.") + mc("LBody", 3, 10, 90, 660, "first step") +
		mc("LBody", 4, 10, 90, 645, "an item with a drawn bullet") +
		mc("TH", 5, 10, 72, 610, "Team") + mc("TH", 6, 10, 250, 610, "Start") + mc("TH", 7, 10, 350, 610, "End") +
		mc("TD", 8, 10, 72, 595, "Support") + mc("TD", 9, 10, 350, 595, "97") +
		mc("TD", 10, 10, 72, 580, "Total staff") + mc("TD", 11, 10, 350, 580, "455") +
		mc("P", 12, 10, 72, 540, "A closing paragraph.") +
		mc("Note", 13, 8, 72, 100, "1 A footnote.") + extraContent
	b.CatalogExtra = fmt.Sprintf("/StructTreeRoot %s /MarkInfo %s", ref("root"), markInfo)
	b.Pages(pdftest.PageSpec{Content: content, Resources: res, Extra: "/StructParents 0"})
	return b.Bytes()
}

func TestTaggedPage(t *testing.T) {
	doc := taggedDoc(t, "<< /Marked true >>", "", nil)
	p, _ := doc.Page(ctx, 1)
	blocks, err := p.Blocks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		text      string
		kind      BlockKind
		level     int
		marker    string
		furniture bool
	}
	want := []row{
		{"Running head", Paragraph, 0, "", true},
		{"Annual report", Heading, 1, "", false},
		{"Revenue grew in every region.", Paragraph, 0, "", false},
		{"1. first step", ListItem, 0, "1.", false},
		{"an item with a drawn bullet", ListItem, 0, "", false},
		// The cells of the table, read row by row as one block.
		{"Team Start End\nSupport 97\nTotal staff 455", Paragraph, 0, "", false},
		{"A closing paragraph.", Paragraph, 0, "", false},
		{"Set at the top of the page but read last.", Paragraph, 0, "", false},
		{"1 A footnote.", Paragraph, 0, "", false},
	}
	var got []row
	for _, b := range blocks {
		got = append(got, row{b.Text(), b.Kind, b.Level, b.Marker, b.Furniture})
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("blocks:\n got %+v\nwant %+v", got, want)
	}

	tables, err := p.Tables(ctx)
	if err != nil || len(tables) != 1 {
		t.Fatalf("tables = %+v, %v", tables, err)
	}
	tb := tables[0]
	if tb.Method != TableFromStructure || tb.Rows != 3 || tb.Cols != 3 || tb.HeaderRows != 1 || tb.Confidence < 0.9 {
		t.Errorf("table: %s, %d x %d, %d header rows, confidence %.2f", tb.Method, tb.Rows, tb.Cols, tb.HeaderRows, tb.Confidence)
	}
	// The empty cell holds its place, and the merged cell spans two.
	if got, want := tb.Grid(), [][]string{{"Team", "Start", "End"}, {"Support", "", "97"}, {"Total staff", "", "455"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("grid = %q", got)
	}
	for _, c := range tb.Cells {
		if wantSpan := map[bool]int{true: 2, false: 1}[c.Text == "Total staff"]; c.ColSpan != wantSpan || c.RowSpan != 1 {
			t.Errorf("cell %q spans %d x %d", c.Text, c.RowSpan, c.ColSpan)
		}
	}

	md, _ := doc.Markdown(ctx, MarkdownOptions{OmitPageMarkers: true})
	wantMD := "# Annual report\n\n" +
		"Revenue grew in every region.\n\n" +
		"1. first step\n- an item with a drawn bullet\n\n" +
		"| Team | Start | End |\n| --- | --- | --- |\n| Support |  | 97 |\n| Total staff |  | 455 |\n\n" +
		"A closing paragraph.\n\n" +
		"Set at the top of the page but read last.\n\n" +
		"1 A footnote.\n"
	if md != wantMD {
		t.Errorf("Markdown:\n%s\nwant:\n%s", md, wantMD)
	}
	if text, _ := p.Text(ctx); !strings.HasPrefix(text, "Running head\n\nAnnual report\n\nRevenue grew") ||
		!strings.Contains(text, "\n\nTeam Start End\nSupport 97\nTotal staff 455\n\n") {
		t.Errorf("text = %q", text)
	}
}

func TestTagsNotUsed(t *testing.T) {
	geometric := func(name string, doc *Document) {
		t.Helper()
		p, _ := doc.Page(ctx, 1)
		blocks, err := p.Blocks(ctx)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var first *Block
		for i := range blocks {
			if !blocks[i].Furniture {
				first = &blocks[i]
				break
			}
		}
		// Read from the top of the page down, as the layout has it.
		if first == nil || !strings.HasPrefix(first.Text(), "Set at the top of the page") {
			t.Errorf("%s: the page was not laid out by its geometry: %v", name, first)
		}
		if tables, _ := p.Tables(ctx); len(tables) != 1 || tables[0].Method != TableFromAlignment {
			t.Errorf("%s: tables = %+v", name, tables)
		}
	}
	// The document calls its own tags suspect.
	geometric("suspect", taggedDoc(t, "<< /Marked true /Suspects true >>", "", nil))
	// Much of the page's text is in no tag at all.
	loose := ""
	for i := 0; i < 8; i++ {
		loose += fmt.Sprintf("BT /F1 10 Tf 72 %d Td (A line of text that the structure tree does not know about.) Tj ET ", 480-12*i)
	}
	geometric("untagged text", taggedDoc(t, "<< /Marked true >>", loose, nil))
	// The parent tree is missing, or has no entry for the page.
	geometric("no parent tree", taggedDoc(t, "<< /Marked true >>", "", func(b *pdftest.Builder, e map[string]int) {
		b.Set(e["parents"], "null")
	}))
	geometric("no entry", taggedDoc(t, "<< /Marked true >>", "", func(b *pdftest.Builder, e map[string]int) {
		b.Set(e["parents"], "<< /Nums [5 []] >>")
	}))
}

func TestDamagedTags(t *testing.T) {
	// Whatever is wrong with the tree, the page still gives all its text.
	for name, damage := range map[string]func(b *pdftest.Builder, e map[string]int){
		"an element that is its own parent": func(b *pdftest.Builder, e map[string]int) {
			b.Set(e["p1"], fmt.Sprintf("<< /Type /StructElem /S /P /P %d 0 R /K 1 >>", e["p1"]))
		},
		"two elements that are each other's parent": func(b *pdftest.Builder, e map[string]int) {
			b.Set(e["l"], fmt.Sprintf("<< /S /L /P %d 0 R /K [] >>", e["li1"]))
		},
		"a cell with no row": func(b *pdftest.Builder, e map[string]int) {
			b.Set(e["td1"], fmt.Sprintf("<< /S /TD /P %d 0 R /K 8 >>", e["tb"]))
		},
		"a parent tree of the wrong kind": func(b *pdftest.Builder, e map[string]int) {
			b.Set(e["parents"], "<< /Nums [0 [1 2 (three) null << >>]] >>")
		},
		"a parent tree that is its own child": func(b *pdftest.Builder, e map[string]int) {
			b.Set(e["parents"], fmt.Sprintf("<< /Kids [%d 0 R %d 0 R %d 0 R] >>", e["parents"], e["parents"], e["parents"]))
		},
		"elements that are not dictionaries": func(b *pdftest.Builder, e map[string]int) {
			b.Set(e["h"], "42")
			b.Set(e["tb"], "[1 2 3]")
		},
		"a role map that goes round in a circle": func(b *pdftest.Builder, e map[string]int) {
			b.Set(e["root"], fmt.Sprintf("<< /K %d 0 R /ParentTree %d 0 R /RoleMap << /Heading1 /Other /Other /Heading1 >> >>", e["doc"], e["parents"]))
		},
		"absurd spans": func(b *pdftest.Builder, e map[string]int) {
			b.Set(e["tdspan"], fmt.Sprintf("<< /S /TD /P %d 0 R /K 10 /A [<< /ColSpan 99999999 /RowSpan -4 >> 0 << /RowSpan 900 >>] >>", e["r3"]))
		},
	} {
		doc := taggedDoc(t, "<< /Marked true >>", "", damage)
		p, _ := doc.Page(ctx, 1)
		text, err := p.Text(ctx)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		for _, want := range []string{"Annual report", "first step", "Support", "455", "A closing paragraph.", "footnote"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s: %q is missing from the text", name, want)
			}
		}
		if _, err := p.Tables(ctx); err != nil {
			t.Errorf("%s: tables: %v", name, err)
		}
		if _, err := doc.Markdown(ctx, MarkdownOptions{}); err != nil {
			t.Errorf("%s: Markdown: %v", name, err)
		}
	}
}

func TestHeadingNumber(t *testing.T) {
	for role, want := range map[string]int{"H1": 1, "H6": 6, "H12": 12, "H": 0, "H0": 0, "Hx": 0, "H123": 0, "P": 0, "": 0, "TH": 0} {
		if got := headingNumber(role); got != want {
			t.Errorf("headingNumber(%q) = %d, want %d", role, got, want)
		}
	}
}
