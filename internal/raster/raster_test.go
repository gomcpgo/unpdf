// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package raster

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"
)

var ctx = context.Background()

// convert runs PNG and reads the result back with the standard library's
// decoder, which is the check that the file is a valid one.
func convert(t *testing.T, img *Image) image.Image {
	t.Helper()
	data, _, err := PNG(ctx, img)
	if err != nil {
		t.Fatalf("PNG: %v", err)
	}
	out, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("the PNG does not decode: %v", err)
	}
	if b := out.Bounds(); b.Dx() != img.Width || b.Dy() != img.Height {
		t.Fatalf("the PNG is %d x %d, want %d x %d", b.Dx(), b.Dy(), img.Width, img.Height)
	}
	return out
}

// pixels returns the image as 8-bit R, G, B, A, not premultiplied, row by
// row.
func pixels(img image.Image) []uint8 {
	b := img.Bounds()
	var out []uint8
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			out = append(out, c.R, c.G, c.B, c.A)
		}
	}
	return out
}

func check(t *testing.T, name string, img *Image, want ...uint8) {
	t.Helper()
	got := pixels(convert(t, img))
	if !bytes.Equal(got, want) {
		t.Errorf("%s:\n got %v\nwant %v", name, got, want)
	}
}

func TestGray(t *testing.T) {
	check(t, "8 bits", &Image{Width: 2, Height: 2, Bits: 8, Data: []byte{0, 85, 170, 255}},
		0, 0, 0, 255, 85, 85, 85, 255, 170, 170, 170, 255, 255, 255, 255, 255)
	// Rows are padded to a byte: 3 pixels of 1 bit take one byte a row.
	check(t, "1 bit", &Image{Width: 3, Height: 2, Bits: 1, Data: []byte{0b101_00000, 0b010_11111}},
		255, 255, 255, 255, 0, 0, 0, 255, 255, 255, 255, 255,
		0, 0, 0, 255, 255, 255, 255, 255, 0, 0, 0, 255)
	check(t, "2 bits", &Image{Width: 4, Height: 1, Bits: 2, Data: []byte{0b00_01_10_11}},
		0, 0, 0, 255, 85, 85, 85, 255, 170, 170, 170, 255, 255, 255, 255, 255)
	check(t, "4 bits", &Image{Width: 2, Height: 1, Bits: 4, Data: []byte{0x0F}},
		0, 0, 0, 255, 255, 255, 255, 255)
	check(t, "reversed", &Image{Width: 2, Height: 1, Bits: 8, Decode: []float64{1, 0}, Data: []byte{0, 200}},
		255, 255, 255, 255, 55, 55, 55, 255)
	// A decode array that is neither the default nor its reverse.
	check(t, "half range", &Image{Width: 2, Height: 1, Bits: 8, Decode: []float64{0, 0.5}, Data: []byte{0, 255}},
		0, 0, 0, 255, 128, 128, 128, 255)

	data, _, err := PNG(ctx, &Image{Width: 1, Height: 1, Bits: 16, Data: []byte{0x12, 0x34}})
	if err != nil {
		t.Fatal(err)
	}
	out, _ := png.Decode(bytes.NewReader(data))
	if g, ok := out.At(0, 0).(color.Gray16); !ok || g.Y != 0x1234 {
		t.Errorf("16 bits: got %#v, want grey 0x1234 kept at 16 bits", out.At(0, 0))
	}
	for bits, depth := range map[int]string{1: "\x01", 2: "\x02", 4: "\x04", 8: "\x08"} {
		data, _, _ := PNG(ctx, &Image{Width: 1, Height: 1, Bits: bits, Data: []byte{0}})
		// Byte 24 of a PNG is the bit depth, byte 25 the colour type.
		if string(data[24:26]) != depth+"\x00" {
			t.Errorf("grey of %d bits was written with depth %d, colour type %d", bits, data[24], data[25])
		}
	}
}

