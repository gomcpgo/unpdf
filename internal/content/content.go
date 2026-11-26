// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

// Package content interprets page content streams far enough to know what
// text is shown and where: the graphics state stack, the text state, the
// text-showing operators, form XObjects and marked content.
//
// It does not render. Paths, colours and shadings are skipped; images are
// reported with their position, not drawn.
package content

import (
	"context"
	"errors"
	"io"
	"math"

	"github.com/gomcpgo/unpdf/internal/font"
	"github.com/gomcpgo/unpdf/internal/pdfobj"
	"github.com/gomcpgo/unpdf/internal/reader"
)

// Matrix is a transformation matrix [a b c d e f], mapping (x, y) to
// (a*x + c*y + e, b*x + d*y + f).
type Matrix [6]float64

// Identity is the matrix that changes nothing.
var Identity = Matrix{1, 0, 0, 1, 0, 0}

// Mul returns the matrix that applies m and then n.
func (m Matrix) Mul(n Matrix) Matrix {
	return Matrix{
		m[0]*n[0] + m[1]*n[2],
		m[0]*n[1] + m[1]*n[3],
		m[2]*n[0] + m[3]*n[2],
		m[2]*n[1] + m[3]*n[3],
		m[4]*n[0] + m[5]*n[2] + n[4],
		m[4]*n[1] + m[5]*n[3] + n[5],
	}
}

// Glyph is one glyph placed on the page.
type Glyph struct {
	// Text is the glyph's Unicode text. It is empty for a glyph with no
	// mapping (see Mapped) and for the later glyphs of a span whose text
	// was supplied by /ActualText (see Replaced).
	Text   string
	Mapped bool
	// Missing reports a glyph that its font's ToUnicode map leaves out; see
	// font.Glyph.
	Missing bool
	// X, Y is the glyph origin in page space: the point on the baseline
	// where the glyph starts.
	X, Y float64
	// DX, DY is the glyph's advance in page space, from its own width. The
	// baseline runs along this vector.
	DX, DY float64
	// UX, UY is the unit vector of the writing direction in page space:
	// along the baseline for horizontal text, down the column for vertical
	// text. Unlike DX, DY it is defined for glyphs of zero width too.
	UX, UY float64
	// Size is the font size in page space units.
	Size float64
	Font *font.Font
	// Invisible reports text rendering mode 3: text that is neither filled
	// nor stroked, as an OCR layer over a scanned image is.
	Invisible bool
	// MCID is the marked-content identifier that ties the glyph to the
	// structure tree, or -1.
	MCID int
	// Artifact reports that the glyph is inside marked content tagged as an
	// artifact: page furniture such as running heads and page numbers.
	Artifact bool
	// Replaced reports that the enclosing marked content has /ActualText.
	// The first glyph of the span carries that text; the rest carry none.
	Replaced bool
	// Clipped reports that the glyph lies outside the clipping path in
	// force, so it is not visible. Clipping paths are approximated by their
	// bounding boxes, which errs on the side of keeping text.
	Clipped bool
}

// Image is an image placed on the page.
type Image struct {
	// X0, Y0, X1, Y1 is the bounding box in page space of the whole image.
	X0, Y0, X1, Y1 float64
	// VX0, VY0, VX1, VY1 is the part of that box inside the clipping path,
	// which is the part that shows. Hidden reports that there is none.
	// Clipping paths are approximated by their bounding boxes, so this errs
	// on the side of showing more.
	VX0, VY0, VX1, VY1 float64
	Hidden             bool
	// Stream is the image XObject. It is nil for an inline image.
	Stream *pdfobj.Stream
	// Inline reports an image written into the content stream itself. Dict
	// is then its dictionary, with the abbreviations of inline images
	// written out and a colour space named in the resources looked up, and
	// Data its data, still encoded. Data is part of the content stream and
	// is only valid during the call to the sink: copy it to keep it.
	Inline bool
	Dict   pdfobj.Dict
	Data   []byte
}

// Rule is a horizontal or vertical line in page space. For a horizontal
// rule Y0 equals Y1; for a vertical one X0 equals X1.
type Rule struct {
	X0, Y0, X1, Y1 float64
}

