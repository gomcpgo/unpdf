// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package filter

import "errors"

// predict undoes the predictor that Flate and LZW streams may carry.
// Predictor 2 is TIFF horizontal differencing; 10 to 15 are the PNG filters,
// where every row starts with a byte naming the filter used for that row.
func predict(data []byte, p Params) ([]byte, error) {
	if p.Predictor <= 1 {
		return data, nil
	}
	colors, bpc, columns := p.Colors, p.BitsPerComponent, p.Columns
	if colors == 0 {
		colors = 1
	}
	if bpc == 0 {
		bpc = 8
	}
	if columns == 0 {
		columns = 1
	}
	if colors < 1 || colors > 256 || columns < 1 || columns > 1<<24 {
		return nil, errors.New("filter: predictor parameters out of range")
	}
	switch bpc {
	case 1, 2, 4, 8, 16:
	default:
		return nil, errors.New("filter: predictor bits per component out of range")
	}
	rowBits := int64(colors) * int64(bpc) * int64(columns)
	rowLen := int((rowBits + 7) / 8)
	bpp := (colors*bpc + 7) / 8 // bytes per pixel, at least 1
	if rowLen <= 0 || rowLen > 1<<28 {
		return nil, errors.New("filter: predictor row size out of range")
	}

	switch {
	case p.Predictor == 2:
		return tiffPredict(data, colors, bpc, rowLen), nil
	case p.Predictor >= 10 && p.Predictor <= 15:
		return pngPredict(data, rowLen, bpp), nil
	}
	return nil, errors.New("filter: unknown predictor")
}

// pngPredict works in place on the decoded rows and returns them with the
// per-row filter bytes removed. A short final row is decoded as far as it
// goes.
func pngPredict(data []byte, rowLen, bpp int) []byte {
	out := data[:0]
	prev := make([]byte, rowLen)
	for len(data) > 0 {
		ft := data[0]
		row := data[1:]
		if len(row) > rowLen {
			row = row[:rowLen]
		}
		data = data[1+len(row):]

		switch ft {
		case 1: // Sub
			for i := bpp; i < len(row); i++ {
				row[i] += row[i-bpp]
			}
		case 2: // Up
			for i := range row {
				row[i] += prev[i]
			}
		case 3: // Average
			for i := range row {
				left := 0
				if i >= bpp {
					left = int(row[i-bpp])
				}
				row[i] += byte((left + int(prev[i])) / 2)
			}
		case 4: // Paeth
			for i := range row {
				var a, c int
				if i >= bpp {
					a, c = int(row[i-bpp]), int(prev[i-bpp])
				}
				b := int(prev[i])
				pa, pb, pc := abs(b-c), abs(a-c), abs(a+b-2*c)
				pred := c
				if pa <= pb && pa <= pc {
					pred = a
				} else if pb <= pc {
					pred = b
				}
				row[i] += byte(pred)
			}
		}
		// Keep this row for the next one. A short row leaves the tail of
		// prev from the row before, which is the best guess available.
		copy(prev, row)
		// out trails data by at least one byte per row, so this never
		// overwrites input that is still to be read.
		out = append(out, row...)
	}
	return out
}

func tiffPredict(data []byte, colors, bpc, rowLen int) []byte {
	for start := 0; start < len(data); start += rowLen {
		row := data[start:min(start+rowLen, len(data))]
		switch bpc {
		case 8:
			for i := colors; i < len(row); i++ {
				row[i] += row[i-colors]
			}
		case 16:
			for i := 2 * colors; i+1 < len(row); i += 2 {
				v := uint16(row[i])<<8 | uint16(row[i+1])
				w := uint16(row[i-2*colors])<<8 | uint16(row[i-2*colors+1])
				v += w
				row[i], row[i+1] = byte(v>>8), byte(v)
			}
		default:
			// 1, 2 or 4 bits per component: samples are packed into bytes.
			mask := byte(1<<bpc - 1)
			samples := len(row) * 8 / bpc
			get := func(i int) byte {
				shift := 8 - bpc - (i*bpc)%8
				return row[i*bpc/8] >> shift & mask
			}
			set := func(i int, v byte) {
				shift := 8 - bpc - (i*bpc)%8
				row[i*bpc/8] = row[i*bpc/8]&^(mask<<shift) | (v&mask)<<shift
			}
			for i := colors; i < samples; i++ {
				set(i, get(i)+get(i-colors))
			}
		}
	}
	return data
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