func TestRGB(t *testing.T) {
	rgb := Space{Model: RGB}
	check(t, "8 bits", &Image{Width: 2, Height: 1, Bits: 8, Space: rgb, Data: []byte{255, 0, 0, 10, 20, 30}},
		255, 0, 0, 255, 10, 20, 30, 255)
	check(t, "4 bits", &Image{Width: 2, Height: 1, Bits: 4, Space: rgb, Data: []byte{0xF0, 0x0F, 0x00}},
		255, 0, 0, 255, 255, 0, 0, 255)
	check(t, "reversed", &Image{Width: 1, Height: 1, Bits: 8, Space: rgb, Decode: []float64{1, 0, 1, 0, 1, 0}, Data: []byte{255, 0, 55}},
		0, 255, 200, 255)

	data, _, err := PNG(ctx, &Image{Width: 1, Height: 1, Bits: 16, Space: rgb, Data: []byte{0xAB, 0xCD, 0, 1, 0xFF, 0xFE}})
	if err != nil {
		t.Fatal(err)
	}
	out, _ := png.Decode(bytes.NewReader(data))
	if c, ok := out.At(0, 0).(color.RGBA64); !ok || c.R != 0xABCD || c.G != 1 || c.B != 0xFFFE {
		t.Errorf("16 bits: got %#v", out.At(0, 0))
	}
}

func TestCMYK(t *testing.T) {
	cmyk := Space{Model: CMYK}
	check(t, "inks", &Image{Width: 4, Height: 1, Bits: 8, Space: cmyk, Data: []byte{
		0, 0, 0, 0, // no ink: white
		0, 0, 0, 255, // black
		255, 0, 0, 0, // cyan
		0, 255, 255, 0, // magenta and yellow: red
	}}, 255, 255, 255, 255, 0, 0, 0, 255, 0, 255, 255, 255, 255, 0, 0, 255)
	// Two of the four inks, as a DeviceN space of process colours has them,
	// and a component that marks nothing.
	some := Space{Model: CMYK, Slots: []int{1, -1, 3}}
	check(t, "some inks", &Image{Width: 2, Height: 1, Bits: 8, Space: some, Data: []byte{255, 255, 0, 0, 255, 255}},
		255, 0, 255, 255, 0, 0, 0, 255)
}

func TestLabAndTint(t *testing.T) {
	lab := Space{Model: Lab, Range: [4]float64{-100, 100, -100, 100}}
	// L* runs from 0 to 100 over the samples; a* and b* are 0 at the middle.
	got := pixels(convert(t, &Image{Width: 3, Height: 1, Bits: 8, Space: lab, Data: []byte{
		255, 128, 128, 0, 128, 128, 136, 208, 196,
	}}))
	if got[0] < 252 || got[1] < 252 || got[2] < 252 {
		t.Errorf("Lab white = %v", got[:3])
	}
	// Sample 128 of 255 is a hair above the middle, so not quite neutral.
	if got[4] > 2 || got[5] > 2 || got[6] > 2 {
		t.Errorf("Lab black = %v", got[4:7])
	}
	// L* 53, a* 63, b* 53 is close to pure red.
	if r, g, b := got[8], got[9], got[10]; r < 230 || g > 80 || b > 80 {
		t.Errorf("Lab red = %d %d %d", r, g, b)
	}

	tint := Space{Model: Tint}
	check(t, "tint", &Image{Width: 2, Height: 1, Bits: 8, Space: tint, Data: []byte{0, 255}},
		255, 255, 255, 255, 0, 0, 0, 255)
	check(t, "tint reversed", &Image{Width: 2, Height: 1, Bits: 8, Space: tint, Decode: []float64{1, 0}, Data: []byte{0, 255}},
		0, 0, 0, 255, 255, 255, 255, 255)
}