// Sink receives what the interpreter finds. Either function may be nil.
type Sink struct {
	// Glyph is called for every glyph shown. Returning false stops the
	// interpretation. The Glyph is reused for the next call: copy it to
	// keep it.
	Glyph func(*Glyph) bool
	Image func(Image)
	// Rule is called for every straight line that runs across or down the
	// page: a stroked horizontal or vertical segment, or a filled rectangle
	// so thin that it is a line. These are the rulings of tables.
	Rule func(Rule)
}

const (
	maxFormDepth  = 24
	maxStateDepth = 256
	maxOperands   = 64
	maxErrors     = 500
)

// box is an axis-aligned rectangle in page space.
type box struct {
	x0, y0, x1, y1 float64
	set            bool
}

func (b *box) add(x, y float64) {
	if !b.set {
		*b = box{x, y, x, y, true}
		return
	}
	b.x0, b.y0 = math.Min(b.x0, x), math.Min(b.y0, y)
	b.x1, b.y1 = math.Max(b.x1, x), math.Max(b.y1, y)
}

type gstate struct {
	clip                 box // the clipping path's bounding box, if any
	ctm                  Matrix
	font                 *font.Font
	size                 float64
	tc, tw, th, tl, rise float64
	mode                 int64
}

type marked struct {
	mcid      int
	artifact  bool
	hasActual bool
	actual    string
	used      bool
}

type resources struct {
	dict  pdfobj.Dict
	fonts map[pdfobj.Name]*font.Font // fonts already looked up by name
}

// Interp interprets content streams for one document.
type Interp struct {
	f     *reader.File
	fonts *font.Cache
	sink  Sink

	ctx     context.Context
	gs      gstate
	stack   []gstate
	tm, tlm Matrix
	marks   []marked
	depth   int
	active  map[int]bool // form XObjects being executed, to cut recursion
	glyph   Glyph        // the glyph handed to the sink, reused
	path    box          // bounding box of the path under construction
	segs    [][4]float64 // straight segments of that path, in page space
	rects   [][4]float64 // rectangles of that path, as bounding boxes
	start   [2]float64   // first point of the current subpath
	cur     [2]float64   // current point
	hasCur  bool
	clipNow bool // W or W* seen: the path becomes the clip when it ends
	ops     int
	stopped bool
}

// New returns an interpreter that reads from f, loads fonts through fonts
// and reports to sink.
func New(f *reader.File, fonts *font.Cache, sink Sink) *Interp {
	return &Interp{f: f, fonts: fonts, sink: sink}
}

// Run interprets a content stream. res is its resource dictionary and ctm
// the initial transformation matrix. It returns an error only when ctx is
// cancelled; damaged content is skipped.
func (in *Interp) Run(ctx context.Context, data []byte, res pdfobj.Dict, ctm Matrix) error {
	in.ctx = ctx
	in.gs = gstate{ctm: ctm, th: 1}
	in.stack = in.stack[:0]
	in.tm, in.tlm = Identity, Identity
	in.marks = in.marks[:0]
	in.depth, in.ops, in.stopped = 0, 0, false
	in.path, in.clipNow = box{}, false
	in.segs, in.rects, in.hasCur = in.segs[:0], in.rects[:0], false
	in.active = map[int]bool{}
	in.run(data, &resources{dict: res})
	return ctx.Err()
}

func (in *Interp) run(data []byte, res *resources) {
	lim := pdfobj.Limits{MaxDepth: 32, MaxString: 1 << 24, MaxItems: 1 << 20}
	p := pdfobj.NewParser(pdfobj.NewBytesLexer(data, lim))
	operands := make([]pdfobj.Object, 0, 8)
	errs := 0
	for !in.stopped {
		if in.ops++; in.ops%2048 == 0 && in.ctx.Err() != nil {
			return
		}
		obj, op, err := p.ContentNext()
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return
		}
		if err != nil {
			if errs++; errs > maxErrors {
				return
			}
			operands = operands[:0]
			continue
		}
		if op == "" {
			if len(operands) >= maxOperands {
				operands = operands[:0]
			}
			operands = append(operands, obj)
			continue
		}
		in.exec(op, operands, p, res)
		operands = operands[:0]
	}
}

