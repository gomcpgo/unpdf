// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package unpdf

import (
	"context"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/gomcpgo/unpdf/internal/layout"
)

// MarkdownOptions adjusts Markdown output. The zero value gives every page,
// without running heads and page numbers, with a marker before each page.
type MarkdownOptions struct {
	// Pages selects the pages to convert, as a list of numbers and ranges
	// such as "1-5,8,12-". An open range runs to the last page. Empty
	// means every page.
	Pages string
	// Furniture keeps running heads, running feet and page numbers, which
	// are left out by default.
	Furniture bool
	// OmitPageMarkers leaves out the comment that precedes each page,
	// "<!-- page 3 -->". The markers let a reader of the output cite the
	// page a passage came from.
	OmitPageMarkers bool
	// MaxBytes stops the conversion once the output has reached about this
	// many bytes, so that a huge document cannot produce a huge result. The
	// output then ends with a comment that says where it stopped. Zero
	// means no limit.
	MaxBytes int
	// MinTableConfidence is the confidence, as in Table.Confidence, from
	// which a table is shown as a table. Tables found with less stay lines
	// of text. Zero means DefaultTableConfidence. A negative value shows
	// every table that is found, and a value above 1 shows none.
	MinTableConfidence float64
}

// DefaultTableConfidence is the confidence from which Markdown shows a table
// as a table when MarkdownOptions.MinTableConfidence is not set.
const DefaultTableConfidence = 0.5

// tableConfidence returns the confidence from which tables are shown.
func (o MarkdownOptions) tableConfidence() float64 {
	if o.MinTableConfidence == 0 {
		return DefaultTableConfidence
	}
	return o.MinTableConfidence
}

// Markdown converts the document to Markdown: headings, paragraphs with
// their lines joined, list items, tables and links.
//
// Structure is inferred from the layout. A heading is a short block set
// larger or bolder than the body text, or one that matches an entry of the
// document outline; its level follows its size. A table is shown as a table
// when Page.Tables finds it with a confidence of at least 0.5, and as lines
// of text otherwise. Its header rows, or its first row where they are not
// known, become the header row: see Table.Markdown. A table that runs on
// over a page break is continued under the same header.
func (d *Document) Markdown(ctx context.Context, opts MarkdownOptions) (string, error) {
	var b strings.Builder
	if err := d.WriteMarkdown(ctx, &b, opts); err != nil {
		return "", err
	}
	return b.String(), nil
}

// WriteMarkdown writes the document to w as Markdown, a page at a time. See
// Markdown.
func (d *Document) WriteMarkdown(ctx context.Context, w io.Writer, opts MarkdownOptions) error {
	selected, err := parsePages(opts.Pages)
	if err != nil {
		return err
	}
	written := 0
	first := true
	var carry *tableTail // the table the last page ended with
	last := 0            // the last page written
	for page, err := range d.Pages(ctx) {
		if err != nil {
			return err
		}
		if !selected(page.Number) {
			continue
		}
		if page.Number != last+1 {
			carry = nil
		}
		last = page.Number
		text, joins, tail, err := page.markdown(ctx, opts, carry)
		if err != nil {
			return err
		}
		carry = tail
		var out strings.Builder
		if !first && !joins {
			out.WriteString("\n")
		}
		if !opts.OmitPageMarkers {
			fmt.Fprintf(&out, "<!-- page %d -->\n\n", page.Number)
		}
		if text != "" {
			out.WriteString(text)
			out.WriteString("\n")
		}
		if out.Len() == 0 || (opts.OmitPageMarkers && text == "") {
			continue
		}
		if opts.MaxBytes > 0 && written > 0 && written+out.Len() > opts.MaxBytes {
			_, err := fmt.Fprintf(w, "\n<!-- output limit reached: stopped before page %d -->\n", page.Number)
			return err
		}
		n, err := io.WriteString(w, out.String())
		if err != nil {
			return err
		}
		written += n
		first = false
	}
	return ctx.Err()
}