func TestIndexed(t *testing.T) {
	pal := Space{Model: Indexed, Base: &Space{Model: RGB}, HiVal: 2, Lookup: []byte{255, 0, 0, 0, 255, 0, 0, 0, 255}}
	img := &Image{Width: 4, Height: 1, Bits: 2, Space: pal, Data: []byte{0b00_01_10_11}}
	// The fourth index is beyond the table: the last colour stands in.
	check(t, "palette", img, 255, 0, 0, 255, 0, 255, 0, 255, 0, 0, 255, 255, 0, 0, 255, 255)
	data, _, _ := PNG(ctx, img)
	if data[24] != 2 || data[25] != ctPalette {
		t.Errorf("an indexed image was written with depth %d, colour type %d", data[24], data[25])
	}

	cmyk := Space{Model: Indexed, Base: &Space{Model: CMYK}, HiVal: 1, Lookup: []byte{0, 0, 0, 255, 0, 0, 0, 0}}
	check(t, "CMYK base", &Image{Width: 2, Height: 1, Bits: 8, Space: cmyk, Data: []byte{0, 1}},
		0, 0, 0, 255, 255, 255, 255, 255)
	// A short table: the colours it lacks are black.
	short := Space{Model: Indexed, Base: &Space{Model: RGB}, HiVal: 1, Lookup: []byte{9, 9, 9}}
	check(t, "short table", &Image{Width: 2, Height: 1, Bits: 8, Space: short, Data: []byte{0, 1}},
		9, 9, 9, 255, 0, 0, 0, 255)
	// A decode array that turns the indices round.
	check(t, "reversed", &Image{Width: 3, Height: 1, Bits: 8, Space: pal, Decode: []float64{2, 0}, Data: []byte{0, 255, 128}},
		0, 0, 255, 255, 255, 0, 0, 255, 0, 255, 0, 255)
	// With a mask the palette gives way to RGB and alpha.
	check(t, "masked", &Image{Width: 2, Height: 1, Bits: 8, Space: pal, Data: []byte{1, 2},
		Alpha: &Alpha{Width: 2, Height: 1, Bits: 8, Data: []byte{255, 0}, Decode: [2]float64{0, 1}}},
		0, 255, 0, 255, 0, 0, 255, 0)
}

func TestStencil(t *testing.T) {
	// A stencil paints where its bits are 0, and painted is black.
	check(t, "default", &Image{Width: 2, Height: 1, Stencil: true, Data: []byte{0b01_000000}},
		0, 0, 0, 255, 255, 255, 255, 255)
	check(t, "reversed", &Image{Width: 2, Height: 1, Stencil: true, Decode: []float64{1, 0}, Data: []byte{0b01_000000}},
		255, 255, 255, 255, 0, 0, 0, 255)
}

func TestAlpha(t *testing.T) {
	soft := func(w, h int, data ...byte) *Alpha {
		return &Alpha{Width: w, Height: h, Bits: 8, Data: data, Decode: [2]float64{0, 1}}
	}
	rgb := Space{Model: RGB}
	check(t, "soft mask", &Image{Width: 2, Height: 1, Bits: 8, Space: rgb, Data: []byte{1, 2, 3, 4, 5, 6}, Alpha: soft(2, 1, 255, 64)},
		1, 2, 3, 255, 4, 5, 6, 64)
	check(t, "grey", &Image{Width: 2, Height: 1, Bits: 8, Data: []byte{7, 8}, Alpha: soft(2, 1, 0, 200)},
		7, 7, 7, 0, 8, 8, 8, 200)
	// A mask of another size is stretched over the image.
	check(t, "small mask", &Image{Width: 4, Height: 2, Bits: 8, Data: make([]byte, 8), Alpha: soft(2, 1, 255, 0)},
		0, 0, 0, 255, 0, 0, 0, 255, 0, 0, 0, 0, 0, 0, 0, 0,
		0, 0, 0, 255, 0, 0, 0, 255, 0, 0, 0, 0, 0, 0, 0, 0)
	check(t, "large mask", &Image{Width: 1, Height: 1, Bits: 8, Data: []byte{9}, Alpha: soft(2, 2, 77, 1, 2, 3)},
		9, 9, 9, 77)
	// A stencil as a mask: its 0 bits are where the image shows.
	check(t, "stencil mask", &Image{Width: 2, Height: 1, Bits: 8, Data: []byte{7, 8},
		Alpha: &Alpha{Width: 2, Height: 1, Bits: 1, Data: []byte{0b01_000000}, Decode: [2]float64{1, 0}}},
		7, 7, 7, 255, 8, 8, 8, 0)
	// A mask with no data for its second row leaves that row opaque.
	check(t, "short mask", &Image{Width: 1, Height: 2, Bits: 8, Data: []byte{7, 8}, Alpha: soft(1, 2, 0)},
		7, 7, 7, 0, 8, 8, 8, 255)

	// Blended in advance with white at half opacity: 255 was 255, and 128
	// was 0.5*c + 0.5*255, so c was 1.
	matte := soft(2, 1, 128, 128)
	matte.Matte = []float64{1}
	got := pixels(convert(t, &Image{Width: 2, Height: 1, Bits: 8, Data: []byte{255, 128}, Alpha: matte}))
	if got[0] != 255 || got[4] > 2 || got[3] != 128 {
		t.Errorf("matte: got %v", got)
	}

	check(t, "colour key", &Image{Width: 3, Height: 1, Bits: 8, Space: rgb, ColorKey: []int{250, 255, 250, 255, 250, 255},
		Data: []byte{255, 255, 255, 251, 250, 252, 255, 255, 249}},
		255, 255, 255, 0, 251, 250, 252, 0, 255, 255, 249, 255)
}