// num returns operand i as a finite number, or 0.
func num(args []pdfobj.Object, i int) float64 {
	if i < 0 || i >= len(args) {
		return 0
	}
	v, _ := pdfobj.Number(args[i])
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

func (in *Interp) exec(op string, args []pdfobj.Object, p *pdfobj.Parser, res *resources) {
	g := &in.gs
	n := len(args)
	switch op {
	// Graphics state.
	case "q":
		if len(in.stack) < maxStateDepth {
			in.stack = append(in.stack, *g)
		}
	case "Q":
		if k := len(in.stack); k > 0 {
			*g = in.stack[k-1]
			in.stack = in.stack[:k-1]
		}
	case "cm":
		if n >= 6 {
			m := Matrix{num(args, n-6), num(args, n-5), num(args, n-4), num(args, n-3), num(args, n-2), num(args, n-1)}
			g.ctm = m.Mul(g.ctm)
		}
	case "gs":
		if n >= 1 {
			name, _ := args[n-1].(pdfobj.Name)
			ext := in.f.Dict(in.ctx, in.f.Dict(in.ctx, res.dict["ExtGState"])[name])
			if fa := in.f.Array(in.ctx, ext["Font"]); len(fa) >= 2 {
				if fnt := in.fonts.Get(in.ctx, fa[0]); fnt != nil {
					g.font = fnt
					g.size, _ = in.f.Number(in.ctx, fa[1])
				}
			}
		}

	// Paths matter only as clipping regions. Track the bounding box of
	// the points; curves stay inside the hull of their control points.
	case "m":
		x, y := in.pathPoint(num(args, n-2), num(args, n-1))
		in.start, in.cur, in.hasCur = [2]float64{x, y}, [2]float64{x, y}, true
	case "l":
		x, y := in.pathPoint(num(args, n-2), num(args, n-1))
		in.lineTo(x, y)
	case "h":
		if in.hasCur {
			in.lineTo(in.start[0], in.start[1])
		}
	case "c":
		for i := 6; i >= 2; i -= 2 {
			x, y := in.pathPoint(num(args, n-i), num(args, n-i+1))
			in.cur = [2]float64{x, y} // a curve is no ruling: move without a segment
		}
	case "v", "y":
		for i := 4; i >= 2; i -= 2 {
			x, y := in.pathPoint(num(args, n-i), num(args, n-i+1))
			in.cur = [2]float64{x, y}
		}
	case "re":
		x, y, w, h := num(args, n-4), num(args, n-3), num(args, n-2), num(args, n-1)
		ax, ay := in.pathPoint(x, y)
		bx, by := in.pathPoint(x+w, y)
		cx, cy := in.pathPoint(x+w, y+h)
		dx, dy := in.pathPoint(x, y+h)
		if in.sink.Rule != nil && len(in.rects) < maxPathSegments {
			in.rects = append(in.rects, [4]float64{
				math.Min(math.Min(ax, bx), math.Min(cx, dx)), math.Min(math.Min(ay, by), math.Min(cy, dy)),
				math.Max(math.Max(ax, bx), math.Max(cx, dx)), math.Max(math.Max(ay, by), math.Max(cy, dy)),
			})
		}
		in.start, in.cur, in.hasCur = [2]float64{ax, ay}, [2]float64{ax, ay}, true
	case "W", "W*":
		in.clipNow = true
	case "n", "S", "s", "f", "F", "f*", "B", "B*", "b", "b*":
		if in.clipNow && in.path.set {
			if c := &g.clip; c.set {
				c.x0, c.y0 = math.Max(c.x0, in.path.x0), math.Max(c.y0, in.path.y0)
				c.x1, c.y1 = math.Min(c.x1, in.path.x1), math.Min(c.y1, in.path.y1)
			} else {
				*c = in.path
			}
		}
		if in.sink.Rule != nil && op != "n" {
			if op == "s" || op == "b" || op == "b*" {
				if in.hasCur {
					in.lineTo(in.start[0], in.start[1]) // these close the path first
				}
			}
			in.emitRules(op != "f" && op != "F" && op != "f*", op != "S" && op != "s")
		}
		in.path, in.clipNow = box{}, false
		in.segs, in.rects, in.hasCur = in.segs[:0], in.rects[:0], false

	// Text objects and text state.
	case "BT":
		in.tm, in.tlm = Identity, Identity
	case "ET":
	case "Tc":
		g.tc = num(args, n-1)
	case "Tw":
		g.tw = num(args, n-1)
	case "Tz":
		g.th = num(args, n-1) / 100
	case "TL":
		g.tl = num(args, n-1)
	case "Ts":
		g.rise = num(args, n-1)
	case "Tr":
		if n >= 1 {
			g.mode, _ = pdfobj.Int(args[n-1])
		}
	case "Tf":
		if n >= 2 {
			name, _ := args[n-2].(pdfobj.Name)
			g.font = in.font(res, name)
			g.size = num(args, n-1)
		}

	// Text positioning.
	case "Td":
		in.moveLine(num(args, n-2), num(args, n-1))
	case "TD":
		g.tl = -num(args, n-1)
		in.moveLine(num(args, n-2), num(args, n-1))
	case "Tm":
		if n >= 6 {
			in.tlm = Matrix{num(args, n-6), num(args, n-5), num(args, n-4), num(args, n-3), num(args, n-2), num(args, n-1)}
			in.tm = in.tlm
		}
	case "T*":
		in.moveLine(0, -g.tl)

	// Text showing.
	case "Tj":
		if n >= 1 {
			if s, ok := args[n-1].(pdfobj.String); ok {
				in.show(s)
			}
		}
	case "'":
		in.moveLine(0, -g.tl)
		if n >= 1 {
			if s, ok := args[n-1].(pdfobj.String); ok {
				in.show(s)
			}
		}
	case "\"":
		if n >= 3 {
			g.tw, g.tc = num(args, n-3), num(args, n-2)
			in.moveLine(0, -g.tl)
			if s, ok := args[n-1].(pdfobj.String); ok {
				in.show(s)
			}
		}
	case "TJ":
		if n >= 1 {
			arr, _ := args[n-1].(pdfobj.Array)
			for _, item := range arr {
				switch v := item.(type) {
				case pdfobj.String:
					in.show(v)
				case int64:
					in.advance(-float64(v) / 1000 * g.size)
				case float64:
					if !math.IsNaN(v) && !math.IsInf(v, 0) {
						in.advance(-v / 1000 * g.size)
					}
				}
				if in.stopped {
					return
				}
			}
		}

	// XObjects and inline images.
	case "Do":
		if n >= 1 {
			name, _ := args[n-1].(pdfobj.Name)
			in.xobject(in.f.Dict(in.ctx, res.dict["XObject"])[name], res)
		}
	case "BI":
		// The image dictionary follows as operands, then ID, the data, EI.
		var entries []pdfobj.Object
		for {
			obj, next, err := p.ContentNext()
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return
			}
			if next == "ID" {
				dict := in.inlineDict(entries, res)
				in.image(Image{Inline: true, Dict: dict, Data: p.InlineImage(in.inlineSize(dict))})
				break
			}
			if next == "EI" {
				in.image(Image{Inline: true, Dict: in.inlineDict(entries, res)})
				break
			}
			if err == nil && next == "" && len(entries) < maxOperands {
				entries = append(entries, obj)
			}
		}

	// Marked content.
	case "BMC":
		tag, _ := firstName(args)
		in.pushMark(tag, nil)
	case "BDC":
		tag, _ := firstName(args)
		var props pdfobj.Dict
		if n >= 2 {
			switch v := args[n-1].(type) {
			case pdfobj.Dict:
				props = v
			case pdfobj.Name:
				props = in.f.Dict(in.ctx, in.f.Dict(in.ctx, res.dict["Properties"])[v])
			}
		}
		in.pushMark(tag, props)
	case "EMC":
		if k := len(in.marks); k > 0 {
			in.marks = in.marks[:k-1]
		}
	}
}

