// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package unpdf

import (
	"context"
	"math"
	"sort"
	"strings"
	"unicode"
)

// BlockKind says what part a block plays in the document.
type BlockKind int

const (
	// Paragraph is body text, and anything not recognised as something
	// else.
	Paragraph BlockKind = iota
	// Heading is a title or section heading. Block.Level is its rank, from
	// 1 for the largest.
	Heading
	// ListItem is one item of a bulleted or numbered list. Block.Marker is
	// its bullet or number as printed, and Block.Level its depth, from 0.
	ListItem
)

func (k BlockKind) String() string {
	switch k {
	case Heading:
		return "heading"
	case ListItem:
		return "list item"
	}
	return "paragraph"
}

// styleModel is what a document's pages have in common typographically: the
// size of its body text, and the larger sizes it sets headings in.
type styleModel struct {
	body     float64
	headings []float64 // sizes above the body size, largest first
	boldBody bool      // most body text is bold, so bold marks nothing
	text     []float64 // other sizes that running text is set in
	// tagsHeadings reports that the document's structure tree marks
	// headings, so that its word on what is a heading can be taken.
	tagsHeadings bool
}

// styleSample is how many pages are read to learn a document's styles.
const styleSample = 24

// roundSize puts font sizes that differ by rounding into one bucket.
func roundSize(size float64) float64 { return math.Round(size*2) / 2 }

// style returns the document's style model, reading a sample of its pages
// on first use.
func (d *Document) style(ctx context.Context) (*styleModel, error) {
	if d.styles != nil {
		return d.styles, nil
	}
	n := d.NumPages()
	var sample []int
	if n <= styleSample {
		for i := 1; i <= n; i++ {
			sample = append(sample, i)
		}
	} else {
		for i := 0; i < styleSample; i++ {
			sample = append(sample, 1+i*(n-1)/(styleSample-1))
		}
	}
	chars := map[float64]int{}
	bold := map[float64]int{}
	long := map[float64]int{} // characters in blocks too long to be headings
	tagged := false
	for _, num := range sample {
		page, err := d.Page(ctx, num)
		if err != nil {
			if cerr := ctx.Err(); cerr != nil {
				return nil, cerr
			}
			continue
		}
		blocks, err := page.layout(ctx, false)
		if err != nil {
			return nil, err
		}
		if page.tags != nil {
			for _, g := range page.tags.groups {
				tagged = tagged || g.kind == Heading
			}
		}
		for _, b := range blocks {
			if b.Furniture || b.Angle != 0 {
				continue
			}
			for _, l := range b.Lines {
				for _, w := range l.Words {
					size := roundSize(w.Size)
					chars[size] += len(w.Text)
					if len(b.Lines) > maxHeadingLines {
						long[size] += len(w.Text)
					}
					if w.Bold {
						bold[size] += len(w.Text)
					}
				}
			}
		}
	}
	m := &styleModel{tagsHeadings: tagged}
	most := 0
	for size, c := range chars {
		if c > most || (c == most && size < m.body) {
			m.body, most = size, c
		}
	}
	m.boldBody = bold[m.body]*2 > most
	for size, c := range chars {
		if size < m.body*headingRatio || c < 3 {
			continue
		}
		// A larger size that is used for whole paragraphs is a second body
		// size, for a letter before the accounts or the main column beside
		// the notes. Headings are short.
		if c >= 200 && long[size]*100 >= c*30 {
			m.text = append(m.text, size)
			continue
		}
		m.headings = append(m.headings, size)
	}
	sort.Sort(sort.Reverse(sort.Float64Slice(m.headings)))
	// Sizes within a few percent of each other are one level, not two: a
	// title in 24 point and a subtitle in 23 do not make a hierarchy.
	merged := m.headings[:0]
	for _, size := range m.headings {
		if n := len(merged); n > 0 && merged[n-1] <= size*1.08 {
			continue
		}
		merged = append(merged, size)
	}
	m.headings = merged
	d.styles = m
	return m, nil
}

const (
	// headingRatio is how much larger than body text a heading must be to
	// be one on size alone.
	headingRatio = 1.12
	// maxHeadingChars and maxHeadingLines bound what can be a heading: it
	// is short.
	maxHeadingChars = 200
	maxHeadingLines = 3
)