// parsePages turns a page list such as "1-5,8,12-" into a test for page
// numbers.
func parsePages(spec string) (func(int) bool, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return func(int) bool { return true }, nil
	}
	type span struct{ lo, hi int }
	var spans []span
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		lo, hi, isRange := strings.Cut(part, "-")
		number := func(s string, empty int) (int, bool) {
			s = strings.TrimSpace(s)
			if s == "" {
				return empty, true
			}
			n, err := strconv.Atoi(s)
			return n, err == nil && n >= 1
		}
		a, ok1 := number(lo, 1)
		b := a
		ok2 := true
		if isRange {
			b, ok2 = number(hi, 1<<31-1)
		}
		if part == "" || part == "-" || !ok1 || !ok2 || b < a {
			return nil, fmt.Errorf("unpdf: invalid page list %q: %q is not a page number or range", spec, part)
		}
		spans = append(spans, span{a, b})
	}
	return func(n int) bool {
		for _, s := range spans {
			if n >= s.lo && n <= s.hi {
				return true
			}
		}
		return false
	}, nil
}

// token is a word on its way into Markdown, with the link it is part of.
type token struct {
	text string
	link int // index into the page's links, or -1
}

// tableTail describes the table a page ends with, for the page after it.
type tableTail struct {
	header  []string
	numeric []bool // which cells of its last row hold figures
	width   float64
}

// continues reports whether the table a page starts with, given by its
// header and by all its rows, carries on the table the page before ended
// with: the same number of columns, about the same width, and either the
// same header again or a first row that holds figures where the row before
// the break held figures.
func (t *tableTail) continues(header []string, rows [][]string, width float64) (repeated, ok bool) {
	if t == nil || len(rows) == 0 || len(rows[0]) != len(t.header) || math.Abs(width-t.width) > 0.05*t.width {
		return false, false
	}
	if slices.Equal(header, t.header) {
		return true, true
	}
	same, figures := 0, 0
	for c, cell := range rows[0] {
		n := numericCell(cell)
		if n == t.numeric[c] {
			same++
		}
		if n {
			figures++
		}
	}
	return false, figures > 0 && same*5 >= len(t.header)*4
}

func numericCell(s string) bool {
	digits, letters := 0, 0
	for _, r := range s {
		switch {
		case unicode.IsDigit(r):
			digits++
		case unicode.IsLetter(r):
			letters++
		}
	}
	return digits > 0 && digits >= letters
}

