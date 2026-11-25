// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package filter

import (
	"bytes"
	"compress/flate"
	"compress/lzw"
	"compress/zlib"
	"encoding/ascii85"
	"errors"
	"strings"
	"testing"
)

const limit = 1 << 20

func deflate(data []byte) []byte {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	w.Write(data)
	w.Close()
	return buf.Bytes()
}

func TestFlate(t *testing.T) {
	plain := []byte(strings.Repeat("The quick brown fox. ", 500))
	packed := deflate(plain)

	out, partial, err := Decode("FlateDecode", packed, Params{}, limit)
	if err != nil || partial || !bytes.Equal(out, plain) {
		t.Fatalf("round trip failed: partial=%v err=%v", partial, err)
	}
	if out, _, err := Decode("Fl", packed, Params{}, limit); err != nil || !bytes.Equal(out, plain) {
		t.Errorf("abbreviated name failed: %v", err)
	}

	// A truncated stream yields what could be decompressed, flagged partial.
	out, partial, err = Decode("FlateDecode", packed[:len(packed)/2], Params{}, limit)
	if err != nil || !partial || len(out) == 0 || !bytes.HasPrefix(plain, out) {
		t.Errorf("truncated: got %d bytes, partial=%v err=%v", len(out), partial, err)
	}

	// Raw deflate without the zlib wrapper is accepted.
	var raw bytes.Buffer
	fw, _ := flate.NewWriter(&raw, flate.DefaultCompression)
	fw.Write(plain)
	fw.Close()
	if out, _, err := Decode("FlateDecode", raw.Bytes(), Params{}, limit); err != nil || !bytes.Equal(out, plain) {
		t.Errorf("raw deflate: err=%v, %d bytes", err, len(out))
	}

	// A bad checksum still gives the data.
	bad := append([]byte(nil), packed...)
	bad[len(bad)-1] ^= 0xFF
	if out, partial, err := Decode("FlateDecode", bad, Params{}, limit); err != nil || !partial || !bytes.Equal(out, plain) {
		t.Errorf("bad checksum: partial=%v err=%v", partial, err)
	}

	if _, _, err := Decode("FlateDecode", []byte("this is not deflate data at all"), Params{}, limit); err == nil {
		t.Error("garbage decoded without an error")
	}
	if out, _, err := Decode("FlateDecode", nil, Params{}, limit); err != nil || len(out) != 0 {
		t.Errorf("empty input: %v", err)
	}

	// A decompression bomb stops at the cap.
	bomb := deflate(make([]byte, 8<<20))
	if _, _, err := Decode("FlateDecode", bomb, Params{}, 1<<20); !errors.Is(err, ErrTooLarge) {
		t.Errorf("bomb: got %v, want ErrTooLarge", err)
	}
}

func TestLZW(t *testing.T) {
	// The example from the PDF specification, with the default early change.
	encoded := []byte{0x80, 0x0B, 0x60, 0x50, 0x22, 0x0C, 0x0C, 0x85, 0x01}
	want := []byte{45, 45, 45, 45, 45, 65, 45, 45, 45, 66}
	out, partial, err := Decode("LZWDecode", encoded, Params{}, limit)
	if err != nil || partial || !bytes.Equal(out, want) {
		t.Errorf("specification example: got %v partial=%v err=%v", out, partial, err)
	}

	// The standard library writes the variant without early change. Use
	// enough data to fill the table and force a reset.
	var plain bytes.Buffer
	for i := 0; i < 20000; i++ {
		plain.WriteByte(byte(i * i % 251))
		plain.WriteString("abcabc")
	}
	var packed bytes.Buffer
	w := lzw.NewWriter(&packed, lzw.MSB, 8)
	w.Write(plain.Bytes())
	w.Close()
	zero := 0
	out, partial, err = Decode("LZW", packed.Bytes(), Params{EarlyChange: &zero}, limit)
	if err != nil || partial || !bytes.Equal(out, plain.Bytes()) {
		t.Errorf("no early change: %d bytes, want %d, partial=%v err=%v", len(out), plain.Len(), partial, err)
	}

	if _, _, err := Decode("LZWDecode", packed.Bytes(), Params{EarlyChange: &zero}, 1000); !errors.Is(err, ErrTooLarge) {
		t.Errorf("cap: got %v, want ErrTooLarge", err)
	}
	// Corrupt codes stop the decoder; they do not crash it.
	if _, partial, err := Decode("LZWDecode", []byte{0xFF, 0xFF, 0xFF, 0xFF}, Params{}, limit); err != nil || !partial {
		t.Errorf("corrupt: partial=%v err=%v", partial, err)
	}
}

