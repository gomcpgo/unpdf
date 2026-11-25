// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

// Package font answers the two questions text extraction asks of a font:
// what Unicode text does this character code stand for, and how far does
// its glyph advance.
//
// It never fails. A font that cannot be understood still splits strings
// into codes and gives them a plausible width; its glyphs come back marked
// as unmapped, and the caller counts them.
package font

import (
	"context"
	"strings"

	"github.com/gomcpgo/unpdf/internal/cjk"
	"github.com/gomcpgo/unpdf/internal/cmap"
	"github.com/gomcpgo/unpdf/internal/glyphs"
	"github.com/gomcpgo/unpdf/internal/pdfobj"
	"github.com/gomcpgo/unpdf/internal/reader"
)

// maxProgramBytes caps the CMap programs and font programs that are parsed.
const maxProgramBytes = 16 << 20

// maxCachedGlyphs caps the per-font cache of decoded codes.
const maxCachedGlyphs = 1 << 16

// Glyph is one character code of a string, decoded.
type Glyph struct {
	// Text is the Unicode text of the glyph, with Latin ligatures expanded.
	// It is empty when the font gives no mapping; see Mapped.
	Text string
	// Mapped reports whether the font had a Unicode mapping for the code.
	Mapped bool
	// Missing reports that the font has a ToUnicode map which leaves this
	// code out. Such a glyph is Mapped, with empty Text: producers that
	// shape complex scripts give the text of a whole cluster to its first
	// glyph and nothing to the others, so the gap is deliberate more often
	// than not.
	Missing bool
	// Width is the glyph's advance in text space units, for a font size of
	// 1. For vertical fonts it is the downward advance, and negative.
	Width float64
	// Space reports whether this is the single-byte code 32, the only code
	// that word spacing applies to.
	Space bool
	// Bytes is the length of the code in the string.
	Bytes int
}

// Font is a font as text extraction sees it.
type Font struct {
	// Name is the PostScript name, without the subset tag.
	Name string
	// Subtype is Type1, MMType1, TrueType, Type3 or Type0.
	Subtype string
	// Style, as far as the font descriptor and the name reveal it.
	Bold, Italic, Fixed, Serif, Symbolic bool
	// Vertical reports whether the font writes top to bottom.
	Vertical bool
	// Unmapped counts the glyphs decoded so far that had no Unicode
	// mapping.
	Unmapped int

	simple *[256]Glyph // simple fonts: everything is precomputed

	// Composite fonts.
	predefined *cjk.CMap
	embedded   *cmap.CMap
	base       *cjk.CMap // what an embedded CMap builds on, through usecmap
	toUnicode  *cmap.CMap
	// codesAreUnicode is set by /ToUnicode /Identity-H: each code is the
	// character it stands for.
	codesAreUnicode bool
	registry        string
	ordering        string
	widths          []widthRange
	dw              float64
	cache           map[uint64]Glyph
	// glyphs maps glyph IDs to Unicode, read from the embedded TrueType
	// font program, and cidToGID maps CIDs to glyph IDs when that is not
	// the identity. Both are used only when nothing else gives a mapping.
	glyphs   map[uint16]rune
	cidToGID []byte
}

type widthRange struct {
	lo, hi uint32
	w      float64   // for a run of CIDs that share one width
	list   []float64 // or one width per CID
}

// Load reads a font dictionary.
func Load(ctx context.Context, f *reader.File, dict pdfobj.Dict) *Font {
	fnt := &Font{Subtype: string(f.Name(ctx, dict["Subtype"]))}
	fnt.setName(string(f.Name(ctx, dict["BaseFont"])))
	if fnt.Name == "" {
		fnt.setName(string(f.Name(ctx, dict["Name"])))
	}

	switch tu := resolve(ctx, f, dict["ToUnicode"]).(type) {
	case *pdfobj.Stream:
		if data, err := f.DecodeStream(ctx, tu); err == nil && len(data) <= maxProgramBytes {
			if cm, _ := cmap.Parse(data); cm != nil && cm.HasUnicode() {
				fnt.toUnicode = cm
			}
		}
	case pdfobj.Name:
		// Not in the specification, but producers write it: the codes are
		// already Unicode.
		fnt.codesAreUnicode = tu == "Identity-H" || tu == "Identity-V"
	}

	if fnt.Subtype == "Type0" {
		fnt.loadComposite(ctx, f, dict)
	} else {
		fnt.loadSimple(ctx, f, dict)
	}
	return fnt
}