// markdown renders one page. carry describes the table the page before
// ended with. It returns the page's text, whether that text continues the
// last table of the page before with no break, and the table this page
// ends with.
func (p *Page) markdown(ctx context.Context, opts MarkdownOptions, carry *tableTail) (string, bool, *tableTail, error) {
	blocks, err := p.Blocks(ctx)
	if err != nil {
		return "", false, nil, err
	}
	links, err := p.Links(ctx)
	if err != nil {
		return "", false, nil, err
	}
	// Only links that lead somewhere a reader of Markdown can follow.
	var web []Link
	for _, l := range links {
		if l.URI != "" {
			web = append(web, l)
		}
	}
	all, err := p.Tables(ctx)
	if err != nil {
		return "", false, nil, err
	}
	var tables []Table
	least := opts.tableConfidence()
	for _, t := range all {
		if t.Confidence >= least {
			tables = append(tables, t)
		}
	}
	tableAt := func(w Box) int {
		for k := range tables {
			if inside(w, tables[k].Box) {
				return k
			}
		}
		return -1
	}

	var out strings.Builder
	prevList, wrote, joins := false, false, false
	var tail *tableTail
	write := func(text string, list bool) {
		if strings.TrimSpace(text) == "" {
			return
		}
		if wrote {
			// List items sit together; everything else is set apart.
			if list && prevList {
				out.WriteString("\n")
			} else {
				out.WriteString("\n\n")
			}
		}
		out.WriteString(text)
		wrote, prevList, tail = true, list, nil
	}
	writeTable := func(t *Table) {
		header, body := t.headed()
		if header == nil {
			return
		}
		// Every row as it stands, for a part of a table that goes on from
		// the page before and has no header of its own.
		rows := t.rows()
		width := t.Box.X1 - t.Box.X0
		text := ""
		if repeated, ok := carry.continues(header, rows, width); ok && !wrote {
			// The table the page before ended with goes on here.
			header = carry.header
			if !repeated {
				body = rows
			}
			if opts.OmitPageMarkers {
				// Nothing stands between the two parts: add the rows.
				text, joins = pipeTable(nil, body), true
			}
		}
		if text == "" {
			text = pipeTable(header, body)
		}
		write(text, false)
		tail = &tableTail{header: header, width: width, numeric: make([]bool, len(header))}
		for c, cell := range rows[len(rows)-1] {
			tail.numeric[c] = numericCell(cell)
		}
	}
	shown := make([]bool, len(tables))
	for i := range blocks {
		b := &blocks[i]
		if b.Furniture && !opts.Furniture {
			continue
		}
		if len(tables) == 0 {
			write(renderBlock(b, web), b.Kind == ListItem)
			continue
		}
		// The words that are part of a table are left out here; the table
		// takes their place, where the first of them stood.
		rest := Block{Angle: b.Angle, Kind: b.Kind, Level: b.Level, Marker: b.Marker}
		whole := true
		flush := func() {
			if len(rest.Lines) == 0 {
				return
			}
			if whole && len(rest.Lines) == len(b.Lines) {
				write(renderBlock(b, web), b.Kind == ListItem)
			} else {
				rest.Box = rest.Lines[0].Box
				for _, l := range rest.Lines[1:] {
					rest.Box.X0, rest.Box.Y0 = math.Min(rest.Box.X0, l.Box.X0), math.Min(rest.Box.Y0, l.Box.Y0)
					rest.Box.X1, rest.Box.Y1 = math.Max(rest.Box.X1, l.Box.X1), math.Max(rest.Box.Y1, l.Box.Y1)
				}
				write(renderBlock(&rest, web), rest.Kind == ListItem)
			}
			rest.Lines, rest.Kind, rest.Level, rest.Marker = nil, Paragraph, 0, ""
		}
		for _, l := range b.Lines {
			kept := l.Words[:0:0]
			for _, w := range l.Words {
				k := tableAt(w.Box)
				if k < 0 || b.Angle != 0 {
					kept = append(kept, w)
					continue
				}
				whole = false
				if !shown[k] {
					flush()
					writeTable(&tables[k])
					shown[k] = true
				}
			}
			switch {
			case len(kept) == len(l.Words):
				rest.Lines = append(rest.Lines, l)
			case len(kept) > 0:
				part := Line{Words: kept, RTL: l.RTL, Box: kept[0].Box}
				for _, w := range kept[1:] {
					part.Box.X0, part.Box.Y0 = math.Min(part.Box.X0, w.Box.X0), math.Min(part.Box.Y0, w.Box.Y0)
					part.Box.X1, part.Box.Y1 = math.Max(part.Box.X1, w.Box.X1), math.Max(part.Box.Y1, w.Box.Y1)
				}
				rest.Lines = append(rest.Lines, part)
				rest.Kind, rest.Level, rest.Marker = Paragraph, 0, ""
			}
		}
		flush()
	}
	if !wrote {
		if scanned, err := p.Scanned(ctx); err != nil {
			return "", false, nil, err
		} else if scanned {
			return "<!-- this page is a scanned image with no text layer -->", false, nil, nil
		}
	}
	return out.String(), joins, tail, nil
}

// renderBlock writes one block as Markdown.
func renderBlock(b *Block, web []Link) string {
	lines := make([][]token, len(b.Lines))
	for j, l := range b.Lines {
		for _, w := range l.Words {
			t := token{text: w.Text, link: -1}
			for k := range web {
				if inside(w.Box, web[k].Box) {
					t.link = k
					break
				}
			}
			lines[j] = append(lines[j], t)
		}
	}
	// Join words hyphenated across line ends.
	for j := 0; j+1 < len(lines); j++ {
		cur, next := lines[j], lines[j+1]
		if len(cur) == 0 || len(next) == 0 || b.Lines[j].RTL {
			continue
		}
		if stem, ok := layout.Hyphenated(cur[len(cur)-1].text, next[0].text); ok {
			cur[len(cur)-1].text = stem + next[0].text
			lines[j+1] = next[1:]
		}
	}

	switch {
	case b.Kind == Heading:
		return strings.Repeat("#", max(1, min(b.Level, 6))) + " " + inline(flatten(lines), web)
	case b.Kind == ListItem:
		toks := flatten(lines)
		marker := "- "
		if len(toks) > 0 && b.Marker != "" {
			first := toks[0].text
			toks = toks[1:]
			if n := strings.Trim(first, "()."); isDigits(n) {
				marker = n + ". "
			} else if ordered, _, _ := listMarker(first); ordered {
				// Markdown numbers its lists with digits only: keep a
				// letter or roman numeral as part of the text.
				marker = "- " + first + " "
			}
		}
		return strings.Repeat("    ", max(0, b.Level)) + marker + inline(toks, web)
	case isProse(b):
		return escapeStart(inline(flatten(lines), web))
	}
	// Lines that do not run on: an address, a row of figures.
	var rows []string
	for _, l := range lines {
		if len(l) > 0 {
			rows = append(rows, escapeStart(inline(l, web)))
		}
	}
	return strings.Join(rows, "\n")
}

