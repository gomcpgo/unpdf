// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

// Package filter decodes PDF stream filters: Flate, LZW, ASCII85, ASCIIHex
// and RunLength, with the PNG and TIFF predictors.
//
// Every decoder takes the whole encoded stream and a cap on the decoded
// size. Nothing here trusts its input: a decoder returns an error or a
// truncated result on bad data, and ErrTooLarge when the output would exceed
// the cap, but it never panics and never allocates beyond the cap.
package filter

import (
	"bytes"
	"compress/flate"
	"compress/zlib"
	"errors"
	"io"
)

// ErrTooLarge is returned when decoded data would exceed the caller's cap.
var ErrTooLarge = errors.New("filter: decoded data exceeds the size limit")

// ErrUnknown is returned for a filter name this package does not decode.
var ErrUnknown = errors.New("filter: unknown filter")

// Params are the decode parameters a filter may take. Zero values mean "use
// the default".
type Params struct {
	Predictor        int
	Colors           int
	BitsPerComponent int
	Columns          int
	EarlyChange      *int // LZW only; nil means the default of 1
}

// Known reports whether name is a filter this package decodes, under its
// full name or its inline-image abbreviation.
func Known(name string) bool {
	switch name {
	case "FlateDecode", "Fl", "LZWDecode", "LZW", "ASCII85Decode", "A85",
		"ASCIIHexDecode", "AHx", "RunLengthDecode", "RL":
		return true
	}
	return false
}

// Decode applies one filter to data. The result is at most max bytes long.
//
// partial is true when the input was damaged and the result is whatever
// could be decoded before the damage; err is nil in that case, because a
// truncated stream is usually still worth reading.
func Decode(name string, data []byte, p Params, max int) (out []byte, partial bool, err error) {
	switch name {
	case "FlateDecode", "Fl":
		out, partial, err = inflate(data, max)
		if err == nil {
			out, err = predict(out, p)
		}
	case "LZWDecode", "LZW":
		early := 1
		if p.EarlyChange != nil {
			early = *p.EarlyChange
		}
		out, partial, err = lzwDecode(data, early, max)
		if err == nil {
			out, err = predict(out, p)
		}
	case "ASCII85Decode", "A85":
		out, err = ascii85Decode(data, max)
	case "ASCIIHexDecode", "AHx":
		out, err = asciiHexDecode(data, max)
	case "RunLengthDecode", "RL":
		out, partial, err = runLengthDecode(data, max)
	default:
		return nil, false, ErrUnknown
	}
	return out, partial, err
}

// inflate decompresses zlib data. Real files contain streams with a bad
// checksum, a missing header or a truncated tail, so it keeps whatever was
// decompressed before an error and falls back to raw deflate.
func inflate(data []byte, max int) ([]byte, bool, error) {
	zr, err := zlib.NewReader(bytes.NewReader(data))
	if err == nil {
		out, rerr := readCapped(zr, max)
		if rerr == nil {
			return out, false, nil
		}
		if errors.Is(rerr, ErrTooLarge) {
			return nil, false, rerr
		}
		if len(out) > 0 {
			return out, true, nil
		}
	}
	// No usable zlib stream. Try raw deflate, with and without the two
	// header bytes.
	for _, skip := range []int{0, 2} {
		if len(data) <= skip {
			continue
		}
		out, rerr := readCapped(flate.NewReader(bytes.NewReader(data[skip:])), max)
		if errors.Is(rerr, ErrTooLarge) {
			return nil, false, rerr
		}
		if rerr == nil || len(out) > 0 {
			return out, true, nil
		}
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, false, nil
	}
	return nil, false, errors.New("filter: invalid flate data")
}

// readCapped reads all of r, failing with ErrTooLarge beyond max bytes. On
// any other error it returns what was read so far along with the error.
func readCapped(r io.Reader, max int) ([]byte, error) {
	var buf bytes.Buffer
	_, err := io.Copy(&buf, io.LimitReader(r, int64(max)+1))
	if buf.Len() > max {
		return nil, ErrTooLarge
	}
	return buf.Bytes(), err
}

func asciiHexDecode(data []byte, max int) ([]byte, error) {
	out := make([]byte, 0, min(len(data)/2+1, max))
	hi, have := byte(0), false
	for _, c := range data {
		if c == '>' {
			break
		}
		var v byte
		switch {
		case c >= '0' && c <= '9':
			v = c - '0'
		case c >= 'a' && c <= 'f':
			v = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			v = c - 'A' + 10
		default:
			continue
		}
		if !have {
			hi, have = v, true
			continue
		}
		if len(out) >= max {
			return nil, ErrTooLarge
		}
		out = append(out, hi<<4|v)
		have = false
	}
	if have {
		if len(out) >= max {
			return nil, ErrTooLarge
		}
		out = append(out, hi<<4)
	}
	return out, nil
}

func ascii85Decode(data []byte, max int) ([]byte, error) {
	if bytes.HasPrefix(data, []byte("<~")) {
		data = data[2:]
	}
	out := make([]byte, 0, min(len(data)/5*4+4, max))
	var group [5]byte
	n := 0
	emit := func(count int) bool {
		var v uint32
		for _, d := range group {
			v = v*85 + uint32(d)
		}
		b := [4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
		if len(out)+count > max {
			return false
		}
		out = append(out, b[:count]...)
		return true
	}
	for _, c := range data {
		switch {
		case c == '~':
			goto done
		case c == 'z' && n == 0:
			if len(out)+4 > max {
				return nil, ErrTooLarge
			}
			out = append(out, 0, 0, 0, 0)
		case c >= '!' && c <= 'u':
			group[n] = c - '!'
			n++
			if n == 5 {
				if !emit(4) {
					return nil, ErrTooLarge
				}
				n = 0
			}
		}
		// Anything else is white space or junk, which is skipped.
	}
done:
	if n > 1 {
		for i := n; i < 5; i++ {
			group[i] = 84
		}
		if !emit(n - 1) {
			return nil, ErrTooLarge
		}
	}
	return out, nil
}

func runLengthDecode(data []byte, max int) ([]byte, bool, error) {
	var out []byte
	for i := 0; i < len(data); {
		n := int(data[i])
		i++
		switch {
		case n == 128:
			return out, false, nil
		case n < 128:
			count := n + 1
			if i+count > len(data) {
				count = len(data) - i
				if len(out)+count > max {
					return nil, false, ErrTooLarge
				}
				return append(out, data[i:i+count]...), true, nil
			}
			if len(out)+count > max {
				return nil, false, ErrTooLarge
			}
			out = append(out, data[i:i+count]...)
			i += count
		default:
			if i >= len(data) {
				return out, true, nil
			}
			count := 257 - n
			if len(out)+count > max {
				return nil, false, ErrTooLarge
			}
			for j := 0; j < count; j++ {
				out = append(out, data[i])
			}
			i++
		}
	}
	return out, false, nil
}