// setName stores the font name without its "ABCDEF+" subset tag, and reads
// what it can of the style from it.
func (fnt *Font) setName(name string) {
	if len(name) > 7 && name[6] == '+' {
		tag := true
		for i := 0; i < 6; i++ {
			tag = tag && name[i] >= 'A' && name[i] <= 'Z'
		}
		if tag {
			name = name[7:]
		}
	}
	fnt.Name = name
	lower := strings.ToLower(name)
	for _, s := range []string{"bold", "black", "heavy", "semibold", "demi"} {
		fnt.Bold = fnt.Bold || strings.Contains(lower, s)
	}
	for _, s := range []string{"italic", "oblique", "-it", "slanted"} {
		fnt.Italic = fnt.Italic || strings.Contains(lower, s)
	}
	// Computer Modern names: cmbx is bold, cmti and cmsl are italic.
	if strings.HasPrefix(lower, "cmbx") || strings.HasPrefix(lower, "sfbx") {
		fnt.Bold = true
	}
	if strings.HasPrefix(lower, "cmti") || strings.HasPrefix(lower, "cmsl") || strings.HasPrefix(lower, "cmmi") {
		fnt.Italic = true
	}
	fnt.Fixed = strings.Contains(lower, "courier") || strings.Contains(lower, "mono") || strings.HasPrefix(lower, "cmtt")
}

// readDescriptor applies the font descriptor's flags.
func (fnt *Font) readDescriptor(ctx context.Context, f *reader.File, desc pdfobj.Dict) {
	if desc == nil {
		return
	}
	flags, _ := f.Int(ctx, desc["Flags"])
	fnt.Fixed = fnt.Fixed || flags&1 != 0
	fnt.Serif = flags&2 != 0
	fnt.Symbolic = flags&4 != 0
	fnt.Italic = fnt.Italic || flags&(1<<6) != 0
	fnt.Bold = fnt.Bold || flags&(1<<18) != 0
	if w, ok := f.Number(ctx, desc["FontWeight"]); ok && w >= 600 {
		fnt.Bold = true
	}
	if a, ok := f.Number(ctx, desc["ItalicAngle"]); ok && a != 0 {
		fnt.Italic = true
	}
}

// Next decodes the first character code of s. It returns a zero Glyph when
// s is empty; otherwise Bytes is at least 1.
func (fnt *Font) Next(s []byte) Glyph {
	if len(s) == 0 {
		return Glyph{}
	}
	var g Glyph
	if fnt.simple != nil {
		g = fnt.simple[s[0]]
	} else {
		g = fnt.nextComposite(s)
	}
	if !g.Mapped {
		fnt.Unmapped++
	}
	return g
}

// clean prepares mapped text: it drops mappings to nothing useful and
// expands the Latin ligatures, so that "ﬁ" reads as "fi".
func clean(s string) (string, bool) {
	if s == "" {
		return "", false
	}
	plain := true
	for _, r := range s {
		if r == 0 || r == 0xFFFD || r == 0xFFFE || r == 0xFFFF {
			return "", false
		}
		if r >= 0xFB00 && r <= 0xFEFC {
			plain = false
		}
	}
	if plain {
		return s, true
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == 0xFB00:
			b.WriteString("ff")
		case r == 0xFB01:
			b.WriteString("fi")
		case r == 0xFB02:
			b.WriteString("fl")
		case r == 0xFB03:
			b.WriteString("ffi")
		case r == 0xFB04:
			b.WriteString("ffl")
		case r == 0xFB05, r == 0xFB06:
			b.WriteString("st")
		case r >= 0xFE70 && r <= 0xFEFC:
			b.WriteString(arabicForm(r))
		default:
			if base, ok := arabicFormsA[r]; ok {
				b.WriteRune(base)
			} else {
				b.WriteRune(r)
			}
		}
	}
	return b.String(), true
}