func firstName(args []pdfobj.Object) (pdfobj.Name, bool) {
	if len(args) == 0 {
		return "", false
	}
	n, ok := args[0].(pdfobj.Name)
	return n, ok
}

func (in *Interp) pushMark(tag pdfobj.Name, props pdfobj.Dict) {
	if len(in.marks) >= maxStateDepth {
		return
	}
	m := marked{mcid: -1, artifact: tag == "Artifact"}
	if k := len(in.marks); k > 0 {
		m.mcid = in.marks[k-1].mcid
		m.artifact = m.artifact || in.marks[k-1].artifact
	}
	if props != nil {
		// Identifiers inside a form XObject are numbered apart from the
		// page's own, and would be mistaken for them.
		if id, ok := in.f.Int(in.ctx, props["MCID"]); ok && id >= 0 && id < 1<<31 && in.depth == 0 {
			m.mcid = int(id)
		}
		if s, ok := in.f.String(in.ctx, props["ActualText"]); ok {
			m.hasActual, m.actual = true, pdfobj.DecodeText(s)
		}
	}
	in.marks = append(in.marks, m)
}

// font finds a font by its resource name.
func (in *Interp) font(res *resources, name pdfobj.Name) *font.Font {
	if fnt, ok := res.fonts[name]; ok {
		return fnt
	}
	fnt := in.fonts.Get(in.ctx, in.f.Dict(in.ctx, res.dict["Font"])[name])
	if res.fonts == nil {
		res.fonts = map[pdfobj.Name]*font.Font{}
	}
	res.fonts[name] = fnt
	return fnt
}

