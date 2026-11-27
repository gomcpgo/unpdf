// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

// Package layout turns the glyphs of a page into words, lines and blocks,
// in reading order.
//
// It works on geometry alone. Glyphs become words by their spacing; words
// are split into columns by looking for vertical bands of white space that
// run between bodies of prose; within a column, words on one baseline form a
// line and lines that follow each other closely form a block.
//
// Coordinates are in display space: points, with the origin at the top left
// of the page as shown and Y growing downward.
package layout

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// Glyph is one glyph of the page, in display space.
type Glyph struct {
	// Text is the glyph's text. It may be empty: where one glyph carries
	// the text of a whole cluster, the others carry none, and only widen
	// the word they belong to.
	Text string
	// X, Y is the glyph origin, on the baseline.
	X, Y float64
	// UX, UY is the unit vector of the writing direction.
	UX, UY float64
	// Advance is the glyph's width along the writing direction.
	Advance float64
	// Size is the font size.
	Size float64
	// Font is the font name.
	Font         string
	Bold, Italic bool
	// Artifact marks page furniture declared as such by the document.
	Artifact bool
	// Group is the block the document's own structure puts the glyph in,
	// or 0 when it says nothing. AnalyzeTagged keeps every group together.
	Group int
}

// Box is a rectangle in display space.
type Box struct {
	X0, Y0, X1, Y1 float64
}

// Word is a run of glyphs with no space between them.
type Word struct {
	Text         string
	Box          Box
	Font         string
	Size         float64
	Bold, Italic bool
}

// Line is a row of words on one baseline, in reading order.
type Line struct {
	Words []Word
	Box   Box
	// RTL reports a line of right-to-left script. Its words are in logical
	// order: the first word is the rightmost on the page.
	RTL bool
	// Compound reports that the hyphen the line ends with belongs to its
	// last word: a compound, such as "long-term", broken at its hyphen and
	// going on with the first word of the next line. The layout cannot
	// tell; whoever knows the words of the document sets it.
	Compound bool
}

// Block is a group of lines that belong together: a paragraph, a heading,
// a list item, a table row.
type Block struct {
	Lines []Line
	Box   Box
	// Angle is the direction of the text, in degrees clockwise from upright:
	// 0 for ordinary text, 90 for text that reads down the page.
	Angle int
	// Furniture marks a running head, a running foot or a page number.
	Furniture bool
	// Group is the group of the glyphs the block was built from, when
	// AnalyzeTagged built it from one.
	Group int
}

// Tuning. Distances are fractions of the font size.
const (
	// SpaceGap is the gap between glyphs beyond which they are separate
	// words. Word spaces are a quarter of an em or more before
	// justification squeezes them; kerning stays under a tenth.
	SpaceGap = 0.12
	// ascent and descent approximate a glyph's box around its baseline.
	ascent  = 0.8
	descent = 0.2
	// gutterGap is the least width of a band of white space between columns.
	gutterGap = 0.8
	// bandGap is the least height of a band of white space that separates
	// regions with different column structures.
	bandGap = 0.6
	// blockGap is the least gap between two lines that starts a new block.
	blockGap = 0.75
	// wideGap is the gap between two words on a line beyond which they look
	// like table cells and not like neighbours in a sentence.
	wideGap = 1.5
)

// part is one glyph's contribution to a word.
type part struct {
	text   string
	x0, x1 float64
}

// word is a Word in the making, in the upright frame of its text direction.
type word struct {
	parts               []part
	x0, x1, top, bottom float64
	base                float64 // baseline
	size                float64
	font                string
	bold, italic        bool
	artifact            bool
	rtl                 bool
	text                string
}

type line struct {
	words               []*word
	x0, x1, top, bottom float64
	base, size          float64
	rtl                 bool
	furniture           bool
}

// Analyze lays out the glyphs of one page. The glyphs must be in the order
// the page draws them: that order is the first clue to which glyphs form a
// word.
func Analyze(glyphs []Glyph) []Block {
	// Text in different directions is laid out separately, each in a frame
	// rotated so that its own text is upright. Nearly always there is only
	// one direction, and then the glyphs are used where they are.
	first, mixed := -1, false
	for i := range glyphs {
		if glyphs[i].Size <= 0 {
			continue
		}
		a := direction(glyphs[i].UX, glyphs[i].UY)
		if first < 0 {
			first = a
		} else if a != first {
			mixed = true
			break
		}
	}
	if first < 0 {
		return nil
	}
	if !mixed {
		return analyze(glyphs, first)
	}

	groups := map[int][]Glyph{}
	var angles []int
	for _, g := range glyphs {
		if g.Size <= 0 {
			continue
		}
		a := direction(g.UX, g.UY)
		if _, seen := groups[a]; !seen {
			angles = append(angles, a)
		}
		groups[a] = append(groups[a], g)
	}
	// The direction with the most text goes first.
	sort.SliceStable(angles, func(i, j int) bool { return len(groups[angles[i]]) > len(groups[angles[j]]) })
	var blocks []Block
	for _, angle := range angles {
		blocks = append(blocks, analyze(groups[angle], angle)...)
	}
	return blocks
}