// arabicLetters lists the letters of Arabic Presentation Forms-B from
// U+FE80 on, each with the number of positional forms it has there
// (isolated and final, or all four).
var arabicLetters = []struct {
	base  rune
	forms int
}{
	{0x0621, 1}, {0x0622, 2}, {0x0623, 2}, {0x0624, 2}, {0x0625, 2}, {0x0626, 4}, {0x0627, 2}, {0x0628, 4},
	{0x0629, 2}, {0x062A, 4}, {0x062B, 4}, {0x062C, 4}, {0x062D, 4}, {0x062E, 4}, {0x062F, 2}, {0x0630, 2},
	{0x0631, 2}, {0x0632, 2}, {0x0633, 4}, {0x0634, 4}, {0x0635, 4}, {0x0636, 4}, {0x0637, 4}, {0x0638, 4},
	{0x0639, 4}, {0x063A, 4}, {0x0641, 4}, {0x0642, 4}, {0x0643, 4}, {0x0644, 4}, {0x0645, 4}, {0x0646, 4},
	{0x0647, 4}, {0x0648, 2}, {0x0649, 2}, {0x064A, 4},
}

// arabicForm converts a character of Arabic Presentation Forms-B, a letter
// in one of its positional shapes or a lam-alef ligature, to the ordinary
// letters. Fonts' ToUnicode maps often name the shape, not the letter.
func arabicForm(r rune) string {
	switch {
	case r >= 0xFEF5:
		// Lam-alef ligatures, isolated and final: with madda, hamza above,
		// hamza below, plain.
		alef := [4]rune{0x0622, 0x0623, 0x0625, 0x0627}[(r-0xFEF5)/2]
		return string([]rune{0x0644, alef})
	case r >= 0xFE80:
		at := rune(0xFE80)
		for _, l := range arabicLetters {
			if r < at+rune(l.forms) {
				return string(l.base)
			}
			at += rune(l.forms)
		}
	case r >= 0xFE70 && r <= 0xFE7F:
		// Vowel marks: isolated at even code points, on a tatweel at odd
		// ones. U+FE73 and U+FE75 are not marks of this pattern.
		if r == 0xFE73 || r == 0xFE75 {
			return string(r)
		}
		marks := [8]rune{0x064B, 0x064C, 0x064D, 0x064E, 0x064F, 0x0650, 0x0651, 0x0652}
		mark := marks[(r-0xFE70)/2]
		if (r-0xFE70)%2 == 1 {
			return string([]rune{0x0640, mark})
		}
		return string(mark)
	}
	return string(r)
}

// arabicFormsA maps the positional forms of the commonest letters of
// Arabic Presentation Forms-A, the ones Persian and Urdu add, to the
// letters.
var arabicFormsA = func() map[rune]rune {
	m := map[rune]rune{}
	for _, l := range []struct{ first, base rune }{
		{0xFB56, 0x067E}, {0xFB66, 0x0679}, {0xFB7A, 0x0686}, {0xFB8E, 0x06A9}, {0xFB92, 0x06AF},
		{0xFBA6, 0x06C1}, {0xFBFC, 0x06CC},
	} {
		for i := rune(0); i < 4; i++ {
			m[l.first+i] = l.base
		}
	}
	for _, l := range []struct{ first, base rune }{{0xFB8A, 0x0698}, {0xFB88, 0x0688}, {0xFB8C, 0x0691}, {0xFBAE, 0x06D2}, {0xFB9E, 0x06BA}} {
		m[l.first], m[l.first+1] = l.base, l.base
	}
	return m
}()

// ---- Simple fonts ----