// pathPoint adds a point, given in user space, to the current path.
func (in *Interp) pathPoint(x, y float64) (float64, float64) {
	m := in.gs.ctm
	px, py := m[0]*x+m[2]*y+m[4], m[1]*x+m[3]*y+m[5]
	if finite(px, py) {
		in.path.add(px, py)
	}
	return px, py
}

// maxPathSegments caps the segments and rectangles remembered for one path.
const maxPathSegments = 4096

// lineTo adds a straight segment from the current point.
func (in *Interp) lineTo(x, y float64) {
	if in.hasCur && in.sink.Rule != nil && len(in.segs) < maxPathSegments {
		in.segs = append(in.segs, [4]float64{in.cur[0], in.cur[1], x, y})
	}
	in.cur, in.hasCur = [2]float64{x, y}, true
}

const (
	// ruleSlack is how far a line may stray from horizontal or vertical,
	// in points, and still count as a ruling.
	ruleSlack = 0.75
	// ruleThickness is the greatest thickness of a filled rectangle that
	// is a line and not a box.
	ruleThickness = 3.0
	// ruleLength is the least length of a ruling.
	ruleLength = 4.0
)

// emitRules reports the rulings in the path just painted: its straight
// segments if it was stroked, and its thin rectangles if it was filled.
func (in *Interp) emitRules(stroked, filled bool) {
	emit := func(x0, y0, x1, y1 float64) {
		if !finite(x0, y0, x1, y1) {
			return
		}
		switch {
		case math.Abs(y1-y0) <= ruleSlack && math.Abs(x1-x0) >= ruleLength:
			y := (y0 + y1) / 2
			in.sink.Rule(Rule{math.Min(x0, x1), y, math.Max(x0, x1), y})
		case math.Abs(x1-x0) <= ruleSlack && math.Abs(y1-y0) >= ruleLength:
			x := (x0 + x1) / 2
			in.sink.Rule(Rule{x, math.Min(y0, y1), x, math.Max(y0, y1)})
		}
	}
	if stroked {
		for _, s := range in.segs {
			emit(s[0], s[1], s[2], s[3])
		}
		for _, r := range in.rects {
			emit(r[0], r[1], r[2], r[1])
			emit(r[0], r[3], r[2], r[3])
			emit(r[0], r[1], r[0], r[3])
			emit(r[2], r[1], r[2], r[3])
		}
	}
	if filled && !stroked {
		for _, r := range in.rects {
			w, h := r[2]-r[0], r[3]-r[1]
			switch {
			case h <= ruleThickness && w >= ruleLength:
				emit(r[0], (r[1]+r[3])/2, r[2], (r[1]+r[3])/2)
			case w <= ruleThickness && h >= ruleLength:
				emit((r[0]+r[2])/2, r[1], (r[0]+r[2])/2, r[3])
			}
		}
		// A thin rectangle drawn as four line segments.
		if len(in.segs) == 4 && len(in.rects) == 0 {
			b := box{}
			for _, s := range in.segs {
				b.add(s[0], s[1])
				b.add(s[2], s[3])
			}
			w, h := b.x1-b.x0, b.y1-b.y0
			switch {
			case h <= ruleThickness && w >= ruleLength:
				emit(b.x0, (b.y0+b.y1)/2, b.x1, (b.y0+b.y1)/2)
			case w <= ruleThickness && h >= ruleLength:
				emit((b.x0+b.x1)/2, b.y0, (b.x0+b.x1)/2, b.y1)
			}
		}
	}
}