func TestShortData(t *testing.T) {
	data, short, err := PNG(ctx, &Image{Width: 2, Height: 3, Bits: 8, Space: Space{Model: RGB}, Data: []byte{1, 2, 3, 4, 5, 6, 7, 8}})
	if err != nil || !short {
		t.Fatalf("short = %v, err = %v; want short data reported", short, err)
	}
	out, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	want := []uint8{1, 2, 3, 255, 4, 5, 6, 255, 7, 8, 0, 255, 0, 0, 0, 255, 0, 0, 0, 255, 0, 0, 0, 255}
	if got := pixels(out); !bytes.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if _, short, _ := PNG(ctx, &Image{Width: 1, Height: 1, Bits: 8, Data: []byte{1, 2, 3}}); short {
		t.Error("data beyond the last row was reported as short")
	}
}

func TestInvalid(t *testing.T) {
	rgb := Space{Model: RGB}
	for name, tt := range map[string]struct {
		img  *Image
		want error
	}{
		"no width":      {&Image{Height: 1, Bits: 8}, ErrBadImage},
		"negative":      {&Image{Width: 1, Height: -1, Bits: 8}, ErrBadImage},
		"too wide":      {&Image{Width: MaxSide + 1, Height: 1, Bits: 8}, ErrTooLarge},
		"too tall":      {&Image{Width: 1, Height: MaxSide + 1, Bits: 1}, ErrTooLarge},
		"3 bits":        {&Image{Width: 1, Height: 1, Bits: 3}, ErrBadImage},
		"no bits":       {&Image{Width: 1, Height: 1, Space: rgb}, ErrBadImage},
		"model":         {&Image{Width: 1, Height: 1, Bits: 8, Space: Space{Model: 99}}, ErrBadImage},
		"no base":       {&Image{Width: 1, Height: 1, Bits: 8, Space: Space{Model: Indexed}}, ErrBadImage},
		"indexed twice": {&Image{Width: 1, Height: 1, Bits: 8, Space: Space{Model: Indexed, Base: &Space{Model: Indexed, Base: &rgb}}}, ErrBadImage},
		"long table":    {&Image{Width: 1, Height: 1, Bits: 8, Space: Space{Model: Indexed, Base: &rgb, HiVal: 256}}, ErrBadImage},
		"bad slot":      {&Image{Width: 1, Height: 1, Bits: 8, Space: Space{Model: CMYK, Slots: []int{4}}}, ErrBadImage},
		"no slots":      {&Image{Width: 1, Height: 1, Bits: 8, Space: Space{Model: CMYK, Slots: []int{}}}, ErrBadImage},
		"mask bits":     {&Image{Width: 1, Height: 1, Bits: 8, Alpha: &Alpha{Width: 1, Height: 1, Bits: 5}}, ErrBadImage},
		"mask too wide": {&Image{Width: 1, Height: 1, Bits: 8, Alpha: &Alpha{Width: MaxSide + 1, Height: 1, Bits: 8}}, ErrTooLarge},
	} {
		if _, _, err := PNG(ctx, tt.img); !errors.Is(err, tt.want) {
			t.Errorf("%s: got %v, want %v", name, err, tt.want)
		}
	}
}

func TestCancelled(t *testing.T) {
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := PNG(cancelled, &Image{Width: 100, Height: 100, Bits: 8}); !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
}

