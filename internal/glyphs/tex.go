// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package glyphs

import "strings"

// texNames maps glyph names used by the Computer Modern and AMS fonts that
// the Adobe Glyph List does not know. Documents made with TeX that carry no
// ToUnicode maps, which is most older ones, name their mathematical symbols
// this way.
var texNames = map[string]rune{
	// Math italic.
	"epsilon1": 0x03F5, "rho1": 0x03F1, "pi1": 0x03D6, "tie": 0x2040, "lscript": 0x2113,
	"arrowhookleft": 0x21AA, "arrowhookright": 0x21A9,
	"arrowlefttophalf": 0x21BC, "arrowleftbothalf": 0x21BD, "arrowrighttophalf": 0x21C0, "arrowrightbothalf": 0x21C1,
	"flat": 0x266D, "natural": 0x266E, "sharp": 0x266F, "slurabove": 0x2322, "slurbelow": 0x2323,
	"star": 0x22C6, "triangleleft": 0x25C1, "triangleright": 0x25B7, "vector": 0x20D7, "weierstrass": 0x2118,
	"dotlessj": 0x0237,

	// Math symbols.
	"circlecopyrt": 0x25EF, "circledivide": 0x2298, "circledot": 0x2299, "circleminus": 0x2296,
	"diamondmath": 0x22C4, "equivasymptotic": 0x224D, "follows": 0x227B, "followsequal": 0x227D,
	"precedesequal": 0x227C, "greatermuch": 0x226B, "lessmuch": 0x226A, "bardbl": 0x2225,
	"negationslash": 0x0338, "owner": 0x220B, "triangle": 0x25B3, "triangleinv": 0x25BD,
	"Ifractur": 0x2111, "Rfractur": 0x211C, "angbracketleft": 0x27E8, "angbracketright": 0x27E9,
	"mapsto": 0x21A6, "prime": 0x2032, "minusplus": 0x2213,
	"arrowbothv": 0x2195, "arrowdblbothv": 0x21D5, "arrownortheast": 0x2197, "arrownorthwest": 0x2196,
	"arrowsoutheast": 0x2198, "arrowsouthwest": 0x2199,
	"ceilingleft": 0x2308, "ceilingright": 0x2309, "floorleft": 0x230A, "floorright": 0x230B,
	"coproduct": 0x2210, "intersectionsq": 0x2293, "unionsq": 0x2294, "unionmulti": 0x228E,
	"latticetop": 0x22A4, "similarequal": 0x2243, "subsetsqequal": 0x2291, "supersetsqequal": 0x2292,
	"turnstileleft": 0x22A2, "turnstileright": 0x22A3, "wreathproduct": 0x2240,
	"visiblespace": 0x2423, "openbullet": 0x25E6,

	// Extensible and large symbols. Size variants of these are found by
	// stripping the suffix; see texBase.
	"summation": 0x2211, "product": 0x220F, "contintegral": 0x222E,
	"hat": 0x0302, "tilde": 0x0303, "slash": 0x2215, "backslash": 0x2216,
	"circledottext": 0x2A00, "circleplustext": 0x2A01, "circlemultiplytext": 0x2A02,
	"circledotdisplay": 0x2A00, "circleplusdisplay": 0x2A01, "circlemultiplydisplay": 0x2A02,
	"parenlefttp": 0x239B, "parenleftex": 0x239C, "parenleftbt": 0x239D,
	"parenrighttp": 0x239E, "parenrightex": 0x239F, "parenrightbt": 0x23A0,
	"bracketlefttp": 0x23A1, "bracketleftex": 0x23A2, "bracketleftbt": 0x23A3,
	"bracketrighttp": 0x23A4, "bracketrightex": 0x23A5, "bracketrightbt": 0x23A6,
	"bracelefttp": 0x23A7, "braceleftmid": 0x23A8, "braceleftbt": 0x23A9, "braceex": 0x23AA,
	"bracerighttp": 0x23AB, "bracerightmid": 0x23AC, "bracerightbt": 0x23AD,

	// Text fonts. "suppress" is the stroke that turns l into the Polish ł.
	"suppress": 0x0337,
}

// texSuffixes are the size markers that the extension font appends to the
// name of an ordinary symbol.
var texSuffixes = []string{"bigg", "Bigg", "big", "Big", "display", "text", "widest", "wider", "wide"}

// texName resolves a name from the TeX fonts: either one listed above, or a
// size variant such as "parenleftBig" or "summationdisplay".
func texName(name string) (string, bool) {
	if r, ok := texNames[name]; ok {
		return string(r), true
	}
	for _, suffix := range texSuffixes {
		base, ok := strings.CutSuffix(name, suffix)
		if !ok || base == "" {
			continue
		}
		if r, ok := texNames[base]; ok {
			return string(r), true
		}
		if s, ok := glyphList[base]; ok {
			return s, true
		}
	}
	return "", false
}