// AnalyzeTagged lays out a page whose glyphs the document has already
// sorted into blocks: each group of glyphs becomes one block, and the blocks
// come in ascending order of group number. Only words and lines are worked
// out from geometry. Glyphs in no group are laid out the usual way and come
// last; on a tagged page they are artifacts such as running heads.
func AnalyzeTagged(glyphs []Glyph) []Block {
	// The glyphs sorted by group, each group's in the order they came, in
	// one slice: a page can have a group for every cell of a table.
	// Sorted by counting: where each group starts, then every glyph to its
	// group's next free place.
	last := 0
	for i := range glyphs {
		last = max(last, glyphs[i].Group)
	}
	starts := make([]int, last+2)
	kept := 0
	for i := range glyphs {
		if glyphs[i].Size > 0 {
			starts[max(glyphs[i].Group, 0)+1]++
			kept++
		}
	}
	for g := 1; g < len(starts); g++ {
		starts[g] += starts[g-1]
	}
	sorted := make([]Glyph, kept)
	for i := range glyphs {
		if glyphs[i].Size > 0 {
			g := max(glyphs[i].Group, 0)
			sorted[starts[g]] = glyphs[i]
			starts[g]++
		}
	}
	// starts[g] is now where group g ends; group 0 holds what is in none.
	loose := starts[0]
	rest := sorted[:loose:loose]
	var blocks []Block
	for from := loose; from < len(sorted); {
		id := sorted[from].Group
		to := from
		for to < len(sorted) && sorted[to].Group == id {
			to++
		}
		gs := sorted[from:to]
		from = to
		// The direction of most of the group's glyphs.
		var seen [8]struct{ angle, n int }
		used, angle, most := 0, 0, 0
		for i := range gs {
			a := direction(gs[i].UX, gs[i].UY)
			k := 0
			for k < used && seen[k].angle != a {
				k++
			}
			if k == len(seen) {
				continue // more directions in one block than matter
			}
			if k == used {
				seen[k].angle = a
				used++
			}
			if seen[k].n++; seen[k].n > most {
				angle, most = a, seen[k].n
			}
		}
		f := newFrame(angle)
		words, spaces := f.buildWords(gs)
		words = dropDuplicates(words)
		a := &analysis{words: words, spaces: spaces}
		a.rtl = rtlShare(words)
		// A block may run on from the foot of one column to the head of
		// the next. Its words come in reading order, so a jump back up the
		// page starts a new stretch, and each stretch is put into lines on
		// its own.
		var lines []*line
		start := 0
		for i := 1; i <= len(words); i++ {
			if i < len(words) && words[i].base >= words[i-1].base-2*math.Max(words[i].size, words[i-1].size) {
				continue
			}
			stretch := make([]int, 0, i-start)
			for k := start; k < i; k++ {
				stretch = append(stretch, k)
			}
			lines = append(lines, a.buildLines(stretch)...)
			start = i
		}
		for _, l := range lines {
			l.finish(a.rtl)
		}
		if b, ok := f.oneBlock(lines); ok {
			b.Group = id
			blocks = append(blocks, b)
		}
	}
	// What the structure leaves out is page furniture, by rights. Anything
	// above all the structured text goes first, the rest last.
	top := math.Inf(1)
	for _, b := range blocks {
		if b.Angle == 0 {
			top = math.Min(top, b.Box.Y0)
		}
	}
	var before, after []Block
	for _, b := range Analyze(rest) {
		if b.Box.Y1 <= top {
			before = append(before, b)
		} else {
			after = append(after, b)
		}
	}
	return append(append(before, blocks...), after...)
}

// rtlShare reports whether most of the words are right-to-left script.
func rtlShare(words []*word) bool {
	rtl, ltr := 0, 0
	for _, w := range words {
		switch {
		case w.isRTL():
			rtl++
		case w.hasLetter():
			ltr++
		}
	}
	return rtl > 0 && rtl >= ltr
}

// oneBlock makes a single block of all the lines.
func (f frame) oneBlock(lines []*line) (Block, bool) {
	parts := f.buildBlocks(lines)
	if len(parts) == 0 {
		return Block{}, false
	}
	b := parts[0]
	b.Furniture = false
	for _, p := range parts[1:] {
		b.Lines = append(b.Lines, p.Lines...)
		b.Box.X0, b.Box.Y0 = math.Min(b.Box.X0, p.Box.X0), math.Min(b.Box.Y0, p.Box.Y0)
		b.Box.X1, b.Box.Y1 = math.Max(b.Box.X1, p.Box.X1), math.Max(b.Box.Y1, p.Box.Y1)
	}
	return b, true
}

// analyze lays out glyphs that all run in one direction.
func analyze(glyphs []Glyph, angle int) []Block {
	f := newFrame(angle)
	words, spaces := f.buildWords(glyphs)
	words = dropDuplicates(words)
	a := &analysis{words: words, spaces: spaces}
	a.rtl = rtlShare(words)
	var blocks []Block
	for _, leaf := range a.cut(all(len(words)), 0) {
		lines := a.buildLines(leaf)
		for _, l := range lines {
			l.finish(a.rtl)
		}
		blocks = append(blocks, f.buildBlocks(lines)...)
	}
	return blocks
}

