// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package font

import "encoding/binary"

// maxGlyphMappings caps the entries read from a font's character map.
const maxGlyphMappings = 1 << 17

// glyphUnicode reads the character map of a TrueType or OpenType font
// program backwards: for each glyph, the Unicode character that selects it.
//
// It is the last resort for a composite font that says nothing about
// Unicode itself. The font program of such a font often still has its
// character map, because the program that subset the font left it in. Where
// several characters share a glyph, the lowest wins. The result is nil when
// the font has no Unicode character map.
func glyphUnicode(data []byte) map[uint16]rune {
	u16 := func(off int) int {
		if off < 0 || off+2 > len(data) {
			return -1
		}
		return int(binary.BigEndian.Uint16(data[off:]))
	}
	u32 := func(off int) int {
		if off < 0 || off+4 > len(data) {
			return -1
		}
		v := binary.BigEndian.Uint32(data[off:])
		if v > 1<<30 {
			return -1
		}
		return int(v)
	}

	// The table directory: find "cmap".
	numTables := u16(4)
	cmap := -1
	for i := 0; i < numTables && i < 256; i++ {
		rec := 12 + 16*i
		if rec+16 > len(data) {
			break
		}
		if string(data[rec:rec+4]) == "cmap" {
			cmap = u32(rec + 8)
		}
	}
	if cmap < 0 {
		return nil
	}

	// Pick the best Unicode subtable: full Unicode before the basic plane.
	best, bestRank := -1, 0
	for i, n := 0, u16(cmap+2); i < n && i < 64; i++ {
		rec := cmap + 4 + 8*i
		platform, encoding, off := u16(rec), u16(rec+2), u32(rec+4)
		if off < 0 {
			continue
		}
		rank := 0
		switch {
		case platform == 3 && encoding == 10, platform == 0 && (encoding == 4 || encoding == 6):
			rank = 3
		case platform == 3 && encoding == 1:
			rank = 2
		case platform == 0:
			rank = 1
		}
		if rank > bestRank {
			best, bestRank = cmap+off, rank
		}
	}
	if best < 0 {
		return nil
	}

	out := map[uint16]rune{}
	add := func(gid int, r rune) {
		if gid <= 0 || gid > 0xFFFF || r <= 0 || r > 0x10FFFF || (r >= 0xD800 && r <= 0xDFFF) || r == 0xFFFF {
			return
		}
		if old, ok := out[uint16(gid)]; !ok || r < old {
			out[uint16(gid)] = r
		}
	}
	switch u16(best) {
	case 4:
		segs := u16(best+6) / 2
		ends := best + 14
		starts := ends + 2*segs + 2
		deltas := starts + 2*segs
		offsets := deltas + 2*segs
		for i, total := 0, 0; i < segs; i++ {
			end, start, delta, off := u16(ends+2*i), u16(starts+2*i), u16(deltas+2*i), u16(offsets+2*i)
			if end < 0 || start < 0 || delta < 0 || off < 0 || start > end {
				continue
			}
			for c := start; c <= end; c++ {
				if total++; total > maxGlyphMappings {
					return out
				}
				gid := (c + delta) & 0xFFFF
				if off != 0 {
					// The offset is relative to where it is stored.
					gid = u16(offsets + 2*i + off + 2*(c-start))
					if gid > 0 {
						gid = (gid + delta) & 0xFFFF
					}
				}
				add(gid, rune(c))
			}
		}
	case 12:
		groups := u32(best + 12)
		for i, total := 0, 0; i < groups && i < 1<<16; i++ {
			rec := best + 16 + 12*i
			start, end, gid := u32(rec), u32(rec+4), u32(rec+8)
			if start < 0 || end < start || gid < 0 {
				continue
			}
			for c := start; c <= end; c++ {
				if total++; total > maxGlyphMappings {
					return out
				}
				add(gid+c-start, rune(c))
			}
		}
	case 6:
		first, count := u16(best+6), u16(best+8)
		for i := 0; i < count && i < 1<<16; i++ {
			add(u16(best+10+2*i), rune(first+i))
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