func (fnt *Font) loadSimple(ctx context.Context, f *reader.File, dict pdfobj.Dict) {
	desc := f.Dict(ctx, dict["FontDescriptor"])
	fnt.readDescriptor(ctx, f, desc)
	lower := strings.ToLower(fnt.Name)
	zapf := strings.Contains(lower, "zapfdingbats") || strings.Contains(lower, "dingbats")
	symbol := lower == "symbol" || strings.HasPrefix(lower, "symbol,") || strings.HasPrefix(lower, "symbolmt")

	// The base encoding: what a code means where nothing overrides it.
	var base *[256]rune
	var names [256]string // glyph names from /Differences or the font program
	baseName := ""
	switch enc := resolve(ctx, f, dict["Encoding"]).(type) {
	case pdfobj.Name:
		baseName = string(enc)
	case pdfobj.Dict:
		baseName = string(f.Name(ctx, enc["BaseEncoding"]))
		code := -1
		for _, item := range f.Array(ctx, enc["Differences"]) {
			switch v := resolve(ctx, f, item).(type) {
			case int64:
				code = int(v)
			case pdfobj.Name:
				if code >= 0 && code < 256 {
					names[code] = string(v)
				}
				code++
			}
		}
	}
	if t, ok := glyphs.Encoding(baseName); ok {
		base = t
	} else {
		// No named encoding: the font's own. For the two symbol fonts that
		// is known; for an embedded Type 1 font it can be read from the
		// font program; otherwise assume the usual one for the font type.
		fallback := "StandardEncoding"
		switch {
		case zapf:
			fallback = "ZapfDingbatsEncoding"
		case symbol:
			fallback = "SymbolEncoding"
		case fnt.Subtype == "TrueType":
			fallback = "WinAnsiEncoding"
		}
		if fnt.Subtype != "Type3" {
			base, _ = glyphs.Encoding(fallback)
		}
		if st := f.Stream(ctx, desc["FontFile"]); st != nil && !symbol && !zapf {
			if data, err := f.DecodeStream(ctx, st); err == nil && len(data) <= maxProgramBytes {
				if builtin, ok := type1Encoding(data); ok {
					for code, name := range builtin {
						if names[code] == "" {
							names[code] = name
						}
					}
				}
			}
		}
	}

	// Widths.
	scale := 0.001
	if fnt.Subtype == "Type3" {
		if m := f.Array(ctx, dict["FontMatrix"]); len(m) >= 1 {
			if a, ok := f.Number(ctx, m[0]); ok && a != 0 {
				scale = a
			}
		}
	}
	missing := 500.0
	if fnt.Fixed {
		missing = 600
	}
	if desc != nil {
		if w, ok := f.Number(ctx, desc["MissingWidth"]); ok && w > 0 {
			missing = w
		}
	}
	if fnt.Subtype == "Type3" {
		missing = 0
	}
	first, _ := f.Int(ctx, dict["FirstChar"])
	widths := f.Array(ctx, dict["Widths"])
	// A standard font may come without widths; its metrics are known.
	var standard []uint32
	if len(widths) == 0 && fnt.Subtype != "Type3" {
		var fixed bool
		if standard, fixed = standardMetrics(fnt.Name); fixed {
			missing = 600
		}
	}

	var table [256]Glyph
	for code := 0; code < 256; code++ {
		g := Glyph{Bytes: 1, Space: code == 32, Width: missing * scale}
		if i := int64(code) - first; i >= 0 && i < int64(len(widths)) {
			if w, ok := f.Number(ctx, widths[i]); ok {
				g.Width = w * scale
			}
		}
		if standard != nil {
			// By the character the code stands for in the font's encoding.
			var r rune
			if names[code] != "" {
				if s, ok := glyphs.Name(names[code], zapf); ok {
					r = []rune(s)[0]
				}
			} else if base != nil {
				r = base[code]
			}
			if w, ok := standardWidth(standard, r); ok {
				g.Width = w
			}
		}
		// Unicode: the ToUnicode CMap, then the glyph name, then the base
		// encoding.
		if fnt.toUnicode != nil {
			if s, ok := fnt.toUnicode.Unicode(uint32(code)); ok {
				g.Text, g.Mapped = clean(s)
			}
		}
		if !g.Mapped && fnt.codesAreUnicode && code >= 0x20 {
			g.Text, g.Mapped = clean(string(rune(code)))
		}
		if !g.Mapped && names[code] != "" {
			if s, ok := glyphs.Name(names[code], zapf); ok {
				g.Text, g.Mapped = clean(s)
			}
		}
		if !g.Mapped && names[code] == "" && base != nil && base[code] != 0 {
			g.Text, g.Mapped = clean(string(base[code]))
		}
		if !g.Mapped && fnt.toUnicode != nil {
			g.Mapped, g.Missing = true, true
		}
		table[code] = g
	}
	fnt.simple = &table
}

