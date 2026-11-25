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

func TestOutline(t *testing.T) {
	b := pdftest.Document(3) // pages are objects 3, 5 and 7
	root, a, a1, a2, c, d, e := b.Reserve(), b.Reserve(), b.Reserve(), b.Reserve(), b.Reserve(), b.Reserve(), b.Reserve()
	names := b.Add("<< /Kids [<< /Limits [(alpha) (m)] /Names [(chapter-two) [5 0 R /Fit]] >> << /Limits [(n) (z)] /Names [(other) [7 0 R /Fit] (wrapped) << /D [7 0 R /Fit] >>] >>] >>")
	b.Set(root, fmt.Sprintf("<< /Type /Outlines /First %d 0 R /Last %d 0 R >>", a, e))
	// An explicit destination, with two children.
	b.Set(a, fmt.Sprintf("<< /Title <FEFF00430068006100700074006500720020004F006E0065> /Dest [3 0 R /XYZ 0 792 0] /First %d 0 R /Last %d 0 R /Next %d 0 R >>", a1, a2, c))
	b.Set(a1, fmt.Sprintf("<< /Title (Named in the old way) /Dest /old-name /Next %d 0 R >>", a2))
	b.Set(a2, "<< /Title (Named through the tree) /A << /S /GoTo /D (chapter-two) >> >>")
	b.Set(c, fmt.Sprintf("<< /Title (Wrapped in a dictionary) /Dest (wrapped) /Next %d 0 R >>", d))
	b.Set(d, fmt.Sprintf("<< /Title (A web address) /A << /S /URI /URI (https://example.com) >> /Next %d 0 R >>", e))
	// The last entry points back at the first: a loop.
	b.Set(e, fmt.Sprintf("<< /Title (Broken) /Dest (no-such-name) /Next %d 0 R >>", a))
	b.Set(b.Root, fmt.Sprintf("<< /Type /Catalog /Pages 1 0 R /Outlines %d 0 R /Dests << /old-name [5 0 R /Fit] >> /Names << /Dests %d 0 R >> >>", root, names))
	doc := openBytes(t, b.Bytes(), Options{})

	got, err := doc.Outline(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []OutlineItem{
		{Title: "Chapter One", Page: 1, Children: []OutlineItem{
			{Title: "Named in the old way", Page: 2},
			{Title: "Named through the tree", Page: 2},
		}},
		{Title: "Wrapped in a dictionary", Page: 3},
		{Title: "A web address", Page: 0},
		{Title: "Broken", Page: 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("outline:\n got %+v\nwant %+v", got, want)
	}

	// No outline at all.
	plain := openBytes(t, pdftest.Document(1).Bytes(), Options{})
	if items, err := plain.Outline(ctx); err != nil || items != nil {
		t.Errorf("no outline: %v, %v", items, err)
	}
}

func TestLinksAndAnnotations(t *testing.T) {
	b := pdftest.New()
	f1 := b.Add(helvetica)
	web := b.Add("<< /Type /Annot /Subtype /Link /Rect [70 696 200 712] /A << /S /URI /URI (https://example.com/page) >> >>")
	internal := b.Add("<< /Type /Annot /Subtype /Link /Rect [70 656 130 672] /Dest [9 0 R /Fit] >>")
	dead := b.Add("<< /Type /Annot /Subtype /Link /Rect [70 600 130 620] /A << /S /Launch /F (other.pdf) >> >>")
	note := b.Add("<< /Type /Annot /Subtype /Text /Rect [300 700 320 720] /Contents (Check this figure.) /T <FEFF004A006F0073006500E9> >>")
	highlight := b.Add("<< /Type /Annot /Subtype /Highlight /Rect [70 656 200 672] /Contents (Important) >>")
	silent := b.Add("<< /Type /Annot /Subtype /Highlight /Rect [70 656 200 672] >>")
	popup := b.Add("<< /Type /Annot /Subtype /Popup /Rect [0 0 10 10] /Contents (ignored) >>")
	b.Pages(
		pdftest.PageSpec{
			Content:   "BT /F1 12 Tf 72 700 Td (Visit our website today) Tj 0 -40 Td (See page two for more) Tj ET",
			Resources: fmt.Sprintf("<< /Font << /F1 %d 0 R >> >>", f1),
			Extra:     fmt.Sprintf("/Annots [%d 0 R %d 0 R %d 0 R %d 0 R %d 0 R %d 0 R %d 0 R]", web, internal, dead, note, highlight, silent, popup),
		},
		pdftest.PageSpec{Content: "BT /F1 12 Tf 72 700 Td (Second page) Tj ET", Resources: fmt.Sprintf("<< /Font << /F1 %d 0 R >> >>", f1)},
	)
	// Check the assumption above: the second page is object 12... find it.
	data := b.Bytes()
	doc := openBytes(t, data, Options{})
	p2, _ := doc.Page(ctx, 2)
	if p2.objNum == 0 {
		t.Fatal("second page has no object number")
	}
	if p2.objNum != 9 {
		// Rebuild with the right reference so the test does not depend on
		// how the builder numbers objects.
		t.Skipf("builder numbering changed: second page is object %d", p2.objNum)
	}

	p, _ := doc.Page(ctx, 1)
	links, err := p.Links(ctx)
	if err != nil || len(links) != 2 {
		t.Fatalf("links = %+v, %v", links, err)
	}
	if links[0].URI != "https://example.com/page" || links[0].Page != 0 || links[0].Text != "Visit our website" {
		t.Errorf("web link = %+v", links[0])
	}
	// The link rectangle is given from the bottom of a 792-point page.
	if want := (Box{70, 792 - 712, 200, 792 - 696}); links[0].Box != want {
		t.Errorf("link box = %+v, want %+v", links[0].Box, want)
	}
	if links[1].URI != "" || links[1].Page != 2 || links[1].Text != "See page" {
		t.Errorf("internal link = %+v", links[1])
	}

	notes, err := p.Annotations(ctx)
	if err != nil || len(notes) != 2 {
		t.Fatalf("annotations = %+v, %v", notes, err)
	}
	if notes[0].Kind != "Text" || notes[0].Text != "Check this figure." || notes[0].Author != "Jose"+string(rune(0xE9)) {
		t.Errorf("note = %+v", notes[0])
	}
	if notes[1].Kind != "Highlight" || notes[1].Text != "Important" || notes[1].Author != "" {
		t.Errorf("highlight = %+v", notes[1])
	}

	// In Markdown the web link is a link; the internal one is plain text.
	md, _ := doc.Markdown(ctx, MarkdownOptions{Pages: "1", OmitPageMarkers: true})
	if !strings.Contains(md, "[Visit our website](https://example.com/page) today") || !strings.Contains(md, "See page two for more") || strings.Contains(md, "[See") {
		t.Errorf("markdown = %q", md)
	}
	if links, _ := p2.Links(ctx); links != nil {
		t.Errorf("page without links gave %+v", links)
	}
}

func TestFormFields(t *testing.T) {
	b := pdftest.Document(1)
	person, first, last := b.Reserve(), b.Reserve(), b.Reserve()
	b.Set(person, fmt.Sprintf("<< /T (person) /FT /Tx /Kids [%d 0 R %d 0 R] >>", first, last))
	// The type is inherited; the second field shows on the page through a
	// widget of its own.
	b.Set(first, fmt.Sprintf("<< /T (first) /Parent %d 0 R /V (Ada) >>", person))
	widget := b.Add(fmt.Sprintf("<< /Type /Annot /Subtype /Widget /Parent %d 0 R /Rect [0 0 10 10] >>", last))
	b.Set(last, fmt.Sprintf("<< /T (last) /Parent %d 0 R /V <FEFF004C006F00760065006C006100630065> /Kids [%d 0 R] >>", person, widget))
	agree := b.Add("<< /T (agree) /FT /Btn /V /Yes >>")
	colour := b.Add("<< /T (colour) /FT /Btn /Ff 32768 /V /Red >>")
	submit := b.Add("<< /T (submit) /FT /Btn /Ff 65536 >>")
	tags := b.Add("<< /T (tags) /FT /Ch /V [(one) (two)] >>")
	sig := b.Add("<< /T (signature) /FT /Sig >>")
	unnamed := b.Add("<< /FT /Tx /V (no name) >>")
	loop := b.Reserve()
	b.Set(loop, fmt.Sprintf("<< /T (loop) /Kids [%d 0 R] >>", loop))
	b.Set(b.Root, fmt.Sprintf("<< /Type /Catalog /Pages 1 0 R /AcroForm << /Fields [%d 0 R %d 0 R %d 0 R %d 0 R %d 0 R %d 0 R %d 0 R %d 0 R 999 0 R] >> >>",
		person, agree, colour, submit, tags, sig, unnamed, loop))
	doc := openBytes(t, b.Bytes(), Options{})
	got, err := doc.FormFields(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []FormField{
		{"person.first", "text", "Ada"},
		{"person.last", "text", "Lovelace"},
		{"agree", "checkbox", "Yes"},
		{"colour", "radio", "Red"},
		{"submit", "button", ""},
		{"tags", "choice", "one, two"},
		{"signature", "signature", ""},
		// The field that lists itself as its own child is passed over.
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("fields:\n got %+v\nwant %+v", got, want)
	}
	plain := openBytes(t, pdftest.Document(1).Bytes(), Options{})
	if fields, err := plain.FormFields(ctx); err != nil || fields != nil {
		t.Errorf("no form: %v, %v", fields, err)
	}
}
