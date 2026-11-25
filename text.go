// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package unpdf

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/gomcpgo/unpdf/internal/content"
	"github.com/gomcpgo/unpdf/internal/font"
	"github.com/gomcpgo/unpdf/internal/pdfobj"
	"github.com/gomcpgo/unpdf/internal/reader"
)

// Warning codes for text extraction.
const (
	// WarnUnmappedGlyphs: a font gave no Unicode mapping for some of its
	// glyphs, which appear in the text as U+FFFD. There is one warning per
	// font, saying how many glyphs on how many pages.
	WarnUnmappedGlyphs = "unmapped-glyphs"
	// WarnGlyphsWithoutText: a font's ToUnicode map leaves some glyphs out,
	// and they produce no text. That is normal for complex scripts, where
	// the first glyph of a cluster carries the text of all of them, but it
	// can also mean the map is incomplete.
	WarnGlyphsWithoutText = "glyphs-without-text"
)

// glyphCount tallies the glyphs of one font, on one page, that had no text.
type glyphCount struct {
	unmapped, missing int
}

// glyphWarnings turns the tallies into one warning per font and kind.
func (d *Document) glyphWarnings() []Warning {
	type row struct {
		name              string
		unmapped, missing int
		unmappedPages     int
		missingPages      int
	}
	var rows []row
	for fnt, pages := range d.glyphStats {
		r := row{name: fnt.Name}
		if r.name == "" {
			r.name = "(unnamed)"
		}
		for _, c := range pages {
			if c.unmapped > 0 {
				r.unmapped += c.unmapped
				r.unmappedPages++
			}
			if c.missing > 0 {
				r.missing += c.missing
				r.missingPages++
			}
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].name != rows[j].name {
			return rows[i].name < rows[j].name
		}
		return rows[i].unmapped+rows[i].missing > rows[j].unmapped+rows[j].missing
	})
	var out []Warning
	for _, r := range rows {
		if r.unmapped > 0 {
			out = append(out, Warning{WarnUnmappedGlyphs, fmt.Sprintf(
				"font %s: %d glyphs on %d pages have no Unicode mapping and appear as U+FFFD", r.name, r.unmapped, r.unmappedPages)})
		}
		if r.missing > 0 {
			out = append(out, Warning{WarnGlyphsWithoutText, fmt.Sprintf(
				"font %s: %d glyphs on %d pages are left out of its ToUnicode map and produce no text", r.name, r.missing, r.missingPages)})
		}
	}
	return out
}

const defaultMaxPageGlyphs = 500_000

// pageContent is what interpreting a page's content stream yields.
type pageContent struct {
	glyphs []content.Glyph
	images []content.Image
}