// headingLevel returns the heading level of a block, or 0 when it is not a
// heading. A block is a heading when it is short and either set larger than
// the body text or set in bold where the body is not.
func (m *styleModel) headingLevel(b *Block) int {
	if len(b.Lines) == 0 || len(b.Lines) > maxHeadingLines {
		return 0
	}
	chars, letters, boldChars := 0, 0, 0
	size := 0.0
	var last rune
	for _, l := range b.Lines {
		for _, w := range l.Words {
			chars += len(w.Text) + 1
			if w.Bold {
				boldChars += len(w.Text) + 1
			}
			size = math.Max(size, roundSize(w.Size))
			for _, r := range w.Text {
				if unicode.IsLetter(r) {
					letters++
				}
				last = r
			}
		}
	}
	if chars > maxHeadingChars || letters < 2 {
		return 0
	}
	// A line of a table of contents: a title, dot leaders, a page number.
	if text := b.Text(); strings.Contains(text, "...") || strings.Contains(text, ". . .") {
		return 0
	}
	// Words spread across the line are the cells of a table row, however
	// bold: a header row is not a heading.
	for _, l := range b.Lines {
		for i := 1; i < len(l.Words); i++ {
			gap := l.Words[i].Box.X0 - l.Words[i-1].Box.X1
			if l.RTL {
				gap = l.Words[i-1].Box.X0 - l.Words[i].Box.X1
			}
			if gap > 2*l.Words[i].Size {
				return 0
			}
		}
	}
	// A bullet starts a list item, whatever its size.
	if first := []rune(b.Lines[0].Words[0].Text); len(first) == 1 && strings.ContainsRune(bullets, first[0]) {
		return 0
	}
	textSize := false
	for _, t := range m.text {
		textSize = textSize || size == t
	}
	if m.body > 0 && size >= m.body*headingRatio && !textSize {
		// Rank by size among the sizes the document uses for headings.
		for i, h := range m.headings {
			// The smallest size of a merged group stands for the group.
			if size >= h || i+1 == len(m.headings) || size > m.headings[i+1]*1.08 {
				return min(i+1, 6)
			}
		}
		return min(len(m.headings)+1, 6)
	}
	// Bold at body size: a run-in or minor heading. It ends without the full
	// stop a bold sentence would have, and it is one line or two.
	if !m.boldBody && boldChars == chars && size >= m.body*0.95 && len(b.Lines) <= 2 && chars <= 120 &&
		last != '.' && last != ',' && last != ';' && last != ':' {
		return min(len(m.headings)+1, 6)
	}
	return 0
}

// bullets are the characters that start an item of a bulleted list.
const bullets = "•◦▪▫●○■□‣⁃∙·➢➤►▶✓✔–—-*"

// listMarker reports whether word, the first word of a line, is a list
// marker. certain is false for markers that are also common at the start of
// ordinary sentences, such as "A." or "I.": those count only next to other
// list items.
func listMarker(word string) (ordered, certain, ok bool) {
	r := []rune(word)
	if len(r) == 1 && strings.ContainsRune(bullets, r[0]) {
		return false, true, true
	}
	// Ordered: "1." "1)" "(1)" "a." "a)" "(a)" "iv." and the like.
	s := word
	open := strings.HasPrefix(s, "(")
	s = strings.TrimPrefix(s, "(")
	if len(s) < 2 {
		return false, false, false
	}
	closer := s[len(s)-1]
	if closer != '.' && closer != ')' || (open && closer != ')') {
		return false, false, false
	}
	body := s[:len(s)-1]
	digits, letters := 0, 0
	for _, c := range body {
		switch {
		case c >= '0' && c <= '9':
			digits++
		case unicode.IsLetter(c) && c < 0x80:
			letters++
		default:
			return false, false, false
		}
	}
	switch {
	case digits > 0 && letters == 0 && digits <= 3:
		return true, true, true
	case letters == 1 && digits == 0:
		// A single letter with a closing bracket is a marker; with a full
		// stop it may be an initial.
		return true, closer == ')', true
	case letters > 1 && letters <= 6 && digits == 0 && strings.Trim(strings.ToLower(body), "ivxlcdm") == "":
		return true, false, true // roman numerals, or a short word such as "mix."
	}
	return false, false, false
}

