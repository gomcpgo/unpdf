// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

// Package raster turns the samples of a PDF image into a PNG file.
//
// It knows pixels and colour, not PDF: the caller reads the image
// dictionary, decodes the stream and describes the result as an Image. The
// conversion keeps what it can as it is: grey and RGB samples are written
// at their own bit depth, and an indexed image stays a palette image.
// Everything else becomes 8-bit grey or RGB, with an alpha channel when the
// image has a mask.
//
// Colour is converted by the plain formulas, without colour management: an
// ICC profile is taken for the device space with the same number of
// components.
//
// Nothing here trusts its input. Sizes are checked before anything is
// allocated, memory use is a few rows whatever the image, and sample data
// that stops early is read as zeros.
package raster

import (
	"context"
	"errors"
	"math"
)

// MaxSide is the greatest width or height, in pixels, of an image that PNG
// converts.
const MaxSide = 1 << 16

// ErrTooLarge is returned for an image wider or taller than MaxSide.
var ErrTooLarge = errors.New("raster: image is too large")

// ErrBadImage is returned for an image whose description makes no sense: a
// size of zero, a bit depth that does not exist, a colour space with the
// wrong number of components.
var ErrBadImage = errors.New("raster: invalid image")

// Model is the kind of a colour space.
type Model int

const (
	// Gray has one component, from black at 0 to white at 1.
	Gray Model = iota
	// RGB has three additive components.
	RGB
	// CMYK has four ink components. With Space.Slots set it has one
	// component for each of some of the four inks.
	CMYK
	// Lab is CIE L*a*b*.
	Lab
	// Indexed has one component, an index into a table of colours.
	Indexed
	// Tint has one component, the amount of a single ink: 0 is none and 1
	// is solid. It is shown as grey.
	Tint
)

// Space is a colour space.
type Space struct {
	Model Model
	// Slots says, for a CMYK space of fewer or more than four components,
	// which ink each component is: 0 to 3 for cyan, magenta, yellow and
	// black, or -1 for a component that leaves no mark.
	Slots []int
	// Range belongs to a Lab space: the least and the greatest a*, then the
	// least and the greatest b*.
	Range [4]float64
	// Base, HiVal and Lookup belong to an Indexed space: the space of the
	// colours in the table, the greatest index, and the table, one byte per
	// component of the base space.
	Base   *Space
	HiVal  int
	Lookup []byte
}

// Components returns the number of components of a colour in the space, or
// 0 when the space is not a valid one.
func (s *Space) Components() int {
	switch s.Model {
	case Gray, Tint, Indexed:
		return 1
	case RGB, Lab:
		return 3
	case CMYK:
		if s.Slots == nil {
			return 4
		}
		if n := len(s.Slots); n >= 1 && n <= maxComponents {
			return n
		}
	}
	return 0
}

// maxComponents caps the components of a colour. PDF allows a DeviceN space
// up to 32.
const maxComponents = 32

// Image describes an image by its samples.
type Image struct {
	Width, Height int
	// Bits is the number of bits per component: 1, 2, 4, 8 or 16.
	Bits  int
	Space Space
	// Decode maps the least and the greatest sample value of each component
	// to component values, two numbers per component. Nil means the
	// default for the colour space.
	Decode []float64
	// Stencil marks an image mask: one bit per pixel that says where to
	// paint. It comes out black where it paints, on white. Bits and Space
	// are ignored.
	Stencil bool
	// Data holds the samples row by row, each row padded to a whole byte,
	// components in order, most significant bit first. What is missing at
	// the end counts as zeros, and what is beyond the last row is ignored.
	Data []byte
	// Alpha is the image's opacity, if it has a mask.
	Alpha *Alpha
	// ColorKey names a range of colours that are transparent: the least and
	// the greatest sample value for each component. A pixel is transparent
	// when every component is in its range.
	ColorKey []int
}

