// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package unpdf

import (
	"context"
	"math"
	"strings"

	"github.com/gomcpgo/unpdf/internal/pdfobj"
)

// Link is a clickable area of a page.
type Link struct {
	// Box is the area, in the same coordinates as the boxes of Blocks.
	Box Box
	// URI is the address the link opens. It is empty for a link to another
	// place in the document.
	URI string
	// Page is the number of the page the link jumps to, from 1, or 0 for a
	// link that leaves the document.
	Page int
	// Text is the text under the link: the words whose middle lies inside
	// Box.
	Text string
}

// Annotation is a comment someone added to a page: a sticky note, a
// highlight with a remark, text typed onto the page.
type Annotation struct {
	// Kind is the annotation type as the PDF names it: Text for a sticky
	// note, FreeText, Highlight, Underline, StrikeOut, Squiggly, Stamp,
	// Caret, Ink and so on.
	Kind string
	// Box is the area the annotation covers.
	Box Box
	// Text is the comment.
	Text string
	// Author is who wrote it, when the document says.
	Author string
}

// displayBox converts a rectangle from PDF page space to the display space
// that Box uses.
func (p *Page) displayBox(r Rect) Box {
	crop := p.CropBox
	w, h := crop.Width(), crop.Height()
	corner := func(x, y float64) (float64, float64) {
		x, y = x-crop.X0, y-crop.Y0
		switch p.Rotate {
		case 90:
			return y, x
		case 180:
			return w - x, y
		case 270:
			return h - y, w - x
		}
		return x, h - y
	}
	ax, ay := corner(r.X0, r.Y0)
	bx, by := corner(r.X1, r.Y1)
	return Box{math.Min(ax, bx), math.Min(ay, by), math.Max(ax, bx), math.Max(ay, by)}
}