// markerShape reduces a list marker to its style: "(a)" and "(b)" have the
// same shape, "a." and "1." do not.
func markerShape(marker string) string {
	var b strings.Builder
	for _, r := range marker {
		switch {
		case r >= '0' && r <= '9':
			r = '1'
		case unicode.IsUpper(r):
			r = 'A'
		case unicode.IsLower(r):
			r = 'a'
		}
		if n := b.Len(); n == 0 || rune(b.String()[n-1]) != r || (r != '1' && r != 'A' && r != 'a') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// classify sorts a page's blocks into headings, list items and paragraphs.
// A block that holds several list items, or a paragraph followed by list
// items, is split so that each item is a block of its own.
func classify(blocks []Block, m *styleModel, titles []outlineTitle) []Block {
	out := make([]Block, 0, len(blocks))
	for _, b := range blocks {
		if b.Furniture || b.Angle != 0 || b.fixed {
			out = append(out, b)
			continue
		}
		// The document's own outline is the best evidence there is: a
		// short block with the text of an outline entry for this page is
		// that entry's heading, at the entry's depth.
		if level := outlineLevel(&b, titles); level > 0 {
			b.Kind, b.Level = Heading, level
			out = append(out, b)
			continue
		}
		// A numbered heading, "2. Results", starts like a list item. Size
		// and weight decide.
		// Where the document tags its headings, a block it tags as
		// something else is not one, however it is set.
		if level := m.headingLevel(&b); level > 0 && !(b.tagged && m.tagsHeadings) {
			b.Kind, b.Level = Heading, level
			out = append(out, b)
			continue
		}
		out = append(out, splitList(b)...)
	}

	// Markers that could also be ordinary words, "A." or "I.", count only
	// beside an item numbered in the same style.
	for i := range out {
		if out[i].Kind != ListItem || out[i].Level >= 0 || out[i].fixed {
			continue
		}
		shape := markerShape(out[i].Marker)
		near := (i > 0 && out[i-1].Kind == ListItem && markerShape(out[i-1].Marker) == shape) ||
			(i+1 < len(out) && out[i+1].Kind == ListItem && markerShape(out[i+1].Marker) == shape)
		if near {
			out[i].Level = 0
		} else {
			out[i].Kind, out[i].Marker, out[i].Level = Paragraph, "", 0
		}
	}

	// List depth: an item set further right than the one before it is one
	// level down; back to an earlier indent is back to that level.
	var indents []float64
	for i := range out {
		b := &out[i]
		if b.fixed && b.Kind == ListItem {
			continue // the document gives its depth
		}
		if b.Kind != ListItem {
			if !b.Furniture {
				indents = indents[:0]
			}
			continue
		}
		x, slack := b.Box.X0, 0.5*b.Lines[0].Words[0].Size
		for len(indents) > 0 && x < indents[len(indents)-1]-slack {
			indents = indents[:len(indents)-1]
		}
		if len(indents) == 0 || x > indents[len(indents)-1]+slack {
			indents = append(indents, x)
		}
		b.Level = len(indents) - 1
	}

	return out
}

// splitList splits a block at the lines that start with a list marker. An
// item whose marker is uncertain gets Level -1, for classify to settle.
func splitList(b Block) []Block {
	type cut struct {
		line    int
		marker  string
		certain bool
	}
	var cuts []cut
	for i, l := range b.Lines {
		if len(l.Words) < 2 || l.RTL {
			continue
		}
		// A row of asterisks or dashes is a separator, not a list.
		if second := []rune(l.Words[1].Text); len(second) == 1 && strings.ContainsRune(bullets, second[0]) {
			continue
		}
		if _, certain, ok := listMarker(l.Words[0].Text); ok {
			cuts = append(cuts, cut{i, l.Words[0].Text, certain})
		}
	}
	if len(cuts) == 0 {
		return []Block{b}
	}
	// Several uncertain markers in one block vouch for each other.
	if len(cuts) > 1 {
		for i := range cuts {
			cuts[i].certain = true
		}
	}
	var out []Block
	part := func(from, to int) Block {
		p := Block{Lines: b.Lines[from:to:to], Angle: b.Angle, Box: b.Lines[from].Box}
		for _, l := range p.Lines[1:] {
			p.Box.X0, p.Box.Y0 = math.Min(p.Box.X0, l.Box.X0), math.Min(p.Box.Y0, l.Box.Y0)
			p.Box.X1, p.Box.Y1 = math.Max(p.Box.X1, l.Box.X1), math.Max(p.Box.Y1, l.Box.Y1)
		}
		return p
	}
	if cuts[0].line > 0 {
		out = append(out, part(0, cuts[0].line))
	}
	for i, c := range cuts {
		end := len(b.Lines)
		if i+1 < len(cuts) {
			end = cuts[i+1].line
		}
		item := part(c.line, end)
		item.Kind, item.Marker = ListItem, c.marker
		if !c.certain {
			item.Level = -1
		}
		out = append(out, item)
	}
	return out
}

// outlineTitle is an outline entry reduced to what a heading on the page is
// compared with.
type outlineTitle struct {
	text  string // letters and digits only, lower case
	depth int    // 0 for a top-level entry
}

// plainKey reduces text to its letters and digits, in lower case, so that
// spacing, punctuation and case do not matter in a comparison.
func plainKey(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// outlineLevel returns the heading level that the outline gives a block, or
// 0 when no outline entry for the page matches it. Section numbers may be
// printed on the page and missing from the outline, or the other way round.
func outlineLevel(b *Block, titles []outlineTitle) int {
	if len(titles) == 0 || len(b.Lines) > maxHeadingLines {
		return 0
	}
	key := plainKey(b.Text())
	if key == "" || len(key) > maxHeadingChars {
		return 0
	}
	bare := strings.TrimLeft(key, "0123456789")
	for _, t := range titles {
		if t.text == "" {
			continue
		}
		if t.text == key || (bare != "" && (t.text == bare || strings.TrimLeft(t.text, "0123456789") == bare)) {
			return min(t.depth+1, 6)
		}
	}
	return 0
}

// outlineTitles returns the outline entries that lead to page n. The
// outline is read once per document.
func (d *Document) outlineTitles(ctx context.Context, n int) ([]outlineTitle, error) {
	if d.titles == nil {
		items, err := d.Outline(ctx)
		if err != nil {
			return nil, err
		}
		index := map[int][]outlineTitle{}
		var walk func(items []OutlineItem, depth int)
		walk = func(items []OutlineItem, depth int) {
			for _, it := range items {
				if it.Page > 0 {
					index[it.Page] = append(index[it.Page], outlineTitle{plainKey(it.Title), depth})
				}
				walk(it.Children, depth+1)
			}
		}
		walk(items, 0)
		d.titles = index
	}
	return d.titles[n], nil
}