// Mark returns blocks with page furniture marked. test is asked about every
// line of upright text that is not marked already, and a block whose lines
// do not all get the same answer is split, so that a running head never
// shares a block with the text below it. The input is not modified.
func Mark(blocks []Block, test func(text string, box Box) bool) []Block {
	out := make([]Block, 0, len(blocks))
	for _, b := range blocks {
		if b.Furniture || b.Angle != 0 {
			out = append(out, b)
			continue
		}
		start := 0
		var prev bool
		flush := func(end int, furniture bool) {
			if end <= start {
				return
			}
			part := Block{Lines: b.Lines[start:end:end], Angle: b.Angle, Furniture: furniture, Group: b.Group, Box: b.Lines[start].Box}
			for _, l := range part.Lines[1:] {
				part.Box.X0, part.Box.Y0 = math.Min(part.Box.X0, l.Box.X0), math.Min(part.Box.Y0, l.Box.Y0)
				part.Box.X1, part.Box.Y1 = math.Max(part.Box.X1, l.Box.X1), math.Max(part.Box.Y1, l.Box.Y1)
			}
			out = append(out, part)
			start = end
		}
		for i, l := range b.Lines {
			is := test(l.Text(), l.Box)
			if i > 0 && is != prev {
				flush(i, prev)
			}
			prev = is
		}
		flush(len(b.Lines), prev)
	}
	return out
}

// direction returns the writing direction as an angle in degrees, clockwise
// in display space, snapped to a right angle when it is close to one and to
// a multiple of five degrees otherwise.
func direction(ux, uy float64) int {
	if ux == 0 && uy == 0 {
		return 0
	}
	deg := math.Atan2(uy, ux) * 180 / math.Pi
	if deg < 0 {
		deg += 360
	}
	right := math.Round(deg/90) * 90
	if math.Abs(deg-right) <= 12 {
		return int(right) % 360
	}
	return int(math.Round(deg/5)*5) % 360
}

// frame converts between display space and the upright frame of one text
// direction, in which that text runs along +X and lines follow each other
// along +Y.
type frame struct {
	angle    int
	cos, sin float64
}

func newFrame(angle int) frame {
	rad := float64(angle) * math.Pi / 180
	f := frame{angle: angle, cos: math.Cos(rad), sin: math.Sin(rad)}
	// Exact values at right angles keep upright text free of rounding.
	switch angle {
	case 0:
		f.cos, f.sin = 1, 0
	case 90:
		f.cos, f.sin = 0, 1
	case 180:
		f.cos, f.sin = -1, 0
	case 270:
		f.cos, f.sin = 0, -1
	}
	return f
}

func (f frame) in(x, y float64) (float64, float64) {
	return x*f.cos + y*f.sin, -x*f.sin + y*f.cos
}

func (f frame) out(x, y float64) (float64, float64) {
	return x*f.cos - y*f.sin, x*f.sin + y*f.cos
}

