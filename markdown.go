// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package unpdf

import (
	"context"
	"fmt"
	"io"
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
}

// Markdown converts the document to Markdown: headings, paragraphs with
// their lines joined, list items, and links.
//
// Structure is inferred from the layout. A heading is a short block set
// larger or bolder than the body text, or one that matches an entry of the
// document outline; its level follows its size. Tables are not recognised
// yet, and come out as lines of text.
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
	for page, err := range d.Pages(ctx) {
		if err != nil {
			return err
		}
		if !selected(page.Number) {
			continue
		}
		text, err := page.markdown(ctx, opts)
		if err != nil {
			return err
		}
		var out strings.Builder
		if !first {
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

// markdown renders one page.
func (p *Page) markdown(ctx context.Context, opts MarkdownOptions) (string, error) {
	blocks, err := p.Blocks(ctx)
	if err != nil {
		return "", err
	}
	links, err := p.Links(ctx)
	if err != nil {
		return "", err
	}
	// Only links that lead somewhere a reader of Markdown can follow.
	var web []Link
	for _, l := range links {
		if l.URI != "" {
			web = append(web, l)
		}
	}

	var out strings.Builder
	prevList := false
	wrote := false
	for i := range blocks {
		b := &blocks[i]
		if b.Furniture && !opts.Furniture {
			continue
		}
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

		var text string
		switch {
		case b.Kind == Heading:
			text = strings.Repeat("#", max(1, min(b.Level, 6))) + " " + inline(flatten(lines), web)
		case b.Kind == ListItem:
			toks := flatten(lines)
			marker := "- "
			if len(toks) > 0 {
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
			text = strings.Repeat("    ", max(0, b.Level)) + marker + inline(toks, web)
		case isProse(b):
			text = escapeStart(inline(flatten(lines), web))
		default:
			// Lines that do not run on: an address, a row of figures.
			var rows []string
			for _, l := range lines {
				if len(l) > 0 {
					rows = append(rows, escapeStart(inline(l, web)))
				}
			}
			text = strings.Join(rows, "\n")
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		if wrote {
			// List items sit together; everything else is set apart.
			if b.Kind == ListItem && prevList {
				out.WriteString("\n")
			} else {
				out.WriteString("\n\n")
			}
		}
		out.WriteString(text)
		wrote, prevList = true, b.Kind == ListItem
	}
	if !wrote {
		if scanned, err := p.Scanned(ctx); err != nil {
			return "", err
		} else if scanned {
			return "<!-- this page is a scanned image with no text layer -->", nil
		}
	}
	return out.String(), nil
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
func isProse(b *Block) bool {
	if len(b.Lines) < 2 {
		return true
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
