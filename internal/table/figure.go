// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package table

import (
	"math"
	"sort"
)

// Box is a rectangle of the page.
type Box struct {
	X0, Y0, X1, Y1 float64
}

const (
	// rowShare is the part of a row's height a shape must overlap to count
	// as running through that row.
	rowShare = 0.25
	// pictureShare is the part of a table a picture must lie under for the
	// table to be labels on that picture.
	pictureShare = 0.3
	// backdropShare and backdropOf describe a picture that is the
	// background of a table and nothing else: it lies under all of the
	// table, and the table takes up most of it.
	backdropShare, backdropOf = 0.95, 0.5
)

// Figure reports whether a table found by alignment is more likely the
// labels of a figure: the boxes of a diagram, the axes of a chart, the
// names on a map. Labels like these line up in rows and columns as the
// cells of a table do, and the text alone cannot tell the two apart. What
// is drawn around the text can.
//
// shapes are the pieces of drawing on the page that are no ruling: curves
// and lines that slant. A table is ruled with straight lines across and
// down, and what decorates it stays within a row: the rounded corner of a
// frame, a tick, a badge behind a word. A curve or a slanting line that
// runs through the text of two rows or more is the arc of a diagram or the
// line of a chart.
//
// pictures are the images on the page. A picture under a good part of the
// table makes the text labels on that picture, unless it lies under the
// whole table and is not much larger: a backdrop. The caller leaves out
// pictures that are the page itself, as on a scan.
func Figure(t *Table, shapes, pictures []Box) bool {
	if t.X1 <= t.X0 || t.Y1 <= t.Y0 {
		return false
	}
	if len(shapes) > 0 {
		// Where the text of each row lies, from the top down.
		type band struct{ y0, y1 float64 }
		at := make([]band, t.Rows)
		for r := range at {
			at[r] = band{math.Inf(1), math.Inf(-1)}
		}
		for _, c := range t.Cells {
			if c.Text == "" || c.RowSpan != 1 || c.Row < 0 || c.Row >= t.Rows {
				continue
			}
			at[c.Row].y0, at[c.Row].y1 = math.Min(at[c.Row].y0, c.Y0), math.Max(at[c.Row].y1, c.Y1)
		}
		bands := at[:0]
		for _, b := range at {
			if b.y1 > b.y0 {
				bands = append(bands, b)
			}
		}
		sort.Slice(bands, func(i, j int) bool { return bands[i].y0 < bands[j].y0 })
		// The rows of a table follow one another down the page, so the
		// search for the first row a shape reaches can be by halves.
		lowest := make([]float64, len(bands)) // the lowest bottom edge so far
		for i, b := range bands {
			lowest[i] = b.y1
			if i > 0 {
				lowest[i] = math.Max(lowest[i], lowest[i-1])
			}
		}
		for _, s := range shapes {
			if s.X1 < t.X0 || s.X0 > t.X1 || s.Y1 < t.Y0 || s.Y0 > t.Y1 {
				continue
			}
			through := 0
			for i := sort.SearchFloat64s(lowest, s.Y0); i < len(bands) && bands[i].y0 < s.Y1; i++ {
				b := bands[i]
				if math.Min(s.Y1, b.y1)-math.Max(s.Y0, b.y0) >= rowShare*(b.y1-b.y0) {
					if through++; through >= 2 {
						return true
					}
				}
			}
		}
	}
	area := (t.X1 - t.X0) * (t.Y1 - t.Y0)
	for _, p := range pictures {
		w := math.Min(p.X1, t.X1) - math.Max(p.X0, t.X0)
		h := math.Min(p.Y1, t.Y1) - math.Max(p.Y0, t.Y0)
		if w <= 0 || h <= 0 {
			continue
		}
		under := w * h / area
		if under < pictureShare {
			continue
		}
		if size := (p.X1 - p.X0) * (p.Y1 - p.Y0); under >= backdropShare && w*h >= backdropOf*size {
			continue
		}
		return true
	}
	return false
}