// moveLine starts a new line, offset from the start of the current one.
func (in *Interp) moveLine(tx, ty float64) {
	in.tlm = Matrix{1, 0, 0, 1, tx, ty}.Mul(in.tlm)
	in.tm = in.tlm
}

// advance moves the text position along the writing direction by t, a
// distance in unscaled text space.
func (in *Interp) advance(t float64) {
	if in.gs.font != nil && in.gs.font.Vertical {
		in.tm = Matrix{1, 0, 0, 1, 0, t}.Mul(in.tm)
		return
	}
	in.tm = Matrix{1, 0, 0, 1, t * in.gs.th, 0}.Mul(in.tm)
}

// show places the glyphs of one string.
func (in *Interp) show(s []byte) {
	g := &in.gs
	if g.font == nil {
		return
	}
	// Find the innermost marked content that replaces its glyphs' text.
	var actual *marked
	mcid, artifact := -1, false
	if k := len(in.marks); k > 0 {
		mcid, artifact = in.marks[k-1].mcid, in.marks[k-1].artifact
		for i := k - 1; i >= 0; i-- {
			if in.marks[i].hasActual {
				actual = &in.marks[i]
				break
			}
		}
	}
	vertical := g.font.Vertical
	for len(s) > 0 {
		ch := g.font.Next(s)
		s = s[ch.Bytes:]

		m := in.tm.Mul(g.ctm)
		out := &in.glyph
		*out = Glyph{
			Text: ch.Text, Mapped: ch.Mapped, Missing: ch.Missing,
			X:    m[2]*g.rise + m[4],
			Y:    m[3]*g.rise + m[5],
			Size: math.Abs(g.size) * math.Hypot(m[2], m[3]),
			Font: g.font, Invisible: g.mode == 3,
			MCID: mcid, Artifact: artifact,
		}
		spacing := g.tc
		if ch.Space {
			spacing += g.tw
		}
		if vertical {
			w := ch.Width * g.size
			out.DX, out.DY = w*m[2], w*m[3]
			out.UX, out.UY = unit(-m[2], -m[3])
			in.tm = Matrix{1, 0, 0, 1, 0, w + spacing}.Mul(in.tm)
		} else {
			w := ch.Width * g.size * g.th
			out.DX, out.DY = w*m[0], w*m[1]
			out.UX, out.UY = unit(m[0], m[1])
			if g.size*g.th < 0 {
				out.UX, out.UY = -out.UX, -out.UY
			}
			in.tm = Matrix{1, 0, 0, 1, w + spacing*g.th, 0}.Mul(in.tm)
		}
		if c := g.clip; c.set {
			// Judge by the middle of the glyph, with some slack: clips are
			// often drawn tight around the text they frame.
			mx, my := out.X+out.DX/2, out.Y+out.DY/2
			slack := out.Size / 2
			out.Clipped = mx < c.x0-slack || mx > c.x1+slack || my < c.y0-slack || my > c.y1+slack
		}
		if actual != nil {
			out.Replaced, out.Mapped = true, true
			out.Text = ""
			if !actual.used {
				out.Text, actual.used = actual.actual, true
			}
		}
		if !finite(out.X, out.Y, out.DX, out.DY, out.Size) {
			continue
		}
		if in.sink.Glyph != nil && !in.sink.Glyph(out) {
			in.stopped = true
			return
		}
	}
}

