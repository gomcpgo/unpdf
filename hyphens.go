// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package unpdf

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gomcpgo/unpdf/internal/layout"
)

// A hyphen at the end of a line, between two lower-case letters, is there
// for the line break or belongs to the word: "exam-" "ple", or "long-"
// "term". The two halves do not say which. The document does: it writes
// "long-term" within a line somewhere, and nowhere writes "longterm".
//
// Somewhere is the page and the pages around it, up to four before and four
// after: the pages that are read anyway to find running heads. So no page
// is read for this that would not be read otherwise, and what is kept of a
// page, a number for each of its words, is kept only as long as the page
// is. A compound the pages around do not write again is joined without its
// hyphen, as every such word was before.

// compoundReach is how many pages before and after a page are searched for
// a word written within a line.
const compoundReach = furnitureReach

// wordKey reduces a word to a number that is the same however the word is
// capitalised and whatever punctuation stands before or after it. It
// returns 0 for a word with no letter in it.
func wordKey(word string) uint64 {
	start, end := -1, 0
	for i, r := range word {
		if unicode.IsLetter(r) {
			if start < 0 {
				start = i
			}
			end = i + utf8.RuneLen(r)
		}
	}
	if start < 0 {
		return 0
	}
	// FNV-1a over the lower-case letters, with every kind of hyphen the
	// same and soft hyphens left out.
	h := uint64(14695981039346656037)
	for _, r := range word[start:end] {
		switch r {
		case 0x00AD:
			continue
		case 0x2010, 0x2011:
			r = '-'
		}
		r = unicode.ToLower(r)
		for ; r > 0; r >>= 8 {
			h = (h ^ uint64(r&0xff)) * 1099511628211
		}
	}
	if h == 0 {
		h = 1
	}
	return h
}

// wordKeys returns the key of every word on the page.
func (p *Page) wordKeys(ctx context.Context) ([]uint64, error) {
	if p.keyed {
		return p.keys, nil
	}
	blocks, err := p.layout(ctx, false)
	if err != nil {
		return nil, err
	}
	n := 0
	for _, b := range blocks {
		for _, l := range b.Lines {
			n += len(l.Words)
		}
	}
	keys := make([]uint64, 0, n)
	for _, b := range blocks {
		for _, l := range b.Lines {
			for _, w := range l.Words {
				if k := wordKey(w.Text); k != 0 {
					keys = append(keys, k)
				}
			}
		}
	}
	p.keys, p.keyed = keys, true
	return keys, nil
}

// wordsAround returns the words of the page and of the pages around it, a
// list of keys for each page. A page that cannot be read has no words.
func (p *Page) wordsAround(ctx context.Context) ([][]uint64, error) {
	if p.surveyed {
		return p.around, nil
	}
	total := p.doc.NumPages()
	var around [][]uint64
	for m := p.Number - compoundReach; m <= p.Number+compoundReach; m++ {
		if m < 1 || m > total {
			continue
		}
		q := p
		if m != p.Number {
			var err error
			if q, err = p.doc.Page(ctx, m); err != nil {
				if cerr := ctx.Err(); cerr != nil {
					return nil, cerr
				}
				continue
			}
		}
		keys, err := q.wordKeys(ctx)
		if err != nil {
			if cerr := ctx.Err(); cerr != nil {
				return nil, cerr
			}
			continue
		}
		if len(keys) > 0 {
			around = append(around, keys)
		}
	}
	p.around, p.surveyed = around, true
	return around, nil
}

// compound reports whether stem and next, the two halves of a word broken
// at a hyphen, are a compound that keeps its hyphen: among the words given,
// the two are written with a hyphen between them, and never as one word.
func compound(around [][]uint64, stem, next string) bool {
	with, without := wordKey(stem+"-"+next), wordKey(stem+next)
	if with == 0 || with == without {
		return false
	}
	seen := false
	for _, keys := range around {
		for _, k := range keys {
			if k == without {
				return false
			}
			seen = seen || k == with
		}
	}
	return seen
}

// broken reports whether last, the final word of a line, ends in a hyphen
// that may belong to it or to the line break, and returns the word without
// it. A soft hyphen is not in question: it is there for the break.
func broken(last, next string) (stem string, ok bool) {
	if strings.HasSuffix(last, "­") {
		return "", false
	}
	return layout.Hyphenated(last, next)
}

// markCompounds marks the lines of the page that end in the hyphen of a
// compound. The pages around are looked at only if the page has a line
// that ends in a hyphen that could be one.
func (p *Page) markCompounds(ctx context.Context, blocks []layout.Block) error {
	for _, b := range blocks {
		for j := 0; j+1 < len(b.Lines); j++ {
			cur, next := &b.Lines[j], &b.Lines[j+1]
			if cur.RTL || len(cur.Words) == 0 || len(next.Words) == 0 {
				continue
			}
			first := next.Words[0].Text
			stem, ok := broken(cur.Words[len(cur.Words)-1].Text, first)
			if !ok {
				continue
			}
			around, err := p.wordsAround(ctx)
			if err != nil {
				return err
			}
			cur.Compound = compound(around, stem, first)
		}
	}
	return nil
}

// cellCompounds joins, in the text of a table's cell, a compound broken at
// its hyphen over two lines of the cell: the second half moves up to the
// first, hyphen kept, so that the word is whole wherever the lines of the
// cell are put together.
func (p *Page) cellCompounds(ctx context.Context, text string) (string, error) {
	if !strings.Contains(text, "-\n") && !strings.Contains(text, "‐\n") {
		return text, nil
	}
	lines := strings.Split(text, "\n")
	for i := 0; i+1 < len(lines); {
		last := lines[i][strings.LastIndexByte(lines[i], ' ')+1:]
		first, rest, _ := strings.Cut(lines[i+1], " ")
		stem, ok := broken(last, first)
		if !ok {
			i++
			continue
		}
		around, err := p.wordsAround(ctx)
		if err != nil {
			return "", err
		}
		if !compound(around, stem, first) {
			i++
			continue
		}
		lines[i] += first
		if rest == "" {
			lines = append(lines[:i+1], lines[i+2:]...)
			continue // the word may go on over a third line
		}
		lines[i+1] = rest
		i++
	}
	return strings.Join(lines, "\n"), nil
}
