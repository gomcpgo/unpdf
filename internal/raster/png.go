// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package raster

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
)

// encoder writes a PNG file a row at a time. The standard library's encoder
// wants the whole image in memory as an image.Image, and cannot write grey
// below 8 bits; this one holds two rows and writes what it is given.
type encoder struct {
	out  bytes.Buffer
	idat idatWriter
	zw   *zlib.Writer
	// unit is the distance in bytes between a byte and the one it is
	// predicted from, or 0 when rows are written unfiltered.
	unit  int
	prev  []byte
	tries [5][]byte
}

// idatWriter collects compressed data and writes it out as IDAT chunks.
type idatWriter struct {
	out *bytes.Buffer
	buf []byte
}

const idatSize = 64 << 10

func (w *idatWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		k := min(len(p), idatSize-len(w.buf))
		w.buf = append(w.buf, p[:k]...)
		p = p[k:]
		if len(w.buf) == idatSize {
			w.flush()
		}
	}
	return n, nil
}

func (w *idatWriter) flush() {
	if len(w.buf) > 0 {
		chunk(w.out, "IDAT", w.buf)
		w.buf = w.buf[:0]
	}
}

// chunk writes one PNG chunk: its length, its type, its data and a checksum
// of the last two.
func chunk(out *bytes.Buffer, kind string, data []byte) {
	var word [4]byte
	binary.BigEndian.PutUint32(word[:], uint32(len(data)))
	out.Write(word[:])
	crc := crc32.NewIEEE()
	crc.Write([]byte(kind))
	crc.Write(data)
	out.WriteString(kind)
	out.Write(data)
	binary.BigEndian.PutUint32(word[:], crc.Sum32())
	out.Write(word[:])
}

func newEncoder(width, height, depth, colorType int, palette []byte) *encoder {
	e := &encoder{}
	e.out.WriteString("\x89PNG\r\n\x1a\n")
	var head [13]byte
	binary.BigEndian.PutUint32(head[0:], uint32(width))
	binary.BigEndian.PutUint32(head[4:], uint32(height))
	head[8], head[9] = byte(depth), byte(colorType)
	chunk(&e.out, "IHDR", head[:])
	if colorType == ctPalette {
		chunk(&e.out, "PLTE", palette)
	}
	e.idat = idatWriter{out: &e.out, buf: make([]byte, 0, idatSize)}
	// The default level. Measured on the pictures of real documents, the
	// fastest level saves a tenth of the time and costs up to a fifth more
	// in size: most of the time goes on choosing the filters below.
	e.zw = zlib.NewWriter(&e.idat)

	// Predicting a byte from its neighbours pays for samples of a byte or
	// more. Packed samples and palette indices compress better as they are.
	if depth >= 8 && colorType != ctPalette {
		channels := map[int]int{ctGray: 1, ctRGB: 3, ctGrayAlpha: 2, ctRGBA: 4}[colorType]
		e.unit = channels * depth / 8
	}
	return e
}

// row adds a row of the image.
func (e *encoder) row(cur []byte) {
	if e.unit == 0 {
		e.zw.Write([]byte{0})
		e.zw.Write(cur)
		return
	}
	if e.prev == nil {
		e.prev = make([]byte, len(cur))
		for i := range e.tries {
			e.tries[i] = make([]byte, 1+len(cur))
			e.tries[i][0] = byte(i)
		}
	}
	e.zw.Write(e.tries[e.filter(cur)])
	copy(e.prev, cur)
}

// filter fills in the five ways PNG has of predicting a row from the bytes
// before and above, and returns the one that leaves the least to encode, by
// the usual measure: the sum of the differences, taken as signed bytes.
func (e *encoder) filter(cur []byte) int {
	prev, unit := e.prev, e.unit
	none, sub, up, avg, paeth := e.tries[0][1:], e.tries[1][1:], e.tries[2][1:], e.tries[3][1:], e.tries[4][1:]
	copy(none, cur)
	for i, c := range cur {
		var left, above, corner int
		if i >= unit {
			left, corner = int(cur[i-unit]), int(prev[i-unit])
		}
		above = int(prev[i])
		sub[i] = c - byte(left)
		up[i] = c - byte(above)
		avg[i] = c - byte((left+above)/2)
		// Paeth: whichever of the three neighbours is nearest to
		// left + above - corner.
		p := left + above - corner
		pl, pa, pc := abs(p-left), abs(p-above), abs(p-corner)
		pick := corner
		if pl <= pa && pl <= pc {
			pick = left
		} else if pa <= pc {
			pick = above
		}
		paeth[i] = c - byte(pick)
	}
	best, least := 0, -1
	for f := range e.tries {
		sum := 0
		for _, b := range e.tries[f][1:] {
			sum += abs(int(int8(b)))
		}
		if least < 0 || sum < least {
			best, least = f, sum
		}
	}
	return best
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// finish ends the file and returns it.
func (e *encoder) finish() []byte {
	e.zw.Close()
	e.idat.flush()
	chunk(&e.out, "IEND", nil)
	return e.out.Bytes()
}
