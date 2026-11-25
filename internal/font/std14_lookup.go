// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package font

import (
	"sort"
	"strings"
)

// standardMetrics returns the width table of the standard font that name
// refers to, or nil. It also recognises the names under which the same
// designs usually appear: Arial for Helvetica, Times New Roman for Times.
// fixed reports a Courier, whose glyphs are all 600 units wide.
func standardMetrics(name string) (table []uint32, fixed bool) {
	lower := strings.ToLower(strings.NewReplacer(" ", "", ",", "", "-", "").Replace(name))
	bold := strings.Contains(lower, "bold")
	italic := strings.Contains(lower, "italic") || strings.Contains(lower, "oblique")
	switch {
	case strings.HasPrefix(lower, "courier"):
		return nil, true
	case strings.HasPrefix(lower, "helvetica") || strings.HasPrefix(lower, "arial"):
		switch {
		case bold && italic:
			return std14Widths["Helvetica-BoldOblique"], false
		case bold:
			return std14Widths["Helvetica-Bold"], false
		case italic:
			return std14Widths["Helvetica-Oblique"], false
		}
		return std14Widths["Helvetica"], false
	case strings.HasPrefix(lower, "times"):
		switch {
		case bold && italic:
			return std14Widths["Times-BoldItalic"], false
		case bold:
			return std14Widths["Times-Bold"], false
		case italic:
			return std14Widths["Times-Italic"], false
		}
		return std14Widths["Times-Roman"], false
	case strings.HasPrefix(lower, "symbol"):
		return std14Widths["Symbol"], false
	case strings.HasPrefix(lower, "zapfdingbats"):
		return std14Widths["ZapfDingbats"], false
	}
	return nil, false
}

// standardWidth looks a character up in a table from std14Widths.
func standardWidth(table []uint32, r rune) (float64, bool) {
	if r < 0 || r > 0xFFFF {
		return 0, false
	}
	key := uint32(r) << 16
	i := sort.Search(len(table), func(i int) bool { return table[i] >= key })
	if i < len(table) && table[i]>>16 == uint32(r) {
		return float64(table[i]&0xFFFF) / 1000, true
	}
	return 0, false
}