// Alpha is the opacity of an image, as an image of its own with one
// component. It need not have the size of the image it belongs to.
type Alpha struct {
	Width, Height int
	Bits          int
	Data          []byte
	// Decode maps the least and the greatest sample value to opacity, from
	// 0 for transparent to 1 for opaque. A soft mask has {0, 1}, a stencil
	// used as a mask {1, 0}.
	Decode [2]float64
	// Matte is the colour that the image's samples were blended with in
	// advance, in the image's own colour space, or nil. It is undone only
	// for a mask of the same size as the image.
	Matte []float64
}

// PNG converts the image. The second result reports that the sample data
// stopped before the last row; the rows without data are black.
func PNG(ctx context.Context, img *Image) (png []byte, short bool, err error) {
	c, err := newConverter(img)
	if err != nil {
		return nil, false, err
	}
	enc := newEncoder(c.width, c.height, c.depth, c.colorType, c.palette)
	for y := 0; y < c.height; y++ {
		if y%32 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, false, err
			}
		}
		enc.row(c.row(y))
	}
	return enc.finish(), c.short, nil
}

// PNG colour types.
const (
	ctGray      = 0
	ctRGB       = 2
	ctPalette   = 3
	ctGrayAlpha = 4
	ctRGBA      = 6
)

// converter produces the rows of the PNG from the rows of the image.
type converter struct {
	width, height int
	bits          int // per component, in the data
	n             int // components per pixel, in the data
	rowBytes      int
	data          []byte
	short         bool

	depth, colorType int
	palette          []byte // PLTE contents, for a palette image

	// A row is copied when direct is set, with its bits flipped when
	// invert is set too. Otherwise each pixel is converted.
	direct, invert bool

	space   *Space
	scale   [][2]float64 // per component: value = [0] + sample*[1]
	lut     [][]float32  // the same, tabulated, for up to 8 bits
	colors  [][3]uint8   // the colours of an indexed space
	grey    bool         // one colour channel in the output, not three
	key     []int
	alpha   *alphaPlane
	matte   []float64
	samples []uint16
	comps   []float64
	in, out []byte
}

// alphaPlane reads the opacity of a pixel from a mask of any size.
type alphaPlane struct {
	width, height int
	bits          int
	rowBytes      int
	data          []byte
	lut           []uint8 // sample to opacity, for up to 8 bits
	d0, d1        float64
	row           []uint16
	have          int // the row of the mask that row holds, or -1
	in            []byte
}

func validBits(bits int) bool {
	return bits == 1 || bits == 2 || bits == 4 || bits == 8 || bits == 16
}

// rowSize returns the bytes in a row of width pixels with n components of
// the given bits, or false when that is beyond reason.
func rowSize(width, n, bits int) (int, bool) {
	if width < 1 || width > MaxSide || n < 1 || n > maxComponents {
		return 0, false
	}
	return (width*n*bits + 7) / 8, true
}