// TestFilters checks that rows survive each of the ways PNG predicts them:
// a gradient, noise and flat colour between them make every filter win.
func TestFilters(t *testing.T) {
	const w, h = 64, 48
	data := make([]byte, w*h*3)
	seed := uint32(1)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			p := data[(y*w+x)*3:]
			switch y / 12 {
			case 0:
				p[0], p[1], p[2] = byte(4*x), byte(3*x), byte(2*x) // predicted from the left
			case 1:
				p[0], p[1], p[2] = byte(5*y), byte(x*x), byte(x^y) // from above
			case 2:
				seed = seed*1664525 + 1013904223
				p[0], p[1], p[2] = byte(seed>>24), byte(seed>>16), byte(seed>>8)
			default:
				p[0], p[1], p[2] = byte(2*x+3*y), byte(x+y), byte(x*y/7) // a slope
			}
		}
	}
	out := convert(t, &Image{Width: w, Height: h, Bits: 8, Space: Space{Model: RGB}, Data: data})
	got := pixels(out)
	for i := 0; i < w*h; i++ {
		if got[4*i] != data[3*i] || got[4*i+1] != data[3*i+1] || got[4*i+2] != data[3*i+2] {
			t.Fatalf("pixel %d: got %v, want %v", i, got[4*i:4*i+3], data[3*i:3*i+3])
		}
	}
}

func FuzzPNG(f *testing.F) {
	f.Add([]byte{0, 85, 170, 255}, uint16(2), uint16(2), uint8(8), uint8(0), uint8(0), []byte(nil))
	f.Add([]byte{255, 0, 0, 0, 255, 0}, uint16(2), uint16(1), uint8(8), uint8(1), uint8(1), []byte{255, 9})
	f.Add([]byte{0, 0, 0, 255, 1, 2, 3, 4}, uint16(1), uint16(2), uint8(16), uint8(2), uint8(2), []byte{3})
	f.Add([]byte{255, 128, 128}, uint16(1), uint16(1), uint8(8), uint8(3), uint8(4), []byte(nil))
	f.Add([]byte{0b00_01_10_11}, uint16(4), uint16(1), uint8(2), uint8(4), uint8(8), []byte{255, 0, 0, 0, 255, 0, 0, 0, 255})
	f.Add([]byte{0xA5}, uint16(8), uint16(1), uint8(1), uint8(5), uint8(16), []byte(nil))
	f.Add([]byte{0x5A, 0xA5}, uint16(5), uint16(2), uint8(1), uint8(6), uint8(32), []byte{0xF0})
	f.Fuzz(func(t *testing.T, data []byte, width, height uint16, bits, model, flags uint8, extra []byte) {
		w, h := int(width)%300, int(height)%300
		img := &Image{Width: w, Height: h, Bits: int(bits), Data: data}
		switch model % 8 {
		case 0, 1, 2, 3, 5:
			img.Space.Model = Model(model % 8)
		case 4:
			img.Space = Space{Model: Indexed, Base: &Space{Model: Model(flags % 4)}, HiVal: int(flags), Lookup: extra}
		case 6:
			img.Stencil = true
		case 7:
			slots := make([]int, len(extra)%6)
			for i := range slots {
				slots[i] = int(extra[i]%5) - 1
			}
			img.Space = Space{Model: CMYK, Slots: slots}
		}
		if img.Space.Model == Lab {
			img.Space.Range = [4]float64{-float64(flags), 100, -100, float64(flags)}
		}
		if flags&1 != 0 {
			for _, b := range extra {
				img.Decode = append(img.Decode, float64(int(b)-64)/64)
			}
		}
		if flags&2 != 0 {
			img.Alpha = &Alpha{Width: 1 + int(flags>>4), Height: 1 + len(extra)%7, Bits: 1 << (flags >> 2 % 5), Data: extra, Decode: [2]float64{float64(flags & 1), float64(flags >> 7)}}
			if flags&4 != 0 {
				img.Alpha.Width, img.Alpha.Height = w, h
				img.Alpha.Matte = []float64{0.5, 1, 0, 0.25}[:max(1, img.Space.Components())%5]
			}
		}
		if flags&8 != 0 {
			for _, b := range extra {
				img.ColorKey = append(img.ColorKey, int(b)-3)
			}
		}
		out, _, err := PNG(ctx, img)
		if err != nil {
			return
		}
		got, err := png.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatalf("the PNG does not decode: %v", err)
		}
		if b := got.Bounds(); b.Dx() != w || b.Dy() != h {
			t.Fatalf("the PNG is %d x %d, want %d x %d", b.Dx(), b.Dy(), w, h)
		}
	})
}
