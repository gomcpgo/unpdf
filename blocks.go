// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package unpdf

import (
	"context"
	"math"
	"strings"
	"unicode"

	"github.com/gomcpgo/unpdf/internal/content"
	"github.com/gomcpgo/unpdf/internal/layout"
)

// Box is a rectangle on the page as displayed: in points, measured from the
// top-left corner of the page after cropping and rotation, with Y growing
// downward. X0,Y0 is the top-left corner and X1,Y1 the bottom-right.
type Box struct {
	X0, Y0, X1, Y1 float64
}

// Word is a run of characters with no space between them.
type Word struct {
	Text string
	Box  Box
	// Font is the name of the word's font, without any subset tag, and
	// Size its size in points. Bold and Italic are set where the font says
	// so or its name gives it away.
	Font         string
	Size         float64
	Bold, Italic bool
}

// Line is a row of words on one baseline, in reading order.
type Line struct {
	Words []Word
	Box   Box
	// RTL reports a line of right-to-left script, such as Arabic or Hebrew.
	// Its words are in logical order: the first word is the rightmost on
	// the page.
	RTL bool
}

// Text returns the line's words separated by single spaces.
func (l Line) Text() string {
	words := make([]string, len(l.Words))
	for i, w := range l.Words {
		words[i] = w.Text
	}
	return strings.Join(words, " ")
}

// Block is a group of lines that belong together: a paragraph, a heading, a
// list item, the row of a table.
type Block struct {
	Lines []Line
	Box   Box
	// Angle is the direction of the block's text in degrees, clockwise from
	// upright: 0 for ordinary text, 90 for text that reads down the page.
	Angle int
	// Furniture reports that the block is a running head, a running foot
	// or a page number: text that repeats in the same place from page to
	// page, or that the document itself declares to be an artifact. It is
	// still returned, so that the caller decides whether to keep it.
	Furniture bool
	// Kind says whether the block is a heading, a list item or a paragraph.
	// A tagged document says so itself. Otherwise it is worked out from the
	// layout: a heading is a short block set larger or bolder than the body
	// text, a list item starts with a bullet or a number.
	Kind BlockKind
	// Level is the rank of a heading, 1 to 6 with 1 the largest, or the
	// depth of a list item, 0 for the outermost.
	Level int
	// Marker is the bullet or number of a list item, as printed. It is also
	// the first word of the item's first line. It is empty for an item
	// whose bullet is drawn and not written.
	Marker string

	// tagged reports that the block is one the document's structure tree
	// defines, and fixed that the tree also says what kind it is.
	tagged, fixed bool
}

// Text returns the text of the block, one line of the page per line of
// text. A word hyphenated at the end of a line is joined to its
// continuation when the hyphen is clearly there for the line break: a soft
// hyphen, or a hyphen between two lower-case letters.
func (b Block) Text() string {
	lines := make([][]string, len(b.Lines))
	rtl := make([]bool, len(b.Lines))
	for i, l := range b.Lines {
		lines[i] = make([]string, len(l.Words))
		for j, w := range l.Words {
			lines[i][j] = w.Text
		}
		rtl[i] = l.RTL
	}
	return layout.JoinLines(lines, rtl)
}

// Blocks returns the text of the page as blocks of lines of words, in
// reading order, each with its position on the page and its kind: heading,
// list item or paragraph.
//
// Reading order is worked out from the layout. A page set in columns is
// read one column after the other; text that does not form columns of
// prose, such as a table, is read row by row. Text in other directions,
// such as a label running up the side, comes after the main text.
func (p *Page) Blocks(ctx context.Context) ([]Block, error) {
	if p.classified {
		return p.public, nil
	}
	laid, err := p.layout(ctx, true)
	if err != nil {
		return nil, err
	}
	model, err := p.doc.style(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Block, len(laid))
	for i, b := range laid {
		out[i] = Block{Box: Box(b.Box), Angle: b.Angle, Furniture: b.Furniture, Lines: make([]Line, len(b.Lines))}
		if p.tags != nil && b.Group > 0 && b.Group <= len(p.tags.groups) {
			g := &p.tags.groups[b.Group-1]
			out[i].tagged = true
			kind := g.kind
			if kind == Heading && !headingSized(b) {
				// Tagged as a heading, but a paragraph by its length: some
				// documents tag text as a heading to make it look like one.
				kind = Paragraph
			}
			if kind != Paragraph || (g.cell != 0 && !p.looseCells[g.cell]) {
				// A table cell is fixed too: its bold header is no heading.
				out[i].Kind, out[i].Level, out[i].fixed = kind, g.level, true
			}
		}
		for j, l := range b.Lines {
			line := Line{Box: Box(l.Box), RTL: l.RTL, Words: make([]Word, len(l.Words))}
			for k, w := range l.Words {
				line.Words[k] = Word{Text: w.Text, Box: Box(w.Box), Font: w.Font, Size: w.Size, Bold: w.Bold, Italic: w.Italic}
			}
			out[i].Lines[j] = line
		}
	}
	titles, err := p.doc.outlineTitles(ctx, p.Number)
	if err != nil {
		return nil, err
	}
	for i := range out {
		// The marker of a list item the document tags: its first word, if
		// that is a bullet or a number.
		if b := &out[i]; b.fixed && b.Kind == ListItem && len(b.Lines) > 0 && len(b.Lines[0].Words) > 0 {
			if _, _, ok := listMarker(b.Lines[0].Words[0].Text); ok {
				b.Marker = b.Lines[0].Words[0].Text
			}
		}
	}
	p.public, p.classified = joinHeadings(classify(out, model, titles)), true
	return p.public, nil
}