// unit scales a vector to length one. A zero vector yields the direction of
// ordinary upright text.
func unit(x, y float64) (float64, float64) {
	n := math.Hypot(x, y)
	if n == 0 || math.IsNaN(n) || math.IsInf(n, 0) {
		return 1, 0
	}
	return x / n, y / n
}

func finite(vs ...float64) bool {
	for _, v := range vs {
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 1e9 {
			return false
		}
	}
	return true
}

// image reports an image drawn in the unit square of the current matrix.
func (in *Interp) image(img Image) {
	if in.sink.Image == nil {
		return
	}
	m := in.gs.ctm
	xs := [4]float64{m[4], m[0] + m[4], m[2] + m[4], m[0] + m[2] + m[4]}
	ys := [4]float64{m[5], m[1] + m[5], m[3] + m[5], m[1] + m[3] + m[5]}
	img.X0, img.Y0, img.X1, img.Y1 = xs[0], ys[0], xs[0], ys[0]
	for i := 1; i < 4; i++ {
		img.X0, img.X1 = math.Min(img.X0, xs[i]), math.Max(img.X1, xs[i])
		img.Y0, img.Y1 = math.Min(img.Y0, ys[i]), math.Max(img.Y1, ys[i])
	}
	if !finite(img.X0, img.Y0, img.X1, img.Y1) {
		return
	}
	img.VX0, img.VY0, img.VX1, img.VY1 = img.X0, img.Y0, img.X1, img.Y1
	if c := in.gs.clip; c.set {
		img.VX0, img.VY0 = math.Max(img.X0, c.x0), math.Max(img.Y0, c.y0)
		img.VX1, img.VY1 = math.Min(img.X1, c.x1), math.Min(img.Y1, c.y1)
		if img.VX1 <= img.VX0 || img.VY1 <= img.VY0 {
			img.Hidden = true
			img.VX0, img.VY0, img.VX1, img.VY1 = img.X0, img.Y0, img.X0, img.Y0
		}
	}
	in.sink.Image(img)
}

// The dictionary of an inline image may abbreviate its keys, the names of
// its filters and the names of the device colour spaces.
var (
	inlineKeys = map[pdfobj.Name]pdfobj.Name{
		"BPC": "BitsPerComponent", "CS": "ColorSpace", "D": "Decode", "DP": "DecodeParms", "F": "Filter",
		"H": "Height", "IM": "ImageMask", "I": "Interpolate", "W": "Width",
	}
	inlineFilters = map[pdfobj.Name]pdfobj.Name{
		"AHx": "ASCIIHexDecode", "A85": "ASCII85Decode", "LZW": "LZWDecode", "Fl": "FlateDecode",
		"RL": "RunLengthDecode", "CCF": "CCITTFaxDecode", "DCT": "DCTDecode",
	}
	inlineSpaces = map[pdfobj.Name]pdfobj.Name{
		"G": "DeviceGray", "RGB": "DeviceRGB", "CMYK": "DeviceCMYK", "I": "Indexed",
	}
)

// inlineDict builds the dictionary of an inline image from the keys and
// values between BI and ID, in the terms of an image XObject.
func (in *Interp) inlineDict(entries []pdfobj.Object, res *resources) pdfobj.Dict {
	dict := pdfobj.Dict{}
	for i := 0; i+1 < len(entries); i += 2 {
		key, ok := entries[i].(pdfobj.Name)
		if !ok {
			break
		}
		if full, ok := inlineKeys[key]; ok {
			key = full
		}
		dict[key] = entries[i+1]
	}
	full := func(o pdfobj.Object, names map[pdfobj.Name]pdfobj.Name) pdfobj.Object {
		if name, ok := o.(pdfobj.Name); ok {
			if long, ok := names[name]; ok {
				return long
			}
		}
		return o
	}
	switch v := dict["Filter"].(type) {
	case pdfobj.Name:
		dict["Filter"] = full(v, inlineFilters)
	case pdfobj.Array:
		filters := make(pdfobj.Array, len(v))
		for i, name := range v {
			filters[i] = full(name, inlineFilters)
		}
		dict["Filter"] = filters
	}
	switch v := dict["ColorSpace"].(type) {
	case pdfobj.Name:
		if long, ok := inlineSpaces[v]; ok {
			dict["ColorSpace"] = long
		} else if named := in.f.Dict(in.ctx, res.dict["ColorSpace"])[v]; named != nil {
			// Any other name is that of a colour space in the resources.
			dict["ColorSpace"] = named
		}
	case pdfobj.Array:
		// An indexed space, written out: its base may be abbreviated too.
		space := make(pdfobj.Array, len(v))
		copy(space, v)
		for i := 0; i < len(space) && i < 2; i++ {
			space[i] = full(space[i], inlineSpaces)
		}
		dict["ColorSpace"] = space
	}
	return dict
}