func resolve(ctx context.Context, f *reader.File, o pdfobj.Object) pdfobj.Object {
	v, err := f.Resolve(ctx, o)
	if err != nil {
		return nil
	}
	return v
}

// type1Encoding reads the encoding array from the clear-text part of a
// Type 1 font program: a run of "dup 65 /A put" lines.
func type1Encoding(data []byte) (map[int]string, bool) {
	// The encoding is in the clear-text header, well within this window.
	if len(data) > 1<<16 {
		data = data[:1<<16]
	}
	text := string(data)
	i := strings.Index(text, "/Encoding")
	if i < 0 {
		return nil, false
	}
	fields := strings.Fields(text[i+len("/Encoding"):])
	if len(fields) > 0 && fields[0] == "StandardEncoding" {
		return nil, false // the default already
	}
	out := map[int]string{}
	for j := 0; j+3 < len(fields); j++ {
		switch fields[j] {
		case "dup":
			code := 0
			valid := len(fields[j+1]) > 0 && len(fields[j+1]) <= 3
			for _, c := range fields[j+1] {
				if c < '0' || c > '9' {
					valid = false
				}
				code = code*10 + int(c-'0')
			}
			name := fields[j+2]
			if valid && code < 256 && strings.HasPrefix(name, "/") && fields[j+3] == "put" {
				out[code] = name[1:]
				j += 3
			}
		case "readonly", "def":
			if len(out) > 0 {
				return out, true
			}
		case "currentdict", "eexec":
			return out, len(out) > 0
		}
	}
	return out, len(out) > 0
}

// ---- Composite fonts ----