// box converts a rectangle of the frame to its bounding box in display
// space.
func (f frame) box(x0, y0, x1, y1 float64) Box {
	b := Box{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	for _, p := range [4][2]float64{{x0, y0}, {x1, y0}, {x0, y1}, {x1, y1}} {
		x, y := f.out(p[0], p[1])
		b.X0, b.Y0 = math.Min(b.X0, x), math.Min(b.Y0, y)
		b.X1, b.Y1 = math.Max(b.X1, x), math.Max(b.Y1, y)
	}
	return b
}

// buildWords groups glyphs into words, relying on drawing order: the glyphs
// of a word are drawn one after another, each starting where the last ended.
func (f frame) buildWords(glyphs []Glyph) ([]*word, []spaceMark) {
	// Words and their parts are carved out of two large slices, not
	// allocated one by one: a page has thousands of them.
	slab := make([]word, 0, len(glyphs)/4+2)
	arena := make([]part, 0, len(glyphs))
	newWord := func(w word) *word {
		if len(slab) == cap(slab) {
			slab = make([]word, 0, cap(slab)) // earlier words stay where they are
		}
		slab = append(slab, w)
		return &slab[len(slab)-1]
	}
	words := make([]*word, 0, len(glyphs)/4+2)
	var spaces []spaceMark
	var cur *word
	curStart := 0
	// Fonts found to overstate their widths (see below), with the width
	// their glyphs really take. A word's last glyph has no neighbour to
	// show its width, so it is given this one.
	type face struct {
		font string
		size float64
	}
	var narrow map[face]float64
	finish := func(w *word) {
		if w == nil || narrow == nil || len(w.parts) == 0 {
			return
		}
		last := &w.parts[len(w.parts)-1]
		if width, ok := narrow[face{w.font, w.size}]; ok && last.x1-last.x0 > 1.5*width && last.x1 == w.x1 {
			last.x1 = last.x0 + width
			w.x1 = w.x0
			for _, p := range w.parts {
				w.x1 = math.Max(w.x1, p.x1)
			}
		}
	}
	for i := range glyphs {
		g := &glyphs[i]
		x, y := f.in(g.X, g.Y)
		adv := math.Max(g.Advance, 0)
		if g.Text == "" {
			// A glyph without text of its own: part of the cluster of the
			// glyph before it.
			if cur != nil && math.Abs(y-cur.base) <= 0.5*cur.size && x-cur.x1 <= SpaceGap*cur.size && x >= cur.x0-cur.size {
				cur.x1 = math.Max(cur.x1, x+adv)
			}
			continue
		}
		if strings.TrimSpace(g.Text) == "" {
			// A space ends the word. It leaves no word of its own, only a
			// mark where it stood.
			finish(cur)
			cur = nil
			if width, ok := narrow[face{g.Font, g.Size}]; ok {
				adv = math.Min(adv, width)
			}
			spaces = append(spaces, spaceMark{x + adv/2, y})
			continue
		}
		if cur != nil && combining(g.Text) && math.Abs(y-cur.base) <= cur.size && x > cur.x0-cur.size && x < cur.x1+cur.size {
			// A vowel point or an accent drawn over the glyph before it:
			// its text follows that glyph's, wherever exactly it sits.
			arena[len(arena)-1].text += g.Text
			cur.parts = arena[curStart:len(arena):len(arena)]
			continue
		}
		if cur != nil {
			size := math.Max(cur.size, g.Size)
			gap := x - cur.x1
			last := &arena[len(arena)-1]
			switch {
			case math.Abs(y-cur.base) > 0.5*size || gap > SpaceGap*size:
				finish(cur)
				cur = nil
			case gap < -0.5*size && x >= last.x0+0.25*size && last.x1 == cur.x1:
				// The glyph starts well inside the one before it, yet
				// further along: that glyph is narrower than its font said.
				// Fonts that give no widths do this, with every glyph set
				// in place one by one. The word goes on.
				last.x1, cur.x1 = x, x
				if narrow == nil {
					narrow = map[face]float64{}
				}
				key := face{cur.font, cur.size}
				if width, ok := narrow[key]; ok {
					narrow[key] = (3*width + x - last.x0) / 4
				} else {
					narrow[key] = x - last.x0
				}
			case gap < -0.5*size:
				finish(cur)
				cur = nil
			}
		}
		if cur == nil {
			cur = newWord(word{
				x0: x, x1: x, top: y - ascent*g.Size, bottom: y + descent*g.Size, base: y,
				size: g.Size, font: g.Font, bold: g.Bold, italic: g.Italic, artifact: true,
			})
			words = append(words, cur)
			curStart = len(arena)
		}
		// The parts of the current word are the tail of the arena. The
		// capacity is clipped so that a later append to this word, when
		// words are merged, copies instead of running into its neighbour.
		arena = append(arena, part{g.Text, x, x + adv})
		cur.parts = arena[curStart:len(arena):len(arena)]
		cur.x0, cur.x1 = math.Min(cur.x0, x), math.Max(cur.x1, x+adv)
		cur.top, cur.bottom = math.Min(cur.top, y-ascent*g.Size), math.Max(cur.bottom, y+descent*g.Size)
		cur.artifact = cur.artifact && g.Artifact
		if g.Size > cur.size {
			// The word takes the look of its largest glyph: a drop cap or a
			// superscript does not define it.
			cur.size, cur.font, cur.bold, cur.italic, cur.base = g.Size, g.Font, g.Bold, g.Italic, y
		}
	}
	finish(cur)
	return words, spaces
}

// dropDuplicates removes a word that is drawn again on top of itself: the
// same text over the same place. Shadows, faked bold and a form field shown
// both by the page and by its annotation do this.
func dropDuplicates(words []*word) []*word {
	const lookBack = 64
	byText := map[string][]*word{}
	out := words[:0]
	for _, w := range words {
		var key strings.Builder
		for _, p := range w.parts {
			key.WriteString(p.text)
		}
		text := key.String()
		seen := byText[text]
		dup := false
		for i := len(seen) - 1; i >= 0 && i >= len(seen)-lookBack; i-- {
			o := seen[i]
			overlap := math.Min(w.x1, o.x1) - math.Max(w.x0, o.x0)
			narrow := math.Min(w.x1-w.x0, o.x1-o.x0)
			small, large := math.Min(w.size, o.size), math.Max(w.size, o.size)
			if large <= 1.25*small && math.Abs(w.base-o.base) <= 0.3*small && overlap > 0.6*narrow && narrow > 0 {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		byText[text] = append(seen, w)
		out = append(out, w)
	}
	return out
}

// combining reports whether s consists of combining marks only.
func combining(s string) bool {
	for _, r := range s {
		if !unicode.Is(unicode.Mn, r) && !unicode.Is(unicode.Me, r) {
			return false
		}
	}
	return s != ""
}

func all(n int) []int {
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	return idx
}

type analysis struct {
	words []*word
	// rtl reports that most of the text is right-to-left script. It
	// settles the direction of lines that mix both.
	rtl bool
	// spaces are the space glyphs of the page. Two pieces of text with a
	// space glyph between them are separate words however narrow the gap.
	spaces []spaceMark
}

// spaceMark is the position of one space glyph: its middle, on a baseline.
type spaceMark struct {
	x, base float64
}

func (a *analysis) medianSize(idx []int) float64 {
	sizes := make([]float64, len(idx))
	for i, w := range idx {
		sizes[i] = a.words[w].size
	}
	sort.Float64s(sizes)
	return sizes[len(sizes)/2]
}

type span struct{ lo, hi float64 }

// gaps returns the stretches not covered by any of the spans, between the
// first and the last, that are at least min wide.
func gaps(spans []span, min float64) []span {
	sort.Slice(spans, func(i, j int) bool { return spans[i].lo < spans[j].lo })
	var out []span
	reach := math.Inf(-1)
	for i, s := range spans {
		if i > 0 && s.lo-reach >= min {
			out = append(out, span{reach, s.lo})
		}
		reach = math.Max(reach, s.hi)
	}
	return out
}

// maxDepth bounds the recursion of the column search.
const maxDepth = 16

// cut splits a region into parts that are each read from top to bottom, and
// returns them in reading order. A region divided by gutters is split into
// its columns. Otherwise it is split into horizontal bands wherever white
// space allows, and bands that share a column structure are rejoined, so
// that a column is read to its end before the next one starts.
func (a *analysis) cut(idx []int, depth int) [][]int {
	if len(idx) < 6 || depth >= maxDepth {
		return [][]int{idx}
	}
	med := a.medianSize(idx)

	if gutters := a.gutters(idx, med); len(gutters) > 0 {
		cols := make([][]int, len(gutters)+1)
		rtl := 0
		for _, w := range idx {
			mid := (a.words[w].x0 + a.words[w].x1) / 2
			c := 0
			for c < len(gutters) && mid > gutters[c].lo {
				c++
			}
			cols[c] = append(cols[c], w)
			if a.words[w].isRTL() {
				rtl++
			}
		}
		if rtl*2 > len(idx) {
			// Right-to-left pages start with the rightmost column.
			for i, j := 0, len(cols)-1; i < j; i, j = i+1, j-1 {
				cols[i], cols[j] = cols[j], cols[i]
			}
		}
		var out [][]int
		for _, col := range cols {
			if len(col) > 0 {
				out = append(out, a.cut(col, depth+1)...)
			}
		}
		return out
	}

	// Horizontal bands.
	spans := make([]span, len(idx))
	for i, w := range idx {
		spans[i] = span{a.words[w].top, a.words[w].bottom}
	}
	breaks := gaps(spans, bandGap*med)
	if len(breaks) == 0 {
		return [][]int{idx}
	}
	bands := make([][]int, len(breaks)+1)
	for _, w := range idx {
		mid := (a.words[w].top + a.words[w].bottom) / 2
		b := 0
		for b < len(breaks) && mid > breaks[b].lo {
			b++
		}
		bands[b] = append(bands[b], w)
	}

	// Rejoin neighbouring bands with the same column structure: both
	// without gutters, or with gutters in the same place.
	type region struct {
		idx     []int
		gutters []span
	}
	var regions []region
	for _, band := range bands {
		if len(band) == 0 {
			continue
		}
		r := region{band, a.gutters(band, a.medianSize(band))}
		if n := len(regions); n > 0 {
			prev := &regions[n-1]
			joined := append(append([]int(nil), prev.idx...), band...)
			same := len(prev.gutters) == 0 && len(r.gutters) == 0
			var shared []span
			if !same && len(prev.gutters) > 0 && len(r.gutters) > 0 {
				shared = a.gutters(joined, a.medianSize(joined))
				same = len(shared) > 0
			}
			if same {
				prev.idx, prev.gutters = joined, shared
				continue
			}
		}
		regions = append(regions, r)
	}
	if len(regions) == 1 && len(regions[0].gutters) == 0 {
		return [][]int{idx}
	}
	var out [][]int
	for _, r := range regions {
		if len(r.gutters) > 0 {
			out = append(out, a.cut(r.idx, depth+1)...)
		} else {
			out = append(out, r.idx)
		}
	}
	return out
}

// gutters returns the vertical bands of white space that divide a region
// into columns of prose.
//
// White space alone is not enough: the columns of a table are separated by
// it too, and a table must be read row by row. So a gap counts as a gutter
// only when the text on both sides of it reads like prose: several lines,
// several words to a line, and the words of a line close together.
func (a *analysis) gutters(idx []int, med float64) []span {
	if len(idx) < 12 {
		return nil
	}
	spans := make([]span, len(idx))
	for i, w := range idx {
		spans[i] = span{a.words[w].x0, a.words[w].x1}
	}
	candidates := gaps(spans, math.Max(gutterGap*med, 4))
	var out []span
	for _, g := range candidates {
		var left, right []int
		for _, w := range idx {
			if a.words[w].x1 <= g.lo+0.01 {
				left = append(left, w)
			} else {
				right = append(right, w)
			}
		}
		if a.prose(left) && a.prose(right) {
			out = append(out, g)
		}
	}
	return out
}

// prose reports whether a set of words looks like running text.
func (a *analysis) prose(idx []int) bool {
	if len(idx) < 8 {
		return false
	}
	rows := a.rows(idx)
	if len(rows) < 3 {
		return false
	}
	counts := make([]int, len(rows))
	pairs, wide := 0, 0
	for i, row := range rows {
		counts[i] = len(row)
		for j := 1; j < len(row); j++ {
			prev, cur := a.words[row[j-1]], a.words[row[j]]
			pairs++
			if cur.x0-prev.x1 > wideGap*math.Max(prev.size, cur.size) {
				wide++
			}
		}
	}
	sort.Ints(counts)
	return counts[len(counts)/2] >= 2 && len(idx) >= 3*len(rows)-len(rows)/2 && wide*10 <= pairs*3
}

// rows groups words by baseline and sorts each group from left to right.
func (a *analysis) rows(idx []int) [][]int {
	sorted := append([]int(nil), idx...)
	sort.Slice(sorted, func(i, j int) bool { return a.words[sorted[i]].base < a.words[sorted[j]].base })
	var rows [][]int
	var base, size float64
	for _, w := range sorted {
		wd := a.words[w]
		if n := len(rows); n > 0 && wd.base-base <= 0.45*math.Max(size, wd.size) {
			rows[n-1] = append(rows[n-1], w)
			size = math.Max(size, wd.size)
			continue
		}
		rows = append(rows, []int{w})
		base, size = wd.base, wd.size
	}
	for _, row := range rows {
		sort.Slice(row, func(i, j int) bool { return a.words[row[i]].x0 < a.words[row[j]].x0 })
	}
	return rows
}

// buildLines forms the lines of one column, top to bottom.
func (a *analysis) buildLines(idx []int) []*line {
	var lines []*line
	for _, row := range a.rows(idx) {
		l := &line{x0: math.Inf(1), top: math.Inf(1), x1: math.Inf(-1), bottom: math.Inf(-1)}
		// The space glyphs on this row's baseline.
		var rowSpaces []float64
		if len(row) > 1 {
			first := a.words[row[0]]
			for _, sp := range a.spaces {
				if math.Abs(sp.base-first.base) <= 0.5*first.size {
					rowSpaces = append(rowSpaces, sp.x)
				}
			}
		}
		// Where the producer wrote its spaces out as glyphs, a gap without
		// one is not a word break unless it is wide. That keeps scripts
		// with loosely fitted letters, such as Arabic, in one piece.
		mergeGap := SpaceGap
		if len(rowSpaces) >= 3 || (len(rowSpaces) >= 1 && len(row) <= 6) {
			mergeGap = 0.3
		}
		spaceBetween := func(lo, hi float64) bool {
			for _, x := range rowSpaces {
				if x > lo-0.5 && x < hi+0.5 {
					return true
				}
			}
			return false
		}
		for _, w := range row {
			wd := a.words[w]
			// Join pieces that touch: one word drawn in several goes, or a
			// word whose glyphs were not drawn in order.
			if n := len(l.words); n > 0 {
				prev := l.words[n-1]
				size := math.Max(prev.size, wd.size)
				gap := wd.x0 - prev.x1
				// Words that overlap for real are separate things drawn in
				// the same place; mixing their letters helps nobody.
				if gap <= mergeGap*size && gap > -0.4*size && !spaceBetween(prev.x1, wd.x0) {
					prev.parts = append(prev.parts, wd.parts...)
					prev.x1 = math.Max(prev.x1, wd.x1)
					prev.top, prev.bottom = math.Min(prev.top, wd.top), math.Max(prev.bottom, wd.bottom)
					prev.artifact = prev.artifact && wd.artifact
					l.x1 = math.Max(l.x1, prev.x1)
					l.top, l.bottom = math.Min(l.top, prev.top), math.Max(l.bottom, prev.bottom)
					continue
				}
			}
			l.words = append(l.words, wd)
			l.x0, l.x1 = math.Min(l.x0, wd.x0), math.Max(l.x1, wd.x1)
			l.top, l.bottom = math.Min(l.top, wd.top), math.Max(l.bottom, wd.bottom)
			if wd.size > l.size {
				l.size, l.base = wd.size, wd.base
			}
		}
		lines = append(lines, l.splitArtifacts()...)
	}
	return lines
}

// splitArtifacts separates words the document marks as artifacts from the
// rest of the line, so that a running head set beside real text can be told
// apart from it.
func (l *line) splitArtifacts() []*line {
	n := 0
	for _, w := range l.words {
		if w.artifact {
			n++
		}
	}
	if n == 0 || n == len(l.words) {
		return []*line{l}
	}
	parts := [2]*line{}
	for _, w := range l.words {
		i := 0
		if w.artifact {
			i = 1
		}
		if parts[i] == nil {
			parts[i] = &line{x0: w.x0, x1: w.x1, top: w.top, bottom: w.bottom}
		}
		p := parts[i]
		p.words = append(p.words, w)
		p.x0, p.x1 = math.Min(p.x0, w.x0), math.Max(p.x1, w.x1)
		p.top, p.bottom = math.Min(p.top, w.top), math.Max(p.bottom, w.bottom)
		if w.size > p.size {
			p.size, p.base = w.size, w.base
		}
	}
	return parts[:]
}

// finish settles the text of each word and the order of the words, which
// for right-to-left script differ from the order on the page.
func (l *line) finish(pageRTL bool) {
	// A "word" that mixes scripts of both directions, a Latin name set
	// tight against an Arabic word, is two words.
	split := make([]*word, 0, len(l.words))
	for _, w := range l.words {
		sort.SliceStable(w.parts, func(i, j int) bool { return w.parts[i].x0 < w.parts[j].x0 })
		split = append(split, w.byDirection()...)
	}
	l.words = split

	rtl, ltr := 0, 0
	artifact := true
	for _, w := range l.words {
		w.rtl = w.isRTL()
		switch {
		case w.rtl:
			rtl++
		case w.hasLetter():
			ltr++
		}
		artifact = artifact && w.artifact
	}
	l.furniture = artifact && len(l.words) > 0
	// A line is right-to-left when most of its words are, or, on a page of
	// right-to-left text, when a fair share of them are: a sentence that
	// quotes a few foreign words keeps its direction.
	l.rtl = rtl > ltr || (pageRTL && rtl > 0 && rtl*2 >= ltr)

	for _, w := range l.words {
		var b strings.Builder
		if w.rtl {
			// The glyphs run right to left: the last on the page is first.
			for i := len(w.parts) - 1; i >= 0; i-- {
				b.WriteString(w.parts[i].text)
			}
		} else {
			for _, p := range w.parts {
				b.WriteString(p.text)
			}
		}
		w.text = b.String()
	}
	if !l.rtl {
		return
	}
	// Read the line from the right. Runs of left-to-right words inside it,
	// numbers and foreign words, keep their own order.
	for i, j := 0, len(l.words)-1; i < j; i, j = i+1, j-1 {
		l.words[i], l.words[j] = l.words[j], l.words[i]
	}
	for i := 0; i < len(l.words); {
		j := i
		for j < len(l.words) && !l.words[j].rtl {
			j++
		}
		for p, q := i, j-1; p < q; p, q = p+1, q-1 {
			l.words[p], l.words[q] = l.words[q], l.words[p]
		}
		if j > i {
			// Punctuation that closes the run belongs to the right-to-left
			// sentence around it, so on the page it stands at the run's
			// left end: ",2024" is "2024," in reading order.
			first, last := l.words[i], l.words[j-1]
			lead := 0
			for lead < len(first.text) && strings.IndexByte(".,;:!?", first.text[lead]) >= 0 {
				lead++
			}
			if lead > 0 && lead < len(first.text) {
				moved := first.text[:lead]
				first.text = first.text[lead:]
				last.text += moved
			}
		}
		i = max(j, i+1)
	}
}

func (l *line) text() string {
	parts := make([]string, len(l.words))
	for i, w := range l.words {
		parts[i] = w.text
	}
	return strings.Join(parts, " ")
}

// byDirection splits a word where its text changes between right-to-left
// and left-to-right script. Characters with no direction of their own, such
// as punctuation, stay with what precedes them. Most words come back whole.
func (w *word) byDirection() []*word {
	dir := func(p part) int {
		d := 0
		for _, r := range p.text {
			switch {
			case rtlRune(r):
				return -1
			case unicode.IsLetter(r) || unicode.IsDigit(r):
				d = 1
			}
		}
		return d
	}
	mixed, last := false, 0
	for _, p := range w.parts {
		if d := dir(p); d != 0 {
			mixed = mixed || (last != 0 && d != last)
			last = d
		}
	}
	if !mixed {
		return []*word{w}
	}
	var out []*word
	last = 0
	for _, p := range w.parts {
		d := dir(p)
		if len(out) == 0 || (d != 0 && last != 0 && d != last) {
			piece := *w
			piece.parts, piece.x0, piece.x1 = nil, p.x0, p.x1
			out = append(out, &piece)
		}
		cur := out[len(out)-1]
		cur.parts = append(cur.parts, p)
		cur.x0, cur.x1 = math.Min(cur.x0, p.x0), math.Max(cur.x1, p.x1)
		if d != 0 {
			last = d
		}
	}
	return out
}

// isRTL reports whether the word contains a letter of a right-to-left
// script.
func (w *word) isRTL() bool {
	for _, p := range w.parts {
		for _, r := range p.text {
			if rtlRune(r) {
				return true
			}
		}
	}
	return false
}

func (w *word) hasLetter() bool {
	for _, p := range w.parts {
		for _, r := range p.text {
			if unicode.IsLetter(r) {
				return true
			}
		}
	}
	return false
}

// rtlRune reports whether r belongs to Hebrew, Arabic or another
// right-to-left script.
func rtlRune(r rune) bool {
	switch {
	case r < 0x0590:
		return false
	case r <= 0x08FF: // Hebrew, Arabic, Syriac, Thaana, NKo, Samaritan, Mandaic
		return unicode.IsLetter(r)
	case r >= 0xFB1D && r <= 0xFDFF, r >= 0xFE70 && r <= 0xFEFC: // presentation forms
		return true
	case r >= 0x10800 && r <= 0x10FFF, r >= 0x1E800 && r <= 0x1EFFF: // historic and other RTL scripts
		return true
	}
	return false
}

// buildBlocks groups the lines of one column into blocks and converts them
// to display space. A new block starts after a gap wider than ordinary
// leading, at a change of font size, and where furniture meets body text.
func (f frame) buildBlocks(lines []*line) []Block {
	var blocks []Block
	var prev *line
	for _, l := range lines {
		if len(l.words) == 0 {
			continue
		}
		start := prev == nil || l.furniture != prev.furniture
		if !start {
			small, large := math.Min(prev.size, l.size), math.Max(prev.size, l.size)
			start = l.top-prev.bottom > blockGap*small || large > 1.15*small
		}
		if start {
			blocks = append(blocks, Block{Angle: f.angle, Furniture: l.furniture,
				Box: Box{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}})
		}
		b := &blocks[len(blocks)-1]
		out := Line{RTL: l.rtl, Box: f.box(l.x0, l.top, l.x1, l.bottom)}
		for _, w := range l.words {
			out.Words = append(out.Words, Word{
				Text: w.text, Box: f.box(w.x0, w.top, w.x1, w.bottom),
				Font: w.font, Size: w.size, Bold: w.bold, Italic: w.italic,
			})
		}
		b.Lines = append(b.Lines, out)
		b.Box.X0, b.Box.Y0 = math.Min(b.Box.X0, out.Box.X0), math.Min(b.Box.Y0, out.Box.Y0)
		b.Box.X1, b.Box.Y1 = math.Max(b.Box.X1, out.Box.X1), math.Max(b.Box.Y1, out.Box.Y1)
		prev = l
	}
	return blocks
}

// Text returns the text of a line: its words separated by single spaces.
func (l Line) Text() string {
	parts := make([]string, len(l.Words))
	for i, w := range l.Words {
		parts[i] = w.Text
	}
	return strings.Join(parts, " ")
}

// Text returns the text of a block, one line of the page per line of text.
// A word hyphenated at the end of a line is joined to its continuation,
// which moves up to the line where the word began, when that is clearly
// what the hyphen is for.
func (b Block) Text() string {
	lines := make([][]string, len(b.Lines))
	rtl := make([]bool, len(b.Lines))
	var compound []bool
	for i, l := range b.Lines {
		lines[i] = make([]string, len(l.Words))
		for j, w := range l.Words {
			lines[i][j] = w.Text
		}
		rtl[i] = l.RTL
		if l.Compound {
			if compound == nil {
				compound = make([]bool, len(b.Lines))
			}
			compound[i] = true
		}
	}
	return JoinLines(lines, rtl, compound)
}

// JoinLines renders lines of words as text, joining words hyphenated across
// line ends. rtl says which lines are right-to-left; those are left alone.
// compound, which may be nil, says which lines end in the hyphen of a
// compound: there the word is joined with its hyphen kept. The slices are
// modified.
func JoinLines(lines [][]string, rtl, compound []bool) string {
	for i := 0; i+1 < len(lines); {
		cur, next := lines[i], lines[i+1]
		if len(next) == 0 {
			// A line used up by the word above it: close the gap, so that
			// a word split over three lines is joined in full. The word now
			// ends as that line did.
			lines, rtl = append(lines[:i+1], lines[i+2:]...), append(rtl[:i+1], rtl[i+2:]...)
			if compound != nil {
				compound[i] = compound[i+1]
				compound = append(compound[:i+1], compound[i+2:]...)
			}
			continue
		}
		if len(cur) > 0 && !rtl[i] {
			if word, ok := Join(cur[len(cur)-1], next[0], compound != nil && compound[i]); ok {
				cur[len(cur)-1] = word
				lines[i+1] = next[1:]
				continue
			}
		}
		i++
	}
	out := make([]string, 0, len(lines))
	for _, words := range lines {
		if len(words) > 0 {
			out = append(out, strings.Join(words, " "))
		}
	}
	return strings.Join(out, "\n")
}

// Join joins last, the final word of a line, to next, the first word of the
// following line, if last was split there. compound says that its hyphen
// is the hyphen of a compound, and stays: "long-" and "term" make
// "long-term", where "exam-" and "ple" make "example". A soft hyphen never
// stays.
func Join(last, next string, compound bool) (string, bool) {
	stem, ok := Hyphenated(last, next)
	if !ok {
		return "", false
	}
	if compound && !strings.HasSuffix(last, "\u00ad") {
		return last + next, true
	}
	return stem + next, true
}

// Hyphenated reports whether last, the final word of a line, was split by
// hyphenation and continues with next, the first word of the following
// line. It returns the word without its hyphen.
//
// The test is conservative. A soft hyphen always marks a split. An ordinary
// hyphen does only when lower-case letters stand on both sides of the line
// break: that leaves out compounds with capitals, numbers, ranges and
// dashes. A compound of two lower-case words broken at its hyphen, "well-"
// "known", passes the test too. Nothing in the two words tells it from a
// word broken for the line; the other words of the document may, and
// Line.Compound is where that is recorded.
func Hyphenated(last, next string) (string, bool) {
	r := []rune(last)
	if len(r) < 2 {
		return "", false
	}
	h := r[len(r)-1]
	stem := string(r[:len(r)-1])
	if h == 0x00AD {
		return stem, true
	}
	if h != '-' && h != 0x2010 {
		return "", false
	}
	before := r[len(r)-2]
	if next == "" {
		return "", false
	}
	after := []rune(next)[0]
	if unicode.IsLower(before) && unicode.IsLower(after) {
		return stem, true
	}
	return "", false
}

// Text returns the text of a page: its blocks in reading order, separated
// by blank lines.
func Text(blocks []Block) string {
	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		if s := b.Text(); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n\n")
}