func newConverter(img *Image) (*converter, error) {
	if img.Width < 1 || img.Height < 1 {
		return nil, ErrBadImage
	}
	if img.Width > MaxSide || img.Height > MaxSide {
		return nil, ErrTooLarge
	}
	c := &converter{width: img.Width, height: img.Height, bits: img.Bits, data: img.Data, space: &img.Space}
	if img.Stencil {
		// A stencil paints where its samples are 0, unless /Decode says the
		// reverse. Painted comes out black, which is sample value 0 too.
		c.bits, c.n, c.space = 1, 1, &Space{Model: Gray}
	} else {
		if !validBits(c.bits) {
			return nil, ErrBadImage
		}
		c.n = c.space.Components()
		if c.n == 0 {
			return nil, ErrBadImage
		}
		if err := checkSpace(c.space); err != nil {
			return nil, err
		}
	}
	var ok bool
	if c.rowBytes, ok = rowSize(c.width, c.n, c.bits); !ok {
		return nil, ErrBadImage
	}

	decode := img.Decode
	if len(decode) < 2*c.n || !finite(decode[:2*c.n]) {
		decode = nil
	}
	if img.Alpha != nil && !img.Stencil {
		a := img.Alpha
		if a.Width < 1 || a.Height < 1 || !validBits(a.Bits) {
			return nil, ErrBadImage
		}
		if a.Width > MaxSide || a.Height > MaxSide {
			return nil, ErrTooLarge
		}
		c.alpha = newAlphaPlane(a)
		if len(a.Matte) == c.n && finite(a.Matte) && a.Width == c.width && a.Height == c.height {
			c.matte = a.Matte
		}
	}
	if len(img.ColorKey) >= 2*c.n && !img.Stencil {
		c.key = img.ColorKey[:2*c.n]
	}
	masked := c.alpha != nil || c.key != nil

	// Decide what the PNG holds, keeping the samples as they are where PNG
	// can hold them.
	model := c.space.Model
	plain := decode == nil || isDecode(decode, 0, 1)
	flipped := decode != nil && isDecode(decode, 1, 0)
	switch {
	case (model == Gray || model == Tint) && !masked && (plain || flipped):
		c.direct, c.depth, c.colorType = true, c.bits, ctGray
		// Ink is the reverse of light.
		c.invert = flipped != (model == Tint)
		return c, nil
	case model == RGB && !masked && plain && c.bits >= 8:
		c.direct, c.depth, c.colorType = true, c.bits, ctRGB
		return c, nil
	case model == Indexed && !masked && c.bits <= 8 && (decode == nil || isDecode(decode, 0, float64(int(1)<<c.bits-1))):
		c.direct, c.depth, c.colorType = true, c.bits, ctPalette
		colors := indexedColors(c.space)
		c.palette = make([]byte, 3<<c.bits)
		for i := 0; i < 1<<c.bits; i++ {
			// An index beyond the table is the last colour of the table.
			copy(c.palette[3*i:], colors[min(i, len(colors)-1)][:])
		}
		return c, nil
	}

	c.depth = 8
	c.grey = model == Gray || model == Tint
	channels := 3
	c.colorType = ctRGB
	if c.grey {
		channels, c.colorType = 1, ctGray
	}
	if masked {
		channels++
		c.colorType += 4 // grey or RGB, with alpha
	}
	if model == Indexed {
		c.colors = indexedColors(c.space)
	}
	c.scale = make([][2]float64, c.n)
	top := float64(int(1)<<c.bits - 1)
	for i := range c.scale {
		lo, hi := defaultDecode(c.space, i, top)
		if decode != nil {
			lo, hi = decode[2*i], decode[2*i+1]
		}
		c.scale[i] = [2]float64{lo, (hi - lo) / top}
	}
	if c.bits <= 8 {
		c.lut = make([][]float32, c.n)
		for i := range c.lut {
			c.lut[i] = make([]float32, 1<<c.bits)
			for v := range c.lut[i] {
				c.lut[i][v] = float32(c.scale[i][0] + float64(v)*c.scale[i][1])
			}
		}
	}
	c.samples = make([]uint16, c.width*c.n)
	c.comps = make([]float64, c.n)
	c.out = make([]byte, c.width*channels)
	return c, nil
}

// checkSpace reports whether a space is one that can be converted.
func checkSpace(s *Space) error {
	switch s.Model {
	case Gray, RGB, Tint, Lab:
	case CMYK:
		for _, slot := range s.Slots {
			if slot < -1 || slot > 3 {
				return ErrBadImage
			}
		}
	case Indexed:
		if s.Base == nil || s.Base.Model == Indexed || s.Base.Components() == 0 || s.HiVal < 0 || s.HiVal > 255 {
			return ErrBadImage
		}
		return checkSpace(s.Base)
	default:
		return ErrBadImage
	}
	return nil
}

// defaultDecode returns the values that the least and the greatest sample
// of component i stand for when the image has no /Decode array.
func defaultDecode(s *Space, i int, top float64) (lo, hi float64) {
	switch s.Model {
	case Indexed:
		return 0, top
	case Lab:
		if i == 0 {
			return 0, 100
		}
		return s.Range[2*i-2], s.Range[2*i-1]
	}
	return 0, 1
}

// isDecode reports whether every pair of a decode array is lo, hi.
func isDecode(decode []float64, lo, hi float64) bool {
	for i := 0; i+1 < len(decode); i += 2 {
		if decode[i] != lo || decode[i+1] != hi {
			return false
		}
	}
	return true
}

