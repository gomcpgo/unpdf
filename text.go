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
	"github.com/gomcpgo/unpdf/internal/layout"
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

// maxPageRules caps the ruling lines kept for one page.
const maxPageRules = 4000

// pageContent is what interpreting a page's content stream yields.
type pageContent struct {
	glyphs []content.Glyph
	images []content.Image
	// inlineBytes counts the data kept for inline images, and inlineDropped
	// reports that some was not kept.
	inlineBytes   int
	inlineDropped bool
	// rules are the straight lines drawn across or down the page.
	rules []content.Rule
	// shown counts the glyphs with text that a reader can see.
	shown int
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
	if p.content != nil {
		return p.content, nil
	}
	d := p.doc
	data := p.contentBytes(ctx)
	// A glyph takes at least a few bytes of content stream; sizing the
	// slice from that saves growing it many times over.
	pc := &pageContent{glyphs: make([]content.Glyph, 0, min(len(data)/8+16, d.maxPageGlyphs))}
	counts := map[*font.Font]glyphCount{}
	truncated := false
	inAnnotations := false
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
			if !g.Invisible && !g.Clipped && strings.TrimSpace(g.Text) != "" {
				pc.shown++
			}
			pc.glyphs = append(pc.glyphs, *g)
			if inAnnotations {
				// An annotation's marked content is numbered apart from
				// the page's.
				pc.glyphs[len(pc.glyphs)-1].MCID = -1
			}
			return true
		},
		Image: func(img content.Image) {
			if len(pc.images) >= maxPageImages {
				return
			}
			if img.Inline && img.Data != nil {
				// The data is part of the content stream, which is let go.
				if pc.inlineBytes+len(img.Data) > maxInlineImageBytes {
					img.Data, pc.inlineDropped = nil, true
				} else {
					pc.inlineBytes += len(img.Data)
					img.Data = bytes.Clone(img.Data)
				}
			}
			pc.images = append(pc.images, img)
		},
		Rule: func(r content.Rule) {
			if len(pc.rules) < maxPageRules {
				pc.rules = append(pc.rules, r)
			}
		},
	}
	if d.fonts == nil {
		d.fonts = font.NewCache(d.f)
	}
	in := content.New(d.f, d.fonts, sink)
	res := d.f.Dict(ctx, p.resources)
	if err := in.Run(ctx, data, res, content.Identity); err != nil {
		return nil, err
	}
	inAnnotations = true
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
	p.content = pc
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

// Text returns the text of the page in reading order: its blocks, as
// Blocks returns them, separated by blank lines, with one line of text per
// line on the page. Words hyphenated at a line end are joined.
//
// Glyphs whose font gives no Unicode mapping appear as U+FFFD, and are
// counted in a warning.
func (p *Page) Text(ctx context.Context) (string, error) {
	blocks, err := p.layout(ctx, true)
	if err != nil {
		return "", err
	}
	return layout.Text(blocks), nil
}

// visible converts the page's glyphs to display space, leaving out those a
// reader of the page would not see:
//
//   - those outside the crop box, such as printer's marks and file
//     identifiers in the trim area, and those outside the clipping path;
//   - invisible text, unless nearly all the page's text is invisible, which
//     is how an OCR layer over a scanned page looks. There the hidden text
//     is the content.
//
// Text drawn twice on top of itself is dealt with later, word by word.
//
// With tags, each glyph is given the number of the block that the
// document's structure tree puts it in. The second result reports whether
// the tags cover the page's text well enough to lay it out by them; when
// they do not, no glyph is given a block.
func (p *Page) visible(glyphs []content.Glyph, tags *pageTags) ([]layout.Glyph, bool) {
	shown, hidden := 0, 0
	for i := range glyphs {
		if g := &glyphs[i]; g.Text != "" && g.Text != " " || !g.Mapped {
			if g.Invisible {
				hidden++
			} else {
				shown++
			}
		}
	}
	keepHidden := shown*10 < hidden

	box := p.CropBox
	tagged, loose := 0, 0
	out := make([]layout.Glyph, 0, len(glyphs))
	for i := range glyphs {
		g := &glyphs[i]
		if g.Clipped || (g.Invisible && !keepHidden) {
			continue
		}
		// Half a glyph of tolerance: text set right at the edge stays.
		margin := g.Size / 2
		if g.X < box.X0-margin || g.X > box.X1+margin || g.Y < box.Y0-margin || g.Y > box.Y1+margin {
			continue
		}
		out = append(out, layout.Glyph{})
		lg := &out[len(out)-1]
		p.display(g, lg)
		if tags == nil || g.Artifact {
			continue
		}
		if g.MCID >= 0 && g.MCID < len(tags.group) {
			lg.Group = int(tags.group[g.MCID])
		}
		if strings.TrimSpace(lg.Text) != "" {
			if lg.Group != 0 {
				tagged++
			} else {
				loose++
			}
		}
	}
	// Tags that leave out much of the page's text are not a sound basis
	// for its layout. Text that a tagged document leaves untagged should
	// be an artifact, and says so.
	if tagged == 0 || loose*4 > tagged {
		for i := range out {
			out[i].Group = 0
		}
		return out, false
	}
	return out, true
}
