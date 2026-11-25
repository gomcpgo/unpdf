// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package filter

// lzwDecode decodes the LZW variant PDF uses: codes are packed most
// significant bit first, start at 9 bits and grow to 12, code 256 clears the
// table and code 257 ends the data. With early set to 1 (the default) the
// code width grows one code sooner than in the plain algorithm.
//
// The standard library's compress/lzw cannot be used because it has no
// early-change mode.
func lzwDecode(data []byte, early, max int) (out []byte, partial bool, err error) {
	const (
		clear    = 256
		eod      = 257
		first    = 258
		maxWidth = 12
		size     = 1 << maxWidth
	)
	if early != 0 {
		early = 1
	}
	var (
		prefix [size]uint16 // code -> the code of everything but its last byte
		suffix [size]byte   // code -> its last byte
		head   [size]byte   // code -> its first byte
		length [size]uint16 // code -> its length in bytes
	)
	for i := 0; i < 256; i++ {
		suffix[i], head[i], length[i] = byte(i), byte(i), 1
	}

	width, next, prev := 9, first, -1
	var acc uint32
	bits := 0
	pos := 0
	for {
		for bits < width && pos < len(data) {
			acc = acc<<8 | uint32(data[pos])
			pos++
			bits += 8
		}
		if bits < width {
			// Ran out of input without an end-of-data code. Common enough
			// that it is not treated as damage.
			return out, false, nil
		}
		code := int(acc>>(bits-width)) & (1<<width - 1)
		bits -= width

		switch {
		case code == clear:
			width, next, prev = 9, first, -1
			continue
		case code == eod:
			return out, false, nil
		}

		if prev < 0 {
			if code > 255 {
				return out, true, nil
			}
			if len(out) >= max {
				return nil, false, ErrTooLarge
			}
			out = append(out, byte(code))
			prev = code
			continue
		}

		var n int
		switch {
		case code < next:
			n = int(length[code])
		case code == next && next < size:
			// The code being defined right now: previous string plus its
			// own first byte.
			n = int(length[prev]) + 1
		default:
			return out, true, nil
		}
		if len(out)+n > max {
			return nil, false, ErrTooLarge
		}

		// Add the new table entry before writing, so that code == next can
		// be expanded like any other code.
		firstByte := head[prev]
		if code < next {
			firstByte = head[code]
		}
		if next < size {
			prefix[next] = uint16(prev)
			suffix[next] = firstByte
			head[next] = head[prev]
			length[next] = length[prev] + 1
			next++
		}

		// Expand the code back to front.
		start := len(out)
		out = append(out, make([]byte, n)...)
		c := code
		for i := start + n - 1; i >= start; i-- {
			out[i] = suffix[c]
			c = int(prefix[c])
		}
		prev = code

		if next+early >= 1<<width && width < maxWidth {
			width++
		}
	}
}