func (fnt *Font) loadComposite(ctx context.Context, f *reader.File, dict pdfobj.Dict) {
	fnt.cache = map[uint64]Glyph{}
	fnt.dw = 1

	if kids := f.Array(ctx, dict["DescendantFonts"]); len(kids) > 0 {
		cid := f.Dict(ctx, kids[0])
		fnt.readDescriptor(ctx, f, f.Dict(ctx, cid["FontDescriptor"]))
		if info := f.Dict(ctx, cid["CIDSystemInfo"]); info != nil {
			r, _ := f.String(ctx, info["Registry"])
			o, _ := f.String(ctx, info["Ordering"])
			fnt.registry, fnt.ordering = string(r), string(o)
		}
		if w, ok := f.Number(ctx, cid["DW"]); ok {
			fnt.dw = w / 1000
		}
		fnt.widths = readCIDWidths(ctx, f, f.Array(ctx, cid["W"]))

		// With no word about Unicode in the PDF, the embedded font program
		// may still have its character map.
		_, knownCollection := cjk.ToUnicode(fnt.registry, fnt.ordering, 1)
		if fnt.toUnicode == nil && !fnt.codesAreUnicode && !knownCollection {
			fd := f.Dict(ctx, cid["FontDescriptor"])
			if st := f.Stream(ctx, fd["FontFile2"]); st != nil {
				if data, err := f.DecodeStream(ctx, st); err == nil && len(data) <= maxProgramBytes {
					fnt.glyphs = glyphUnicode(data)
				}
			}
			if st := f.Stream(ctx, cid["CIDToGIDMap"]); st != nil && fnt.glyphs != nil {
				if data, err := f.DecodeStream(ctx, st); err == nil && len(data) <= 1<<17 {
					fnt.cidToGID = data
				}
			}
		}
	}

	switch enc := resolve(ctx, f, dict["Encoding"]).(type) {
	case pdfobj.Name:
		if m, ok := cjk.Predefined(string(enc)); ok {
			fnt.predefined = m
		}
	case *pdfobj.Stream:
		if data, err := f.DecodeStream(ctx, enc); err == nil && len(data) <= maxProgramBytes {
			if cm, _ := cmap.Parse(data); cm != nil && (cm.HasCIDs() || len(cm.Space) > 0) {
				fnt.embedded = cm
				use := cm.UseCMap
				if n := f.Name(ctx, enc.Dict["UseCMap"]); n != "" {
					use = string(n)
				}
				if m, ok := cjk.Predefined(use); ok {
					fnt.base = m
				}
				fnt.Vertical = cm.WMode == 1
				if w, ok := f.Int(ctx, enc.Dict["WMode"]); ok {
					fnt.Vertical = w == 1
				}
			}
		}
	}
	if fnt.predefined == nil && fnt.embedded == nil {
		// Unknown or unreadable: two-byte codes that are their own CIDs is
		// by far the commonest arrangement.
		fnt.predefined, _ = cjk.Predefined("Identity-H")
	}
	if fnt.predefined != nil {
		fnt.Vertical = fnt.predefined.Vertical
		if fnt.predefined.Collection != "" {
			fnt.registry = "Adobe"
			fnt.ordering = strings.TrimPrefix(fnt.predefined.Collection, "Adobe-")
		}
	}
}

// readCIDWidths reads a /W array, which mixes two forms:
// "first [w1 w2 ...]" and "first last w".
func readCIDWidths(ctx context.Context, f *reader.File, w pdfobj.Array) []widthRange {
	var out []widthRange
	for i := 0; i+1 < len(w) && len(out) < 1<<16; {
		first, ok := f.Int(ctx, w[i])
		if !ok || first < 0 || first > 1<<24 {
			i++
			continue
		}
		if list := f.Array(ctx, w[i+1]); list != nil {
			r := widthRange{lo: uint32(first), hi: uint32(first) + uint32(len(list)) - 1, list: make([]float64, len(list))}
			for j, x := range list {
				v, _ := f.Number(ctx, x)
				r.list[j] = v / 1000
			}
			if len(list) > 0 {
				out = append(out, r)
			}
			i += 2
			continue
		}
		if i+2 >= len(w) {
			break
		}
		last, ok1 := f.Int(ctx, w[i+1])
		width, ok2 := f.Number(ctx, w[i+2])
		if ok1 && ok2 && last >= first && last <= 1<<24 {
			out = append(out, widthRange{lo: uint32(first), hi: uint32(last), w: width / 1000})
		}
		i += 3
	}
	return out
}

func (fnt *Font) cidWidth(cid uint32) float64 {
	for i := len(fnt.widths) - 1; i >= 0; i-- {
		r := &fnt.widths[i]
		if cid < r.lo || cid > r.hi {
			continue
		}
		if r.list != nil {
			return r.list[cid-r.lo]
		}
		return r.w
	}
	return fnt.dw
}