// contentBytes returns the page's content streams, decoded and joined. A
// page may split its content over several streams at any token boundary.
func (p *Page) contentBytes(ctx context.Context) []byte {
	f := p.doc.f
	var streams []*pdfobj.Stream
	switch v := resolveQuiet(ctx, f, p.dict["Contents"]).(type) {
	case *pdfobj.Stream:
		streams = append(streams, v)
	case pdfobj.Array:
		for _, item := range v {
			if st := f.Stream(ctx, item); st != nil {
				streams = append(streams, st)
			}
		}
	}
	var buf bytes.Buffer
	for _, st := range streams {
		data, err := f.DecodeStream(ctx, st)
		if err != nil {
			if ctx.Err() == nil {
				f.Warn(WarnStreamData, "page %d: a content stream could not be read: %v", p.Number, err)
			}
			continue
		}
		if int64(buf.Len())+int64(len(data)) > p.doc.maxStream {
			f.Warn(WarnLimit, "page %d: content beyond %d bytes was ignored", p.Number, p.doc.maxStream)
			break
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

func resolveQuiet(ctx context.Context, f *reader.File, o pdfobj.Object) pdfobj.Object {
	v, err := f.Resolve(ctx, o)
	if err != nil {
		return nil
	}
	return v
}

// interpret runs the page's content stream and collects its glyphs and
// images.
func (p *Page) interpret(ctx context.Context) (*pageContent, error) {
	d := p.doc
	pc := &pageContent{}
	counts := map[*font.Font]glyphCount{}
	truncated := false
	sink := content.Sink{
		Glyph: func(g *content.Glyph) bool {
			if len(pc.glyphs) >= d.maxPageGlyphs {
				truncated = true
				return false
			}
			if !g.Mapped || g.Missing {
				c := counts[g.Font]
				if g.Missing {
					c.missing++
				} else {
					c.unmapped++
				}
				counts[g.Font] = c
			}
			pc.glyphs = append(pc.glyphs, *g)
			return true
		},
		Image: func(img content.Image) {
			if len(pc.images) < 10_000 {
				pc.images = append(pc.images, img)
			}
		},
	}
	if d.fonts == nil {
		d.fonts = font.NewCache(d.f)
	}
	in := content.New(d.f, d.fonts, sink)
	res := d.f.Dict(ctx, p.resources)
	if err := in.Run(ctx, p.contentBytes(ctx), res, content.Identity); err != nil {
		return nil, err
	}
	if err := p.annotations(ctx, in, res, pc); err != nil {
		return nil, err
	}
	if truncated {
		d.f.Warn(WarnLimit, "page %d: text beyond %d glyphs was ignored", p.Number, d.maxPageGlyphs)
	}
	// Record the tallies per page, so that reading a page twice does not
	// count its glyphs twice.
	for fnt, c := range counts {
		if d.glyphStats == nil {
			d.glyphStats = map[*font.Font]map[int]glyphCount{}
		}
		if d.glyphStats[fnt] == nil {
			d.glyphStats[fnt] = map[int]glyphCount{}
		}
		d.glyphStats[fnt][p.Number] = c
	}
	return pc, nil
}

// maxAnnotations caps the annotations looked at on one page.
const maxAnnotations = 5000

// annotations adds the text of the annotations that show text of their own:
// form fields with their filled-in values, free text typed onto the page,
// and stamps. This text is part of what a reader sees but is not in the
// page's content stream.
func (p *Page) annotations(ctx context.Context, in *content.Interp, pageRes pdfobj.Dict, pc *pageContent) error {
	f := p.doc.f
	annots := f.Array(ctx, p.dict["Annots"])
	if len(annots) > maxAnnotations {
		annots = annots[:maxAnnotations]
	}
	for _, a := range annots {
		if err := ctx.Err(); err != nil {
			return err
		}
		annot := f.Dict(ctx, a)
		switch f.Name(ctx, annot["Subtype"]) {
		case "Widget", "FreeText", "Stamp":
		default:
			continue
		}
		// Hidden (bit 2) and NoView (bit 6) annotations are not displayed.
		if flags, _ := f.Int(ctx, annot["F"]); flags&(1<<1|1<<5) != 0 {
			continue
		}
		rect, ok := p.doc.rect(ctx, annot["Rect"])
		if !ok {
			continue
		}
		before := len(pc.glyphs)
		if err := p.appearance(ctx, in, pageRes, annot, rect); err != nil {
			return err
		}
		// A form field whose appearance shows no text still has a value:
		// viewers draw it themselves when the file asks them to.
		if len(pc.glyphs) == before && len(pc.glyphs) < p.doc.maxPageGlyphs {
			if value := p.fieldValue(ctx, annot); value != "" {
				size := math.Min(rect.Height()*0.7, 12)
				pc.glyphs = append(pc.glyphs, content.Glyph{
					Text: value, Mapped: true, MCID: -1,
					X: rect.X0 + 2, Y: rect.Y0 + (rect.Height()-size)/2, DX: rect.Width() - 4, Size: size,
				})
			}
		}
	}
	return nil
}

// appearance interprets an annotation's normal appearance stream, placed on
// the page where the annotation is.
func (p *Page) appearance(ctx context.Context, in *content.Interp, pageRes, annot pdfobj.Dict, rect Rect) error {
	f := p.doc.f
	// The normal appearance is a stream, or one stream per state with /AS
	// naming the current state.
	normal := resolveQuiet(ctx, f, f.Dict(ctx, annot["AP"])["N"])
	if states, ok := normal.(pdfobj.Dict); ok {
		normal = resolveQuiet(ctx, f, states[f.Name(ctx, annot["AS"])])
	}
	st, ok := normal.(*pdfobj.Stream)
	if !ok {
		return nil
	}
	data, err := f.DecodeStream(ctx, st)
	if err != nil {
		return ctx.Err()
	}
	res := pageRes
	if d := f.Dict(ctx, st.Dict["Resources"]); d != nil {
		res = d
	}

	// The appearance's bounding box, taken through its own matrix, is
	// fitted onto the annotation rectangle.
	matrix := content.Identity
	if arr := f.Array(ctx, st.Dict["Matrix"]); len(arr) >= 6 {
		for i := range matrix {
			matrix[i], _ = f.Number(ctx, arr[i])
		}
	}
	fit := content.Matrix{1, 0, 0, 1, rect.X0, rect.Y0}
	if bbox, ok := p.doc.rect(ctx, st.Dict["BBox"]); ok {
		xs := [4]float64{bbox.X0, bbox.X1, bbox.X0, bbox.X1}
		ys := [4]float64{bbox.Y0, bbox.Y0, bbox.Y1, bbox.Y1}
		x0, y0, x1, y1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
		for i := range xs {
			x := matrix[0]*xs[i] + matrix[2]*ys[i] + matrix[4]
			y := matrix[1]*xs[i] + matrix[3]*ys[i] + matrix[5]
			x0, y0, x1, y1 = math.Min(x0, x), math.Min(y0, y), math.Max(x1, x), math.Max(y1, y)
		}
		if x1 > x0 && y1 > y0 {
			sx, sy := rect.Width()/(x1-x0), rect.Height()/(y1-y0)
			fit = content.Matrix{sx, 0, 0, sy, rect.X0 - x0*sx, rect.Y0 - y0*sy}
		}
	}
	return in.Run(ctx, data, res, matrix.Mul(fit))
}

// fieldValue returns the text value of a text or choice form field. The
// value and the field type may be set on the widget or on a field above it.
func (p *Page) fieldValue(ctx context.Context, annot pdfobj.Dict) string {
	f := p.doc.f
	var fieldType pdfobj.Name
	var value pdfobj.Object
	for node, depth := annot, 0; node != nil && depth < 16; depth++ {
		if fieldType == "" {
			fieldType = f.Name(ctx, node["FT"])
		}
		if value == nil {
			value = resolveQuiet(ctx, f, node["V"])
		}
		node = f.Dict(ctx, node["Parent"])
	}
	if fieldType != "Tx" && fieldType != "Ch" {
		return ""
	}
	if s, ok := value.(pdfobj.String); ok {
		return strings.TrimSpace(pdfobj.DecodeText(s))
	}
	return ""
}

// Text returns the text of the page.
//
// The text comes out in the order the page draws it, with a space wherever
// there is a gap between glyphs on a line and a line break wherever the
// baseline moves. Pages laid out in columns are usually drawn column by
// column, but nothing guarantees it; layout analysis that works out the
// reading order is not implemented yet.
//
// Glyphs whose font gives no Unicode mapping appear as U+FFFD, and are
// counted in a warning.
func (p *Page) Text(ctx context.Context) (string, error) {
	pc, err := p.interpret(ctx)
	if err != nil {
		return "", publicErr(err)
	}
	return plainText(p.visible(pc.glyphs)), nil
}

// visible drops the glyphs a reader of the page would not see as text of
// their own: those outside the crop box, such as printer's marks and file
// identifiers in the trim area, and those drawn a second time on top of
// themselves to fake a bold face or a shadow.
func (p *Page) visible(glyphs []content.Glyph) []content.Glyph {
	// The grid step is part of the key: it differs between font sizes, and
	// cells of different grids must not be compared.
	type cell struct {
		text string
		step float64
		x, y int
	}
	seen := make(map[cell]struct{}, len(glyphs))
	box := p.CropBox
	out := glyphs[:0]
	for _, g := range glyphs {
		// Half a glyph of tolerance: text set right at the edge stays.
		margin := g.Size / 2
		if g.X < box.X0-margin || g.X > box.X1+margin || g.Y < box.Y0-margin || g.Y > box.Y1+margin {
			continue
		}
		if g.Text != "" && g.Text != " " {
			// Two glyphs with the same text that sit almost on top of each
			// other are one glyph drawn twice. "Almost" is measured against
			// the glyph's own advance, because two real occurrences of a
			// character are at least one advance apart, however the text is
			// scaled. Look in the grid cell the glyph falls in and in its
			// neighbours.
			q := g.Size / 10
			if adv := math.Hypot(g.DX, g.DY); adv > 0 {
				q = math.Min(q, adv/6)
			}
			q = math.Max(q, 0.01)
			cx, cy := int(math.Floor(g.X/q)), int(math.Floor(g.Y/q))
			dup := false
			for dx := -1; dx <= 1 && !dup; dx++ {
				for dy := -1; dy <= 1 && !dup; dy++ {
					_, dup = seen[cell{g.Text, q, cx + dx, cy + dy}]
				}
			}
			if dup {
				continue
			}
			seen[cell{g.Text, q, cx, cy}] = struct{}{}
		}
		out = append(out, g)
	}
	return out
}

// spaceGap is the gap between two glyphs on a line, as a fraction of the
// font size, beyond which they belong to different words. Word spaces are a
// quarter of an em or more before justification squeezes them; kerning and
// letter fit stay well under a tenth.
const spaceGap = 0.12

// plainText joins glyphs into lines by looking at how each glyph sits
// relative to the one before it.
func plainText(glyphs []content.Glyph) string {
	var b strings.Builder
	var prev *content.Glyph
	endsWithSpace := true // nothing to put a space after yet
	for i := range glyphs {
		g := &glyphs[i]
		text := g.Text
		if !g.Mapped {
			text = "�"
		}
		if text == "" {
			continue // a glyph whose text was given by an earlier one
		}
		if prev != nil {
			// Measure in the frame of the previous glyph's baseline.
			ux, uy := prev.DX, prev.DY
			if n := math.Hypot(ux, uy); n > 0 {
				ux, uy = ux/n, uy/n
			} else {
				ux, uy = 1, 0
			}
			along := (g.X-prev.X-prev.DX)*ux + (g.Y-prev.Y-prev.DY)*uy
			across := (g.Y-prev.Y)*ux - (g.X-prev.X)*uy
			size := math.Max(prev.Size, g.Size)
			if size <= 0 {
				size = 1
			}
			switch {
			case math.Abs(across) > 0.6*size || along < -1.5*size:
				// A different baseline, or a jump back along the same one.
				trimTrailingSpace(&b)
				b.WriteByte('\n')
				endsWithSpace = true
			case along > spaceGap*size && !endsWithSpace && !strings.HasPrefix(text, " "):
				b.WriteByte(' ')
			}
		}
		b.WriteString(text)
		endsWithSpace = strings.HasSuffix(text, " ")
		prev = g
	}
	trimTrailingSpace(&b)
	return b.String()
}

func trimTrailingSpace(b *strings.Builder) {
	s := b.String()
	if t := strings.TrimRight(s, " "); len(t) != len(s) {
		b.Reset()
		b.WriteString(t)
	}
}