// headingSized reports whether a block is short enough to be a heading.
func headingSized(b layout.Block) bool {
	chars := 0
	for _, l := range b.Lines {
		for _, w := range l.Words {
			chars += len(w.Text) + 1
		}
	}
	return len(b.Lines) <= maxHeadingLines+1 && chars <= maxHeadingChars+100
}

// joinHeadings joins the lines of a heading that the document tags as
// separate paragraphs, one per line, as page-layout programs do. They are
// two headings found by their looks, of the same level and size, one right
// under the other.
func joinHeadings(blocks []Block) []Block {
	out := blocks[:0]
	for _, b := range blocks {
		if n := len(out); n > 0 && b.Kind == Heading && b.tagged && !b.fixed && len(b.Lines) > 0 && len(b.Lines[0].Words) > 0 {
			prev := &out[n-1]
			if prev.Kind == Heading && prev.tagged && !prev.fixed && prev.Level == b.Level && prev.Angle == b.Angle &&
				len(prev.Lines)+len(b.Lines) <= maxHeadingLines && len(prev.Lines[0].Words) > 0 {
				size := b.Lines[0].Words[0].Size
				gap := b.Box.Y0 - prev.Box.Y1
				if math.Abs(prev.Lines[0].Words[0].Size-size) <= 0.5 && gap > -0.5*size && gap < 0.8*size {
					prev.Lines = append(prev.Lines[:len(prev.Lines):len(prev.Lines)], b.Lines...)
					prev.Box.X0, prev.Box.Y0 = math.Min(prev.Box.X0, b.Box.X0), math.Min(prev.Box.Y0, b.Box.Y0)
					prev.Box.X1, prev.Box.Y1 = math.Max(prev.Box.X1, b.Box.X1), math.Max(prev.Box.Y1, b.Box.Y1)
					continue
				}
			}
		}
		out = append(out, b)
	}
	return out
}

// layout interprets the page and analyses its layout. With furniture set,
// lines that repeat from page to page are marked, which means reading the
// two pages before and the two after as well.
func (p *Page) layout(ctx context.Context, furniture bool) ([]layout.Block, error) {
	if !p.laidOut {
		pc, err := p.interpret(ctx)
		if err != nil {
			return nil, publicErr(err)
		}
		tags := p.readTags(ctx)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if glyphs, ok := p.visible(pc.glyphs, tags); ok {
			// The document says which text belongs together and in what
			// order: only words and lines are left to work out.
			blocks := layout.AnalyzeTagged(glyphs)
			p.tags = tags
			p.structTables = p.taggedTables(blocks)
			p.blocks = p.foldRows(blocks)
		} else {
			p.blocks = layout.Analyze(glyphs)
		}
		p.laidOut = true
		// The glyphs have served their purpose; a page has thousands.
		pc.glyphs = nil
	}
	if !furniture {
		return p.blocks, nil
	}
	if !p.furnished {
		_, height := p.Size()
		test, err := p.doc.furnitureTest(ctx, p.Number, height)
		if err != nil {
			return nil, err
		}
		p.marked = p.blocks
		if test != nil {
			p.marked = layout.Mark(p.blocks, test)
		}
		p.furnished = true
	}
	return p.marked, nil
}

// display converts a glyph from page space to display space: the crop box
// moved to the origin, the page rotation applied, and Y flipped to grow
// downward.
func (p *Page) display(g *content.Glyph, out *layout.Glyph) {
	box := p.CropBox
	w, h := box.Width(), box.Height()
	x, y := g.X-box.X0, g.Y-box.Y0
	ux, uy := g.UX, g.UY
	if ux == 0 && uy == 0 {
		ux = 1
	}
	out.Text, out.Advance, out.Size, out.Artifact = g.Text, math.Hypot(g.DX, g.DY), g.Size, g.Artifact
	if !g.Mapped {
		out.Text = string(unicode.ReplacementChar)
	} else if strings.IndexFunc(out.Text, unicode.IsControl) >= 0 {
		// A font may map a glyph to a control character, a backspace to
		// pull a row of dots back, say. It is not text.
		out.Text = strings.Map(func(r rune) rune {
			switch {
			case r == '\t' || r == '\n' || r == '\r':
				return ' '
			case unicode.IsControl(r):
				return -1
			}
			return r
		}, out.Text)
	}
	if g.Font != nil {
		out.Font, out.Bold, out.Italic = g.Font.Name, g.Font.Bold, g.Font.Italic
	}
	switch p.Rotate {
	case 90:
		out.X, out.Y, out.UX, out.UY = y, x, uy, ux
	case 180:
		out.X, out.Y, out.UX, out.UY = w-x, y, -ux, uy
	case 270:
		out.X, out.Y, out.UX, out.UY = h-y, w-x, -uy, -ux
	default:
		out.X, out.Y, out.UX, out.UY = x, h-y, ux, -uy
	}
}

// Scanned reports whether the page looks like a scan: an image covers most
// of it and it shows little or no text of its own. A scan with an OCR text
// layer counts too; its hidden text is what Text returns. A caller can send
// such a page to OCR or to a vision model.
func (p *Page) Scanned(ctx context.Context) (bool, error) {
	pc, err := p.interpret(ctx)
	if err != nil {
		return false, publicErr(err)
	}
	box := p.CropBox
	area := box.Width() * box.Height()
	if area <= 0 {
		return false, nil
	}
	// Scanners often store a page as a few strips, so add the images up.
	covered := 0.0
	for _, img := range pc.images {
		w := math.Min(img.X1, box.X1) - math.Max(img.X0, box.X0)
		h := math.Min(img.Y1, box.Y1) - math.Max(img.Y0, box.Y0)
		if w > 0 && h > 0 {
			covered += w * h
		}
	}
	if covered < 0.7*area {
		return false, nil
	}
	return pc.shown < 50, nil
}