func finite(vs []float64) bool {
	for _, v := range vs {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}

// source returns row y of data, rowBytes long, padded with zeros where the
// data has run out.
func source(data []byte, y, rowBytes int, scratch *[]byte) (row []byte, whole bool) {
	start := int64(y) * int64(rowBytes)
	if start+int64(rowBytes) <= int64(len(data)) {
		return data[start : start+int64(rowBytes)], true
	}
	if *scratch == nil {
		*scratch = make([]byte, rowBytes)
	}
	clear(*scratch)
	if start < int64(len(data)) {
		copy(*scratch, data[start:])
	}
	return *scratch, false
}

// row returns row y of the PNG. The slice is reused for the next row.
func (c *converter) row(y int) []byte {
	in, whole := source(c.data, y, c.rowBytes, &c.in)
	if !whole {
		c.short = true
	}
	if c.direct {
		if !c.invert {
			return in
		}
		if c.out == nil {
			c.out = make([]byte, c.rowBytes)
		}
		for i, b := range in {
			c.out[i] = ^b
		}
		return c.out
	}

	unpack(in, c.bits, c.samples)
	if c.alpha != nil {
		c.alpha.seek(int(int64(y) * int64(c.alpha.height) / int64(c.height)))
	}
	out := c.out[:0]
	comps := c.comps
	for x := 0; x < c.width; x++ {
		px := c.samples[x*c.n : (x+1)*c.n]
		a := 1.0
		if c.alpha != nil {
			a = c.alpha.at(int(int64(x) * int64(c.alpha.width) / int64(c.width)))
		}
		if c.key != nil {
			keyed := true
			for i, v := range px {
				if int(v) < c.key[2*i] || int(v) > c.key[2*i+1] {
					keyed = false
					break
				}
			}
			if keyed {
				a = 0
			}
		}
		if c.lut != nil {
			for i, v := range px {
				comps[i] = float64(c.lut[i][v])
			}
		} else {
			for i, v := range px {
				comps[i] = c.scale[i][0] + float64(v)*c.scale[i][1]
			}
		}
		if c.matte != nil && a > 0 && a < 1 {
			for i := range comps {
				comps[i] = c.matte[i] + (comps[i]-c.matte[i])/a
			}
		}
		switch {
		case c.grey:
			g := comps[0]
			if c.space.Model == Tint {
				g = 1 - g
			}
			out = append(out, unit(g))
		case c.colors != nil:
			i := int(math.Round(comps[0]))
			rgb := c.colors[max(0, min(i, len(c.colors)-1))]
			out = append(out, rgb[0], rgb[1], rgb[2])
		default:
			r, g, b := toRGB(c.space, comps)
			out = append(out, unit(r), unit(g), unit(b))
		}
		if c.colorType == ctGrayAlpha || c.colorType == ctRGBA {
			out = append(out, unit(a))
		}
	}
	return out
}

// unit converts a value from 0 to 1 to a byte.
func unit(v float64) uint8 {
	switch {
	case !(v > 0): // also NaN
		return 0
	case v >= 1:
		return 255
	}
	return uint8(v*255 + 0.5)
}

// unpack reads the samples of a row.
func unpack(row []byte, bits int, out []uint16) {
	switch bits {
	case 8:
		for i := range out {
			out[i] = uint16(row[i])
		}
	case 16:
		for i := range out {
			out[i] = uint16(row[2*i])<<8 | uint16(row[2*i+1])
		}
	default:
		mask := byte(1)<<bits - 1
		for i := range out {
			bit := i * bits
			out[i] = uint16(row[bit>>3] >> (8 - bits - bit&7) & mask)
		}
	}
}

func newAlphaPlane(a *Alpha) *alphaPlane {
	p := &alphaPlane{width: a.Width, height: a.Height, bits: a.Bits, data: a.Data, have: -1}
	p.rowBytes, _ = rowSize(a.Width, 1, a.Bits)
	p.d0, p.d1 = a.Decode[0], a.Decode[1]
	if math.IsNaN(p.d0+p.d1) || math.IsInf(p.d0+p.d1, 0) {
		p.d0, p.d1 = 0, 1
	}
	if a.Bits <= 8 {
		top := float64(int(1)<<a.Bits - 1)
		p.lut = make([]uint8, 1<<a.Bits)
		for v := range p.lut {
			p.lut[v] = unit(p.d0 + float64(v)/top*(p.d1-p.d0))
		}
	}
	p.row = make([]uint16, a.Width)
	return p
}

// seek makes row y of the mask the current one.
func (p *alphaPlane) seek(y int) {
	if y == p.have {
		return
	}
	p.have = y
	in, whole := source(p.data, y, p.rowBytes, &p.in)
	if !whole && int64(y)*int64(p.rowBytes) >= int64(len(p.data)) {
		// No data for this row at all: opaque, so that the picture shows.
		p.have = -2
		return
	}
	unpack(in, p.bits, p.row)
}

// at returns the opacity at column x of the current row.
func (p *alphaPlane) at(x int) float64 {
	if p.have == -2 {
		return 1
	}
	v := p.row[x]
	if p.lut != nil {
		return float64(p.lut[v]) / 255
	}
	return min(1, max(0, p.d0+float64(v)/65535*(p.d1-p.d0)))
}

// toRGB converts a colour to red, green and blue from 0 to 1. The values
// may be out of range.
func toRGB(s *Space, c []float64) (r, g, b float64) {
	switch s.Model {
	case Gray:
		return c[0], c[0], c[0]
	case Tint:
		return 1 - c[0], 1 - c[0], 1 - c[0]
	case RGB:
		return c[0], c[1], c[2]
	case CMYK:
		var ink [4]float64
		if s.Slots == nil {
			copy(ink[:], c)
		} else {
			for i, slot := range s.Slots {
				if slot >= 0 {
					ink[slot] = c[i]
				}
			}
		}
		for i := range ink {
			ink[i] = min(1, max(0, ink[i]))
		}
		k := 1 - ink[3]
		return (1 - ink[0]) * k, (1 - ink[1]) * k, (1 - ink[2]) * k
	case Lab:
		return labToRGB(s, c[0], c[1], c[2])
	}
	return 0, 0, 0
}

// labToRGB converts CIE L*a*b* to sRGB. The colour is scaled from the
// space's white point to that of sRGB, which is not a full chromatic
// adaptation but keeps white white.
func labToRGB(s *Space, l, a, b float64) (float64, float64, float64) {
	l = min(100, max(0, l))
	a = min(s.Range[1], max(s.Range[0], a))
	b = min(s.Range[3], max(s.Range[2], b))
	fy := (l + 16) / 116
	fx, fz := fy+a/500, fy-b/200
	inv := func(t float64) float64 {
		if t > 6.0/29 {
			return t * t * t
		}
		return 3 * (6.0 / 29) * (6.0 / 29) * (t - 4.0/29)
	}
	// Relative to the white point; then onto D65, the white of sRGB.
	x, y, z := inv(fx)*0.9505, inv(fy), inv(fz)*1.0890
	gamma := func(v float64) float64 {
		if v <= 0.0031308 {
			return 12.92 * v
		}
		return 1.055*math.Pow(v, 1/2.4) - 0.055
	}
	return gamma(3.2406*x - 1.5372*y - 0.4986*z),
		gamma(-0.9689*x + 1.8758*y + 0.0415*z),
		gamma(0.0557*x - 0.2040*y + 1.0570*z)
}

// indexedColors returns the colours of an indexed space, converted to RGB.
// There is at least one.
func indexedColors(s *Space) [][3]uint8 {
	base := s.Base
	n := base.Components()
	colors := make([][3]uint8, s.HiVal+1)
	comps := make([]float64, n)
	for i := range colors {
		for j := range comps {
			v := 0.0
			if k := i*n + j; k < len(s.Lookup) {
				v = float64(s.Lookup[k]) / 255
			}
			lo, hi := defaultDecode(base, j, 1)
			comps[j] = lo + v*(hi-lo)
		}
		r, g, b := toRGB(base, comps)
		colors[i] = [3]uint8{unit(r), unit(g), unit(b)}
	}
	return colors
}