// Links returns the links on the page, each with the text it covers.
func (p *Page) Links(ctx context.Context) ([]Link, error) {
	f := p.doc.f
	var links []Link
	annots := f.Array(ctx, p.dict["Annots"])
	if len(annots) > maxAnnotations {
		annots = annots[:maxAnnotations]
	}
	for _, a := range annots {
		annot := f.Dict(ctx, a)
		if f.Name(ctx, annot["Subtype"]) != "Link" {
			continue
		}
		rect, ok := p.doc.rect(ctx, annot["Rect"])
		if !ok {
			continue
		}
		link := Link{Box: p.displayBox(rect)}
		dest := annot["Dest"]
		if action := f.Dict(ctx, annot["A"]); action != nil {
			switch f.Name(ctx, action["S"]) {
			case "URI":
				uri, _ := f.String(ctx, action["URI"])
				link.URI = strings.TrimSpace(string(uri))
			case "GoTo":
				dest = action["D"]
			}
		}
		if link.URI == "" {
			link.Page = p.doc.destinationPage(ctx, dest)
		}
		if link.URI == "" && link.Page == 0 {
			continue
		}
		links = append(links, link)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(links) == 0 {
		return nil, nil
	}
	blocks, err := p.Blocks(ctx)
	if err != nil {
		return nil, err
	}
	for i := range links {
		var words []string
		box := links[i].Box
		for _, b := range blocks {
			if b.Box.X1 < box.X0 || b.Box.X0 > box.X1 || b.Box.Y1 < box.Y0 || b.Box.Y0 > box.Y1 {
				continue
			}
			for _, l := range b.Lines {
				for _, w := range l.Words {
					if inside(w.Box, box) {
						words = append(words, w.Text)
					}
				}
			}
		}
		links[i].Text = strings.Join(words, " ")
	}
	return links, nil
}

// inside reports whether the middle of word lies in area.
func inside(word, area Box) bool {
	mx, my := (word.X0+word.X1)/2, (word.Y0+word.Y1)/2
	return mx >= area.X0 && mx <= area.X1 && my >= area.Y0 && my <= area.Y1
}

// Annotations returns the comments on the page: every annotation that
// carries text of its own, except links and form fields. Text typed onto
// the page and the values of form fields are part of Text and Blocks as
// well.
func (p *Page) Annotations(ctx context.Context) ([]Annotation, error) {
	f := p.doc.f
	var out []Annotation
	annots := f.Array(ctx, p.dict["Annots"])
	if len(annots) > maxAnnotations {
		annots = annots[:maxAnnotations]
	}
	for _, a := range annots {
		annot := f.Dict(ctx, a)
		kind := f.Name(ctx, annot["Subtype"])
		switch kind {
		case "", "Link", "Widget", "Popup":
			continue
		}
		contents, _ := f.String(ctx, annot["Contents"])
		text := strings.TrimSpace(pdfobj.DecodeText(contents))
		if text == "" {
			continue
		}
		author, _ := f.String(ctx, annot["T"])
		note := Annotation{Kind: string(kind), Text: text, Author: strings.TrimSpace(pdfobj.DecodeText(author))}
		if rect, ok := p.doc.rect(ctx, annot["Rect"]); ok {
			note.Box = p.displayBox(rect)
		}
		out = append(out, note)
	}
	return out, ctx.Err()
}

// FormField is a field of an interactive form.
type FormField struct {
	// Name is the field's full name: the names of the fields above it and
	// its own, joined by full stops.
	Name string
	// Kind is "text", "checkbox", "radio", "button", "choice" or
	// "signature". It is empty when the document does not say.
	Kind string
	// Value is what the field holds. For a checkbox or a radio group it is
	// the name of the state that is on, commonly "Yes", or "Off".
	Value string
}

const (
	maxFormFields = 50000
	maxFormDepth  = 32
)

// FormFields returns the fields of the document's form with their values.
// It is empty when the document has no form.
func (d *Document) FormFields(ctx context.Context) ([]FormField, error) {
	f := d.f
	form := f.Dict(ctx, d.catalog["AcroForm"])
	if form == nil {
		return nil, ctx.Err()
	}
	var out []FormField
	seen := map[int]bool{}
	type inherited struct {
		name  string
		kind  pdfobj.Name
		value pdfobj.Object
		flags int64
	}
	var walk func(nodes pdfobj.Array, in inherited, depth int)
	walk = func(nodes pdfobj.Array, in inherited, depth int) {
		for _, n := range nodes {
			if ctx.Err() != nil || len(out) >= maxFormFields {
				return
			}
			if ref, ok := n.(pdfobj.Ref); ok {
				if seen[ref.Num] {
					continue
				}
				seen[ref.Num] = true
			}
			node := f.Dict(ctx, n)
			if node == nil {
				continue
			}
			cur := in
			if t, ok := f.String(ctx, node["T"]); ok {
				part := pdfobj.DecodeText(t)
				if cur.name != "" {
					cur.name += "."
				}
				cur.name += part
			}
			if ft := f.Name(ctx, node["FT"]); ft != "" {
				cur.kind = ft
			}
			if v := resolveQuiet(ctx, f, node["V"]); v != nil {
				cur.value = v
			}
			if ff, ok := f.Int(ctx, node["Ff"]); ok {
				cur.flags = ff
			}
			// Kids with names are fields below this one. Kids without are
			// the widgets that show this field on the pages.
			var below pdfobj.Array
			for _, kid := range f.Array(ctx, node["Kids"]) {
				if kd := f.Dict(ctx, kid); kd != nil && kd["T"] != nil {
					below = append(below, kid)
				}
			}
			if len(below) > 0 && depth < maxFormDepth {
				walk(below, cur, depth+1)
				continue
			}
			if cur.name == "" {
				continue
			}
			field := FormField{Name: cur.name}
			switch cur.kind {
			case "Tx":
				field.Kind = "text"
			case "Ch":
				field.Kind = "choice"
			case "Sig":
				field.Kind = "signature"
			case "Btn":
				switch {
				case cur.flags&(1<<16) != 0:
					field.Kind = "button"
				case cur.flags&(1<<15) != 0:
					field.Kind = "radio"
				default:
					field.Kind = "checkbox"
				}
			}
			switch v := cur.value.(type) {
			case pdfobj.String:
				field.Value = pdfobj.DecodeText(v)
			case pdfobj.Name:
				field.Value = string(v)
			case pdfobj.Array:
				var parts []string
				for _, x := range v {
					if s, ok := f.String(ctx, x); ok {
						parts = append(parts, pdfobj.DecodeText(s))
					}
				}
				field.Value = strings.Join(parts, ", ")
			}
			out = append(out, field)
		}
	}
	walk(f.Array(ctx, form["Fields"]), inherited{}, 0)
	return out, ctx.Err()
}