func flatten(lines [][]token) []token {
	var out []token
	for _, l := range lines {
		out = append(out, l...)
	}
	return out
}

func isDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}

// inline joins tokens with spaces, wrapping each run of tokens that share a
// link in Markdown link syntax.
func inline(toks []token, links []Link) string {
	var b strings.Builder
	for i := 0; i < len(toks); {
		if i > 0 {
			b.WriteByte(' ')
		}
		j := i + 1
		for j < len(toks) && toks[j].link == toks[i].link {
			j++
		}
		if toks[i].link < 0 {
			j = i + 1
		}
		var words []string
		for _, t := range toks[i:j] {
			words = append(words, t.text)
		}
		text := strings.Join(words, " ")
		if k := toks[i].link; k >= 0 {
			text = strings.NewReplacer("[", "\\[", "]", "\\]").Replace(text)
			uri := links[k].URI
			if strings.ContainsAny(uri, " ()<>") {
				uri = "<" + strings.NewReplacer("<", "%3C", ">", "%3E").Replace(uri) + ">"
			}
			text = "[" + text + "](" + uri + ")"
		}
		b.WriteString(text)
		i = j
	}
	return b.String()
}

// isProse reports whether a block's lines run on from one to the next, as
// the lines of a paragraph do, so that they can be joined. They do when
// most lines but the last reach the full width of the block, the lines hold
// several words each, and few are spaced out like the rows of a table. An
// address, whose lines happen to be of a length, fails on the second count.
// The lines of a table of contents reach the full width by their dot
// leaders, and each is an entry of its own; one line with a leader is the
// end of a sentence that leads to a box or a figure.
func isProse(b *Block) bool {
	if len(b.Lines) < 2 {
		return true
	}
	led := 0
	for _, l := range b.Lines {
		if leaderLine(l) {
			led++
		}
	}
	if led >= 2 && led*3 >= len(b.Lines) {
		return false
	}
	width := b.Box.X1 - b.Box.X0
	if b.Angle == 90 || b.Angle == 270 {
		width = b.Box.Y1 - b.Box.Y0
	}
	full, spaced, words := 0, 0, 0
	for i, l := range b.Lines {
		if i < len(b.Lines)-1 {
			words += len(l.Words)
		}
		lw := l.Box.X1 - l.Box.X0
		if b.Angle == 90 || b.Angle == 270 {
			lw = l.Box.Y1 - l.Box.Y0
		}
		if i < len(b.Lines)-1 && lw >= 0.8*width {
			full++
		}
		for j := 1; j < len(l.Words) && b.Angle == 0; j++ {
			prev, cur := l.Words[j-1], l.Words[j]
			gap := cur.Box.X0 - prev.Box.X1
			if l.RTL {
				gap = prev.Box.X0 - cur.Box.X1
			}
			if gap > 3*cur.Size {
				spaced++
				break
			}
		}
	}
	return full*10 >= (len(b.Lines)-1)*6 && spaced*10 < len(b.Lines)*3 && words*2 >= (len(b.Lines)-1)*9
}

// leaderLine reports a line with a row of dots in it that leads the eye
// across, as from a title to its page number: four dots or more, in one
// word or set apart. An ellipsis has three.
func leaderLine(l Line) bool {
	dots := 0
	for _, w := range l.Words {
		if strings.Trim(w.Text, ".") != "" {
			dots = 0
			continue
		}
		if dots += len(w.Text); dots >= 4 {
			return true
		}
	}
	return false
}

// escapeStart keeps a paragraph that happens to start like a heading, a
// list item or a quotation from being read as one.
func escapeStart(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	switch {
	case r[0] == '#' || r[0] == '>':
		return "\\" + s
	case (r[0] == '-' || r[0] == '+' || r[0] == '*') && len(r) > 1 && unicode.IsSpace(r[1]):
		return "\\" + s
	}
	// "12. text" would become a numbered list.
	i := 0
	for i < len(r) && r[i] >= '0' && r[i] <= '9' {
		i++
	}
	if i > 0 && i+1 < len(r) && (r[i] == '.' || r[i] == ')') && unicode.IsSpace(r[i+1]) {
		return string(r[:i]) + "\\" + string(r[i:])
	}
	return s
}
