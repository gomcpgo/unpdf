// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

// Package cjk implements the predefined CMaps that composite fonts can name
// as their encoding, and the mapping from CIDs to Unicode for Adobe's
// Chinese, Japanese and Korean character collections.
//
// The tables are built from Adobe's cmap-resources by gen.go and embedded in
// compressed form; a collection is unpacked the first time a font needs it.
package cjk

import (
	"bytes"
	"compress/flate"
	"embed"
	"encoding/binary"
	"io"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

//go:generate go run gen.go ../../../cmap-resources

//go:embed data/*.bin
var dataFS embed.FS

// legacyMap is the code-to-CID table of one legacy CMap.
type legacyMap struct {
	keys []uint64   // length<<32 | code, ascending
	cids []uint32   // parallel to keys
	lens [256]uint8 // for each first byte, a bit set of the code lengths that start with it
}

type mapRef struct {
	count int
	body  []byte
}

type collection struct {
	uni     []rune // CID -> Unicode, 0 for none
	refs    map[string]mapRef
	mu      sync.Mutex
	maps    map[string]*legacyMap
	inverse map[rune]uint32
}

var (
	collMu      sync.Mutex
	collections = map[string]*collection{}
)

// loadCollection returns the named collection ("Adobe-Japan1" and so on), or
// nil when there is no data for it.
func loadCollection(name string) *collection {
	collMu.Lock()
	defer collMu.Unlock()
	if c, ok := collections[name]; ok {
		return c
	}
	c := readCollection(name)
	collections[name] = c
	return c
}

func readCollection(name string) *collection {
	packed, err := dataFS.ReadFile("data/" + name + ".bin")
	if err != nil {
		return nil
	}
	raw, err := io.ReadAll(flate.NewReader(bytes.NewReader(packed)))
	if err != nil || !bytes.HasPrefix(raw, []byte("UCJK1")) {
		return nil
	}
	r := raw[5:]
	next := func() uint64 {
		v, n := binary.Uvarint(r)
		if n <= 0 {
			r = nil
			return 0
		}
		r = r[n:]
		return v
	}
	c := &collection{refs: map[string]mapRef{}, maps: map[string]*legacyMap{}}
	c.uni = make([]rune, next())
	prev := rune(0)
	for i := range c.uni {
		v := next()
		if v == 0 {
			continue
		}
		prev += rune(unzigzag(v - 1))
		c.uni[i] = prev
	}
	for n := next(); n > 0; n-- {
		nameLen := int(next())
		if nameLen > len(r) {
			break
		}
		mapName := string(r[:nameLen])
		r = r[nameLen:]
		count, size := int(next()), int(next())
		if size > len(r) {
			break
		}
		c.refs[mapName] = mapRef{count: count, body: r[:size]}
		r = r[size:]
	}
	return c
}

func unzigzag(v uint64) int64 { return int64(v>>1) ^ -int64(v&1) }

func (c *collection) legacy(name string) *legacyMap {
	c.mu.Lock()
	defer c.mu.Unlock()
	if m, ok := c.maps[name]; ok {
		return m
	}
	ref, ok := c.refs[name]
	if !ok {
		return nil
	}
	m := &legacyMap{keys: make([]uint64, 0, ref.count), cids: make([]uint32, 0, ref.count)}
	r := ref.body
	prevN, prevCode, prevCID := 0, uint32(0), int64(0)
	for len(r) > 0 {
		n := int(r[0])
		r = r[1:]
		d, k := binary.Uvarint(r)
		if k <= 0 {
			break
		}
		r = r[k:]
		z, k := binary.Uvarint(r)
		if k <= 0 {
			break
		}
		r = r[k:]
		if n != prevN {
			prevN, prevCode = n, 0
		}
		prevCode += uint32(d)
		prevCID += unzigzag(z)
		m.keys = append(m.keys, uint64(n)<<32|uint64(prevCode))
		m.cids = append(m.cids, uint32(prevCID))
		if n >= 1 && n <= 4 {
			m.lens[byte(prevCode>>(8*uint(n-1)))] |= 1 << uint(n)
		}
	}
	c.maps[name] = m
	return m
}

func (c *collection) cidFor(r rune) (uint32, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inverse == nil {
		c.inverse = make(map[rune]uint32, len(c.uni))
		for cid := len(c.uni) - 1; cid >= 0; cid-- {
			if u := c.uni[cid]; u != 0 {
				c.inverse[u] = uint32(cid) // the lowest CID for a character wins
			}
		}
	}
	cid, ok := c.inverse[r]
	return cid, ok
}

type kind uint8

const (
	identity kind = iota
	legacy
	ucs2
	utf16be
	utf8enc
	utf32be
)

// CMap is a predefined CMap.
type CMap struct {
	// Vertical reports whether the CMap selects vertical writing.
	Vertical bool
	// Collection is the character collection of the CIDs, such as
	// "Adobe-Japan1". It is empty for the Identity CMaps.
	Collection string

	kind kind
	coll *collection
	leg  *legacyMap
}

// Predefined returns the predefined CMap with the given name, such as
// "Identity-H", "90ms-RKSJ-H" or "UniGB-UTF16-V".
func Predefined(name string) (*CMap, bool) {
	base := name
	m := &CMap{}
	switch {
	case name == "H" || name == "V":
		base, m.Vertical = "H", name == "V"
	case strings.HasSuffix(name, "-V"):
		base, m.Vertical = strings.TrimSuffix(name, "-V"), true
	case strings.HasSuffix(name, "-H"):
		base = strings.TrimSuffix(name, "-H")
	}
	if base == "Identity" {
		return m, true
	}

	if strings.HasPrefix(base, "Uni") {
		switch {
		case strings.HasPrefix(base, "UniJIS"):
			m.Collection = "Adobe-Japan1"
		case strings.HasPrefix(base, "UniGB"):
			m.Collection = "Adobe-GB1"
		case strings.HasPrefix(base, "UniCNS"):
			m.Collection = "Adobe-CNS1"
		case strings.HasPrefix(base, "UniKS"):
			m.Collection = "Adobe-Korea1"
		case strings.HasPrefix(base, "UniAKR"):
			m.Collection = "Adobe-KR"
		default:
			return nil, false
		}
		switch {
		case strings.Contains(base, "-UCS2"):
			m.kind = ucs2
		case strings.Contains(base, "-UTF16"):
			m.kind = utf16be
		case strings.Contains(base, "-UTF8"):
			m.kind = utf8enc
		case strings.Contains(base, "-UTF32"):
			m.kind = utf32be
		default:
			return nil, false
		}
		m.coll = loadCollection(m.Collection)
		return m, true
	}

	collName, ok := legacyCollection[base]
	if !ok {
		// Columns for CMaps with no vertical variant keep their -H suffix.
		if collName, ok = legacyCollection[name]; !ok {
			return nil, false
		}
		base = name
	}
	m.Collection, m.kind = collName, legacy
	m.coll = loadCollection(collName)
	if m.coll == nil {
		return nil, false
	}
	if m.leg = m.coll.legacy(base); m.leg == nil {
		return nil, false
	}
	return m, true
}

// Next splits the next character code off s and returns it with its length
// in bytes.
func (m *CMap) Next(s []byte) (code uint32, n int) {
	if len(s) == 0 {
		return 0, 0
	}
	take := func(n int) (uint32, int) {
		n = min(n, len(s))
		var v uint32
		for _, b := range s[:n] {
			v = v<<8 | uint32(b)
		}
		return v, n
	}
	switch m.kind {
	case identity, ucs2:
		return take(2)
	case utf32be:
		return take(4)
	case utf16be:
		if len(s) >= 4 && s[0] >= 0xD8 && s[0] <= 0xDB && s[2] >= 0xDC && s[2] <= 0xDF {
			return take(4)
		}
		return take(2)
	case utf8enc:
		switch {
		case s[0] < 0x80:
			return take(1)
		case s[0] < 0xE0:
			return take(2)
		case s[0] < 0xF0:
			return take(3)
		}
		return take(4)
	}
	// A legacy CMap: try the code lengths that can start with this byte,
	// longest first, and take the first that the CMap defines.
	lens := m.leg.lens[s[0]]
	shortest := 0
	for n := 4; n >= 1; n-- {
		if lens&(1<<uint(n)) == 0 {
			continue
		}
		shortest = n
		if n > len(s) {
			continue
		}
		if code, _ := take(n); m.leg.has(code, n) {
			return code, n
		}
	}
	if shortest == 0 {
		shortest = 1
	}
	return take(shortest)
}

func (l *legacyMap) find(code uint32, n int) (uint32, bool) {
	key := uint64(n)<<32 | uint64(code)
	i := sort.Search(len(l.keys), func(i int) bool { return l.keys[i] >= key })
	if i < len(l.keys) && l.keys[i] == key {
		return l.cids[i], true
	}
	return 0, false
}

func (l *legacyMap) has(code uint32, n int) bool {
	_, ok := l.find(code, n)
	return ok
}

// Unicode returns the character that a code stands for, when the CMap's
// codes are themselves a Unicode encoding.
func (m *CMap) Unicode(code uint32, n int) (rune, bool) {
	var r rune
	switch m.kind {
	case ucs2, utf32be:
		r = rune(code)
	case utf16be:
		r = rune(code)
		if n == 4 {
			hi, lo := rune(code>>16), rune(code&0xFFFF)
			r = (hi-0xD800)<<10 + (lo - 0xDC00) + 0x10000
		}
	case utf8enc:
		var b [4]byte
		for i := 0; i < n; i++ {
			b[i] = byte(code >> (8 * uint(n-1-i)))
		}
		var size int
		r, size = utf8.DecodeRune(b[:n])
		if r == utf8.RuneError && size <= 1 {
			return 0, false
		}
	default:
		return 0, false
	}
	if r <= 0 || r > 0x10FFFF || (r >= 0xD800 && r <= 0xDFFF) {
		return 0, false
	}
	return r, true
}

// CID returns the CID that a code selects.
func (m *CMap) CID(code uint32, n int) (uint32, bool) {
	switch m.kind {
	case identity:
		return code, true
	case legacy:
		return m.leg.find(code, n)
	}
	// A Unicode CMap. Its exact table is not embedded; the collection's
	// CID for the character is right except where a CMap picks a variant
	// glyph, which matters for glyph widths only.
	if r, ok := m.Unicode(code, n); ok && m.coll != nil {
		return m.coll.cidFor(r)
	}
	return 0, false
}

// ToUnicode returns the character for a CID of the collection named by a
// CIDSystemInfo dictionary's Registry and Ordering, such as "Adobe" and
// "Japan1".
func ToUnicode(registry, ordering string, cid uint32) (rune, bool) {
	if registry != "Adobe" {
		return 0, false
	}
	switch ordering {
	case "Japan1", "GB1", "CNS1", "Korea1", "KR":
	default:
		return 0, false
	}
	c := loadCollection("Adobe-" + ordering)
	if c == nil || int(cid) >= len(c.uni) || c.uni[cid] == 0 {
		return 0, false
	}
	return c.uni[cid], true
}