func TestASCII85(t *testing.T) {
	plain := []byte("Man is distinguished, not only by his reason\x00\x00\x00\x00 and more.")
	buf := make([]byte, ascii85.MaxEncodedLen(len(plain)))
	n := ascii85.Encode(buf, plain)
	encoded := string(buf[:n])
	if !strings.Contains(encoded, "z") {
		t.Fatal("test data should contain a 'z' group")
	}
	for _, in := range []string{encoded + "~>", "<~" + encoded + "~>", encoded, strings.Join(strings.Split(encoded, ""), "\n") + "~>"} {
		out, _, err := Decode("ASCII85Decode", []byte(in), Params{}, limit)
		if err != nil || !bytes.Equal(out, plain) {
			t.Errorf("decode %q: got %q err=%v", in, out, err)
		}
	}
	if _, _, err := Decode("A85", []byte(strings.Repeat("z", 100)), Params{}, 50); !errors.Is(err, ErrTooLarge) {
		t.Errorf("cap: got %v, want ErrTooLarge", err)
	}
}

func TestASCIIHex(t *testing.T) {
	for in, want := range map[string]string{
		"48656C6C6F>":     "Hello",
		"48 65 6c 6c 6f>": "Hello",
		"48656C6C6F":      "Hello",
		"484>":            "H@",
		">":               "",
		"4x8>garbage":     "H",
	} {
		out, _, err := Decode("ASCIIHexDecode", []byte(in), Params{}, limit)
		if err != nil || string(out) != want {
			t.Errorf("decode %q: got %q err=%v, want %q", in, out, err, want)
		}
	}
	if _, _, err := Decode("AHx", []byte("0000000000"), Params{}, 2); !errors.Is(err, ErrTooLarge) {
		t.Errorf("cap: got %v, want ErrTooLarge", err)
	}
}

func TestRunLength(t *testing.T) {
	// Copy 3 literal bytes, repeat 'x' 4 times, end of data, ignored tail.
	in := []byte{2, 'a', 'b', 'c', 253, 'x', 128, 'z'}
	out, partial, err := Decode("RunLengthDecode", in, Params{}, limit)
	if err != nil || partial || string(out) != "abcxxxx" {
		t.Errorf("got %q partial=%v err=%v", out, partial, err)
	}
	// A literal run that claims more bytes than there are.
	out, partial, err = Decode("RL", []byte{5, 'a', 'b'}, Params{}, limit)
	if err != nil || !partial || string(out) != "ab" {
		t.Errorf("short run: got %q partial=%v err=%v", out, partial, err)
	}
	if _, _, err := Decode("RL", []byte{129, 'x', 129, 'x'}, Params{}, 200); !errors.Is(err, ErrTooLarge) {
		t.Errorf("cap: got %v, want ErrTooLarge", err)
	}
}

// pngEncode applies PNG filter ft to every row, as an encoder would.
func pngEncode(data []byte, rowLen, bpp int, ft byte) []byte {
	var out []byte
	prev := make([]byte, rowLen)
	for start := 0; start < len(data); start += rowLen {
		row := data[start : start+rowLen]
		out = append(out, ft)
		for i := range row {
			var a, b, c int
			if i >= bpp {
				a, c = int(row[i-bpp]), int(prev[i-bpp])
			}
			b = int(prev[i])
			var pred int
			switch ft {
			case 1:
				pred = a
			case 2:
				pred = b
			case 3:
				pred = (a + b) / 2
			case 4:
				pa, pb, pc := abs(b-c), abs(a-c), abs(a+b-2*c)
				pred = c
				if pa <= pb && pa <= pc {
					pred = a
				} else if pb <= pc {
					pred = b
				}
			}
			out = append(out, row[i]-byte(pred))
		}
		prev = row
	}
	return out
}

