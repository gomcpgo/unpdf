// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package font

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/gomcpgo/unpdf/internal/pdftest"
)

// sfnt builds a font program holding nothing but a character map.
func sfnt(subtable []byte, platform, encoding uint16) []byte {
	be := binary.BigEndian
	cmap := be.AppendUint16(nil, 0)        // version
	cmap = be.AppendUint16(cmap, 1)        // one subtable
	cmap = be.AppendUint16(cmap, platform) // platform
	cmap = be.AppendUint16(cmap, encoding) // encoding
	cmap = be.AppendUint32(cmap, 12)       // offset of the subtable
	cmap = append(cmap, subtable...)
	font := be.AppendUint32(nil, 0x00010000) // TrueType
	font = be.AppendUint16(font, 1)          // one table
	font = append(font, 0, 0, 0, 0, 0, 0)    // search fields, unused here
	font = append(font, "cmap"...)
	font = be.AppendUint32(font, 0)  // checksum
	font = be.AppendUint32(font, 28) // offset
	font = be.AppendUint32(font, uint32(len(cmap)))
	return append(font, cmap...)
}

// format4 builds a segment-mapping subtable from (first code, last code,
// first glyph) triples.
func format4(segments ...[3]uint16) []byte {
	be := binary.BigEndian
	segments = append(segments, [3]uint16{0xFFFF, 0xFFFF, 0})
	n := len(segments)
	t := be.AppendUint16(nil, 4)
	t = be.AppendUint16(t, uint16(16+8*n))
	t = be.AppendUint16(t, 0) // language
	t = be.AppendUint16(t, uint16(2*n))
	t = append(t, 0, 0, 0, 0, 0, 0) // search fields
	for _, s := range segments {
		t = be.AppendUint16(t, s[1])
	}
	t = be.AppendUint16(t, 0)
	for _, s := range segments {
		t = be.AppendUint16(t, s[0])
	}
	for _, s := range segments {
		t = be.AppendUint16(t, s[2]-s[0]) // delta, modulo 65536
	}
	for range segments {
		t = be.AppendUint16(t, 0)
	}
	return t
}

func TestGlyphUnicode(t *testing.T) {
	// A to C are glyphs 5 to 7; the euro sign is glyph 9; glyph 5 is also
	// reached from a higher code point, and the lower one wins.
	m := glyphUnicode(sfnt(format4([3]uint16{'A', 'C', 5}, [3]uint16{0x20AC, 0x20AC, 9}, [3]uint16{0xF041, 0xF041, 5}), 3, 1))
	if len(m) != 4 || m[5] != 'A' || m[7] != 'C' || m[9] != 0x20AC || m[65535] != 0xFFFF && m[65535] != 0 {
		t.Errorf("format 4: %v", m)
	}

	// Format 12 covers characters outside the basic plane.
	be := binary.BigEndian
	f12 := be.AppendUint16(nil, 12)
	f12 = be.AppendUint16(f12, 0)
	f12 = be.AppendUint32(f12, 28)
	f12 = be.AppendUint32(f12, 0)
	f12 = be.AppendUint32(f12, 1)
	f12 = be.AppendUint32(f12, 0x1F600)
	f12 = be.AppendUint32(f12, 0x1F601)
	f12 = be.AppendUint32(f12, 40)
	if m := glyphUnicode(sfnt(f12, 3, 10)); len(m) != 2 || m[41] != 0x1F601 {
		t.Errorf("format 12: %v", m)
	}

	// A symbol-only character map is not Unicode.
	if m := glyphUnicode(sfnt(format4([3]uint16{0xF041, 0xF043, 5}), 3, 0)); m != nil {
		t.Errorf("symbol map used as Unicode: %v", m)
	}
	// Damage of every kind yields nothing, not a panic.
	good := sfnt(format4([3]uint16{'A', 'C', 5}), 3, 1)
	for cut := 0; cut < len(good); cut++ {
		glyphUnicode(good[:cut])
	}
	for i := range good {
		bad := append([]byte(nil), good...)
		bad[i] ^= 0xFF
		glyphUnicode(bad)
	}
	if glyphUnicode(nil) != nil || glyphUnicode([]byte("not a font at all")) != nil {
		t.Error("garbage yielded a map")
	}
}

func TestCompositeFontFromEmbeddedProgram(t *testing.T) {
	program := sfnt(format4([3]uint16{'A', 'Z', 36}), 3, 1)
	fnt := load(t, func(b *pdftest.Builder) string {
		file := b.AddStream("<< >>", program)
		// CIDs are not glyph IDs here: CID 1 is glyph 36 (A), CID 2 is
		// glyph 38 (C).
		gids := b.AddStream("<< >>", []byte{0, 0, 0, 36, 0, 38})
		cid := b.Add(fmt.Sprintf(`<< /Type /Font /Subtype /CIDFontType2 /BaseFont /ABCDEE+Arial /CIDToGIDMap %d 0 R
			/CIDSystemInfo << /Registry (Adobe) /Ordering (Identity) /Supplement 0 >>
			/FontDescriptor << /Type /FontDescriptor /Flags 32 /FontFile2 %d 0 R >> >>`, gids, file))
		return fmt.Sprintf("<< /Type /Font /Subtype /Type0 /BaseFont /ABCDEE+Arial /Encoding /Identity-H /DescendantFonts [%d 0 R] >>", cid)
	})
	text, _ := decode(fnt, []byte{0, 1, 0, 2, 0, 3})
	if text != "AC"+string(rune(0xFFFD)) || fnt.Unmapped != 1 {
		t.Errorf("text = %q, Unmapped = %d", text, fnt.Unmapped)
	}

	// Without a CIDToGIDMap stream, CIDs are glyph IDs.
	identity := load(t, func(b *pdftest.Builder) string {
		file := b.AddStream("<< >>", program)
		cid := b.Add(fmt.Sprintf(`<< /Type /Font /Subtype /CIDFontType2 /BaseFont /Arial /CIDToGIDMap /Identity
			/FontDescriptor << /Type /FontDescriptor /FontFile2 %d 0 R >> >>`, file))
		return fmt.Sprintf("<< /Type /Font /Subtype /Type0 /BaseFont /Arial /Encoding /Identity-H /DescendantFonts [%d 0 R] >>", cid)
	})
	if text, _ := decode(identity, []byte{0, 36, 0, 61}); text != "AZ" {
		t.Errorf("identity mapping: text = %q", text)
	}
}

func FuzzGlyphUnicode(f *testing.F) {
	f.Add(sfnt(format4([3]uint16{'A', 'C', 5}), 3, 1))
	f.Add(sfnt(format4([3]uint16{0, 0xFFFE, 1}), 0, 4))
	f.Fuzz(func(t *testing.T, data []byte) {
		for gid, r := range glyphUnicode(data) {
			if gid == 0 || r <= 0 || r > 0x10FFFF {
				t.Fatalf("bad mapping %d -> %U", gid, r)
			}
		}
	})
}