// inlineSize returns the length of the data of an inline image when its
// dictionary settles it: an image with no filter, of a device colour space
// or an indexed one. It returns -1 when the data has to be searched for its
// end.
func (in *Interp) inlineSize(dict pdfobj.Dict) int {
	if dict["Filter"] != nil {
		return -1
	}
	w, _ := pdfobj.Int(dict["Width"])
	h, _ := pdfobj.Int(dict["Height"])
	bits, _ := pdfobj.Int(dict["BitsPerComponent"])
	n := int64(0)
	space := dict["ColorSpace"]
	if arr, ok := space.(pdfobj.Array); ok && len(arr) > 0 {
		space = arr[0]
	}
	switch space {
	case pdfobj.Name("DeviceGray"), pdfobj.Name("Indexed"):
		n = 1
	case pdfobj.Name("DeviceRGB"):
		n = 3
	case pdfobj.Name("DeviceCMYK"):
		n = 4
	}
	if mask, _ := dict["ImageMask"].(bool); mask {
		n, bits = 1, 1
	}
	if w < 1 || h < 1 || w > 1<<20 || h > 1<<20 || n == 0 || bits < 1 || bits > 16 {
		return -1
	}
	size := (w*n*bits + 7) / 8 * h
	if size > 1<<30 {
		return -1
	}
	return int(size)
}

func (in *Interp) xobject(o pdfobj.Object, parent *resources) {
	st := in.f.Stream(in.ctx, o)
	if st == nil {
		return
	}
	switch in.f.Name(in.ctx, st.Dict["Subtype"]) {
	case "Image":
		in.image(Image{Stream: st})
		return
	case "Form":
	default:
		return
	}
	if in.depth >= maxFormDepth {
		in.f.Warn(reader.WarnLimit, "form XObjects nested more than %d deep were skipped", maxFormDepth)
		return
	}
	if st.Num != 0 {
		if in.active[st.Num] {
			return // a form that draws itself
		}
		in.active[st.Num] = true
		defer delete(in.active, st.Num)
	}
	data, err := in.f.DecodeStream(in.ctx, st)
	if err != nil {
		if in.ctx.Err() == nil {
			in.f.Warn(reader.WarnStreamData, "form XObject %d could not be read: %v", st.Num, err)
		}
		return
	}
	res := parent
	if d := in.f.Dict(in.ctx, st.Dict["Resources"]); d != nil {
		res = &resources{dict: d}
	}
	matrix := Identity
	if arr := in.f.Array(in.ctx, st.Dict["Matrix"]); len(arr) >= 6 {
		for i := range matrix {
			matrix[i], _ = in.f.Number(in.ctx, arr[i])
		}
	}

	// A form runs in its own graphics state and outside any text object.
	saved, savedStack := in.gs, in.stack
	savedTm, savedTlm, savedMarks := in.tm, in.tlm, len(in.marks)
	in.stack = nil
	in.path, in.clipNow = box{}, false
	in.segs, in.rects, in.hasCur = in.segs[:0], in.rects[:0], false
	in.gs.ctm = matrix.Mul(in.gs.ctm)
	in.depth++
	in.run(data, res)
	in.depth--
	in.gs, in.stack = saved, savedStack
	in.tm, in.tlm = savedTm, savedTlm
	if len(in.marks) > savedMarks {
		in.marks = in.marks[:savedMarks]
	}
}