func TestPredictors(t *testing.T) {
	const columns, colors = 5, 3
	rowLen := columns * colors
	plain := make([]byte, rowLen*7)
	for i := range plain {
		plain[i] = byte(i*37 + i*i%11)
	}
	for ft := byte(0); ft <= 4; ft++ {
		encoded := deflate(pngEncode(plain, rowLen, colors, ft))
		out, _, err := Decode("FlateDecode", encoded, Params{Predictor: 10 + int(ft), Colors: colors, Columns: columns}, limit)
		if err != nil || !bytes.Equal(out, plain) {
			t.Errorf("PNG filter %d: err=%v, output differs=%v", ft, err, !bytes.Equal(out, plain))
		}
	}

	// TIFF predictor, 8 bits: each sample is the difference from the one
	// before it in the same colour.
	tiff := append([]byte(nil), plain...)
	for start := 0; start < len(tiff); start += rowLen {
		row := tiff[start : start+rowLen]
		for i := len(row) - 1; i >= colors; i-- {
			row[i] -= row[i-colors]
		}
	}
	out, _, err := Decode("FlateDecode", deflate(tiff), Params{Predictor: 2, Colors: colors, Columns: columns}, limit)
	if err != nil || !bytes.Equal(out, plain) {
		t.Errorf("TIFF predictor: err=%v", err)
	}

	// TIFF predictor, 1 bit: 0b10110000 as differences decodes to running
	// sums modulo 2.
	out, _, err = Decode("FlateDecode", deflate([]byte{0b10110000}), Params{Predictor: 2, BitsPerComponent: 1, Columns: 8}, limit)
	if err != nil || len(out) != 1 || out[0] != 0b11011111 {
		t.Errorf("TIFF 1-bit predictor: got %08b err=%v", out, err)
	}

	// Defaults (1 colour, 8 bits, 1 column) and a short final row.
	out, _, err = Decode("FlateDecode", deflate([]byte{2, 7, 2, 1, 2}), Params{Predictor: 12}, limit)
	if err != nil || !bytes.Equal(out, []byte{7, 8}) {
		t.Errorf("defaults: got %v err=%v", out, err)
	}

	for _, p := range []Params{
		{Predictor: 12, Columns: 1 << 30},
		{Predictor: 12, Colors: 1000},
		{Predictor: 12, BitsPerComponent: 3},
		{Predictor: 7},
	} {
		if _, _, err := Decode("FlateDecode", deflate([]byte{0, 1, 2}), p, limit); err == nil {
			t.Errorf("params %+v accepted, want an error", p)
		}
	}
}

func TestUnknownFilter(t *testing.T) {
	if _, _, err := Decode("DCTDecode", nil, Params{}, limit); !errors.Is(err, ErrUnknown) {
		t.Errorf("got %v, want ErrUnknown", err)
	}
	if Known("DCTDecode") || !Known("FlateDecode") || !Known("A85") {
		t.Error("Known gives wrong answers")
	}
}

func FuzzDecode(f *testing.F) {
	names := []string{"FlateDecode", "LZWDecode", "ASCII85Decode", "ASCIIHexDecode", "RunLengthDecode"}
	f.Add(uint8(0), deflate([]byte("hello hello hello")), uint8(12), uint8(1), uint8(8), uint16(4), true)
	f.Add(uint8(1), []byte{0x80, 0x0B, 0x60, 0x50, 0x22, 0x0C, 0x0C, 0x85, 0x01}, uint8(2), uint8(3), uint8(16), uint16(2), false)
	f.Add(uint8(2), []byte("9jqo^BlbD-BleB1DJ+*+F(f,q~>"), uint8(0), uint8(0), uint8(0), uint16(0), true)
	f.Add(uint8(3), []byte("48 65 6C>"), uint8(0), uint8(0), uint8(0), uint16(0), true)
	f.Add(uint8(4), []byte{2, 'a', 'b', 'c', 253, 'x', 128}, uint8(0), uint8(0), uint8(0), uint16(0), true)
	f.Fuzz(func(t *testing.T, which uint8, data []byte, predictor, colors, bpc uint8, columns uint16, early bool) {
		const max = 1 << 16
		p := Params{Predictor: int(predictor), Colors: int(colors), BitsPerComponent: int(bpc), Columns: int(columns)}
		if !early {
			zero := 0
			p.EarlyChange = &zero
		}
		out, _, err := Decode(names[int(which)%len(names)], data, p, max)
		if err == nil && len(out) > max {
			t.Fatalf("output of %d bytes exceeds the cap of %d", len(out), max)
		}
	})
}