func (fnt *Font) nextComposite(s []byte) Glyph {
	var code uint32
	var n int
	switch {
	case fnt.embedded != nil && len(fnt.embedded.Space) > 0:
		code, n = cmap.Next(fnt.embedded.Space, s)
	case fnt.base != nil:
		code, n = fnt.base.Next(s)
	case fnt.predefined != nil:
		code, n = fnt.predefined.Next(s)
	default:
		code, n = uint32(s[0]), 1
	}
	key := uint64(n)<<32 | uint64(code)
	if g, ok := fnt.cache[key]; ok {
		return g
	}

	g := Glyph{Bytes: n, Space: n == 1 && code == 32}
	cid, haveCID := uint32(0), false
	if fnt.embedded != nil {
		cid, haveCID = fnt.embedded.CID(code, n)
		if !haveCID && fnt.base != nil {
			cid, haveCID = fnt.base.CID(code, n)
		}
	} else if fnt.predefined != nil {
		cid, haveCID = fnt.predefined.CID(code, n)
	}

	g.Width = fnt.dw
	if haveCID {
		g.Width = fnt.cidWidth(cid)
	}
	if fnt.Vertical {
		// Vertical glyphs advance downwards by one em unless /W2 says
		// otherwise, which is rare enough to leave for later.
		g.Width = -1
	}

	// Unicode: the ToUnicode CMap, then a Unicode-encoded CMap, then the
	// character collection of the CID.
	if fnt.toUnicode != nil {
		if s, ok := fnt.toUnicode.Unicode(code); ok {
			g.Text, g.Mapped = clean(s)
		}
	}
	if !g.Mapped && fnt.codesAreUnicode && code >= 0x20 && code <= 0x10FFFF && (code < 0xD800 || code > 0xDFFF) {
		g.Text, g.Mapped = clean(string(rune(code)))
	}
	if !g.Mapped {
		for _, m := range []*cjk.CMap{fnt.predefined, fnt.base} {
			if m == nil {
				continue
			}
			if r, ok := m.Unicode(code, n); ok {
				g.Text, g.Mapped = clean(string(r))
				break
			}
		}
	}
	if !g.Mapped && haveCID {
		if r, ok := cjk.ToUnicode(fnt.registry, fnt.ordering, cid); ok {
			g.Text, g.Mapped = clean(string(r))
		}
	}

	if !g.Mapped && haveCID && fnt.glyphs != nil && cid <= 0xFFFF {
		gid := uint16(cid)
		if fnt.cidToGID != nil {
			gid = 0
			if i := 2 * int(cid); i+1 < len(fnt.cidToGID) {
				gid = uint16(fnt.cidToGID[i])<<8 | uint16(fnt.cidToGID[i+1])
			}
		}
		if r, ok := fnt.glyphs[gid]; ok {
			g.Text, g.Mapped = clean(string(r))
		}
	}
	if !g.Mapped && fnt.toUnicode != nil {
		g.Mapped, g.Missing = true, true
	}

	if len(fnt.cache) >= maxCachedGlyphs {
		clear(fnt.cache)
	}
	fnt.cache[key] = g
	return g
}

// Cache loads fonts and remembers the ones that are indirect objects, so
// that a font used on every page is read once.
type Cache struct {
	f     *reader.File
	byNum map[int]*Font
}

// NewCache returns an empty cache for the fonts of f.
func NewCache(f *reader.File) *Cache {
	return &Cache{f: f, byNum: map[int]*Font{}}
}

// maxCachedFonts bounds the cache. A document with more fonts than this
// only loses speed.
const maxCachedFonts = 512

// Get returns the font that o refers to, or nil when o is not a font
// dictionary.
func (c *Cache) Get(ctx context.Context, o pdfobj.Object) *Font {
	ref, isRef := o.(pdfobj.Ref)
	if isRef {
		if fnt, ok := c.byNum[ref.Num]; ok {
			return fnt
		}
	}
	dict := c.f.Dict(ctx, o)
	if dict == nil {
		return nil
	}
	fnt := Load(ctx, c.f, dict)
	if isRef && ctx.Err() == nil {
		if len(c.byNum) >= maxCachedFonts {
			clear(c.byNum)
		}
		c.byNum[ref.Num] = fnt
	}
	return fnt
}

// All returns the fonts loaded so far.
func (c *Cache) All() []*Font {
	out := make([]*Font, 0, len(c.byNum))
	for _, fnt := range c.byNum {
		out = append(out, fnt)
	}
	return out
}
