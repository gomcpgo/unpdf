// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package unpdf

import (
	"context"
	"math"
	"strings"
	"unicode"

	"github.com/gomcpgo/unpdf/internal/layout"
)

// Page furniture is found by what makes it furniture: the same words in the
// same place on page after page. Every line near the top or bottom edge of
// a page is reduced to a signature that ignores the digits of a page
// number, and a line is furniture when its signature turns up on at least
// two of the pages around it, up to four before and four after.
//
// Comparing with neighbours, and not with the document as a whole, follows
// running heads that change from chapter to chapter. Looking four pages
// each way finds heads that alternate between left and right pages even on
// the first and last pages of a chapter, where all the pages with the same
// head lie to one side.

const (
	// furnitureBand is the share of the page height, at the top and at the
	// bottom, where furniture is looked for.
	furnitureBand = 0.12
	// furnitureStep is the vertical tolerance, in points, for "the same
	// place".
	furnitureStep = 10.0
	// furnitureReach is how many pages before and after a page it is
	// compared with.
	furnitureReach = 4
	// recentPages is how many pages are kept in memory so that reading a
	// document in order interprets each page once, although every page is
	// also looked at as a neighbour of the pages around it.
	recentPages = 2*furnitureReach + 2
	// maxBandCache bounds the per-page signature cache.
	maxBandCache = 512
)

// signature reduces a line to what it has in common with the same line on
// other pages. It returns "" for a line that is not near the top or bottom
// edge or has no text to speak of.
func signature(text string, box layout.Box, pageHeight float64, shift int) string {
	var edge byte
	var dist float64
	switch {
	case box.Y1 <= furnitureBand*pageHeight:
		edge, dist = 'T', box.Y0
	case box.Y0 >= (1-furnitureBand)*pageHeight:
		edge, dist = 'B', pageHeight-box.Y1
	default:
		return ""
	}
	var b strings.Builder
	digits := false
	for _, r := range strings.ToLower(text) {
		switch {
		case unicode.IsDigit(r):
			if !digits {
				b.WriteByte('#')
			}
			digits = true
		case unicode.IsSpace(r):
		default:
			b.WriteRune(r)
			digits = false
		}
	}
	norm := b.String()
	if norm == "" {
		return ""
	}
	// A page number in roman numerals.
	if strings.Trim(norm, "ivxlcdm") == "" && len(norm) <= 8 {
		norm = "#"
	}
	slot := int(math.Round(dist/furnitureStep)) + shift
	return string(edge) + string(rune('a'+max(0, min(slot, 25)))) + norm
}

// bands returns the signatures of the lines near the top and bottom edges
// of page n.
func (d *Document) bands(ctx context.Context, n int) (map[string]bool, error) {
	if sigs, ok := d.bandCache[n]; ok {
		return sigs, nil
	}
	page, err := d.Page(ctx, n)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr
		}
		return nil, nil // a page that cannot be read has no furniture
	}
	blocks, err := page.layout(ctx, false)
	if err != nil {
		return nil, err
	}
	_, height := page.Size()
	sigs := map[string]bool{}
	for _, b := range blocks {
		if b.Angle != 0 {
			continue
		}
		for _, l := range b.Lines {
			if sig := signature(l.Text(), l.Box, height, 0); sig != "" {
				sigs[sig] = true
			}
		}
	}
	if d.bandCache == nil || len(d.bandCache) >= maxBandCache {
		d.bandCache = map[int]map[string]bool{}
	}
	d.bandCache[n] = sigs
	return sigs, nil
}

// furnitureTest returns the function that says whether a line of page n is
// furniture, or nil when the document is too short for the question.
func (d *Document) furnitureTest(ctx context.Context, n int, pageHeight float64) (func(text string, box layout.Box) bool, error) {
	total := d.NumPages()
	if total < 3 {
		return nil, nil
	}
	var around []map[string]bool
	for m := n - furnitureReach; m <= n+furnitureReach; m++ {
		if m < 1 || m > total || m == n {
			continue
		}
		sigs, err := d.bands(ctx, m)
		if err != nil {
			return nil, err
		}
		if len(sigs) > 0 {
			around = append(around, sigs)
		}
	}
	if len(around) < 2 {
		return nil, nil
	}
	return func(text string, box layout.Box) bool {
		hits := 0
		for _, sigs := range around {
			// The neighbouring slots too: a line on a slot boundary falls to
			// either side from page to page.
			for shift := -1; shift <= 1; shift++ {
				if sig := signature(text, box, pageHeight, shift); sig != "" && sigs[sig] {
					hits++
					break
				}
			}
		}
		return hits >= 2
	}, nil
}

// remember keeps a page for a while, and returns the copy already kept if
// there is one, so that what was worked out for a page as a neighbour is
// not worked out again when the page itself is asked for.
func (d *Document) remember(p *Page) *Page {
	for _, r := range d.recent {
		if r.Number == p.Number {
			return r
		}
	}
	if len(d.recent) >= recentPages {
		copy(d.recent, d.recent[1:])
		d.recent = d.recent[:len(d.recent)-1]
	}
	d.recent = append(d.recent, p)
	return p
}
