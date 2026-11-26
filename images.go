// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package unpdf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"image/jpeg"
	"math"

	"github.com/gomcpgo/unpdf/internal/pdfobj"
	"github.com/gomcpgo/unpdf/internal/raster"
)

// WarnImage: an image was extracted without something that belongs to it.
// Its data stopped before the last row, or its mask could not be read.
const WarnImage = "image"

// ImageFormat names the format of the bytes that Image.Extract returns.
type ImageFormat string

const (
	// FormatPNG is a PNG file, converted from the image's samples.
	FormatPNG ImageFormat = "png"
	// FormatJPEG is the JPEG file the document holds, as it is.
	FormatJPEG ImageFormat = "jpeg"
	// FormatJPEG2000 is the JPEG 2000 file the document holds, as it is.
	FormatJPEG2000 ImageFormat = "jpeg2000"
	// FormatJBIG2 is a JBIG2 stream in the embedded form that PDF uses: a
	// sequence of segments with no file header. The global segments that
	// the image shares with others come first.
	FormatJBIG2 ImageFormat = "jbig2"
	// FormatCCITT is CCITT Group 3 or Group 4 fax data with no header. What
	// a decoder needs to know about it is in Image.CCITT.
	FormatCCITT ImageFormat = "ccitt"
)

// Extension returns the usual file name extension for the format, with its
// dot: ".png", ".jpg", ".jp2", ".jb2" or ".ccitt".
func (f ImageFormat) Extension() string {
	switch f {
	case FormatJPEG:
		return ".jpg"
	case FormatJPEG2000:
		return ".jp2"
	case FormatJBIG2:
		return ".jb2"
	case FormatCCITT:
		return ".ccitt"
	}
	return ".png"
}

// CCITTParams describes CCITT fax data, which has no header to say so
// itself.
type CCITTParams struct {
	// K selects the coding: negative for Group 4, zero for Group 3 in one
	// dimension, positive for Group 3 in two.
	K int
	// Columns and Rows are the size of the image in pixels, as the data
	// has it.
	Columns, Rows int
	// BlackIs1 reports that a 1 bit is black. Otherwise a 0 bit is.
	BlackIs1 bool
	// EncodedByteAlign reports that every row starts on a byte boundary.
	EncodedByteAlign bool
}

// Image is an image shown on a page.
type Image struct {
	// Box is where the image shows on the page. A picture that the page
	// crops has the box of the part left visible; Extract returns the whole
	// picture all the same.
	Box Box
	// Width and Height are the size of the image in pixels.
	Width, Height int
	// Format is the format of the bytes that Extract returns. JPEG, JPEG
	// 2000, JBIG2 and CCITT images are returned as the document holds
	// them; every other image is converted to PNG.
	Format ImageFormat
	// ColorSpace names the kind of colour space the image is in:
	// "DeviceGray", "DeviceRGB", "DeviceCMYK", "ICCBased", "Indexed",
	// "CalGray", "CalRGB", "Lab", "Separation" or "DeviceN". It is empty
	// for a stencil, and for a JPEG 2000 image that leaves the colour space
	// to its own data.
	ColorSpace string
	// BitsPerComponent is the number of bits in each colour component of a
	// pixel. It is 0 when a JPEG 2000 image leaves that to its own data.
	BitsPerComponent int
	// Stencil reports an image mask: one bit per pixel that says where the
	// current colour is painted. Bilevel scans and glyphs drawn as images
	// are stored this way. It comes out black on white.
	Stencil bool
	// Masked reports that the image has a mask, which makes parts of it
	// transparent. A PNG carries the mask as its alpha channel. The other
	// formats are returned as they are, without it.
	Masked bool
	// Inline reports an image written into the page's content, as small
	// images sometimes are, and not stored as an object of its own.
	Inline bool
	// Small reports an image too small to be a picture: a bullet, an icon,
	// a rule, a spacer. Strips and tiles that make up a larger picture
	// between them are not small.
	Small bool
	// Repeated reports an image that appears in the same place on the pages
	// around this one, as a logo or a letterhead does.
	Repeated bool
	// CCITT is set for an image in FormatCCITT.
	CCITT *CCITTParams

	src *imageSource
	key imageKey
}

// imageSource is where an image's data is.
type imageSource struct {
	doc    *Document
	page   int
	dict   pdfobj.Dict
	stream *pdfobj.Stream // nil for an inline image
	inline []byte
	// dropped reports an inline image whose data was not kept, because the
	// inline images of its page hold more than the limit.
	dropped bool
}

// imageKey identifies the picture an image shows, so that the same picture
// can be recognised on another page. The zero key matches nothing.
type imageKey struct {
	kind          uint8
	width, height int
	a, b          uint64
}

const (
	keyObject = iota + 1 // an image stream with no filter: a is its object number
	keyStream            // a compressed image stream: a is its stored length
	keyInline            // an inline image: a is the length of its data, b a hash of it
)

// ImageOptions adjusts Page.Images. The zero value leaves out the images
// that are rarely wanted.
type ImageOptions struct {
	// Small keeps the images marked Small, which are left out by default.
	Small bool
	// Repeated keeps the images marked Repeated, which are left out by
	// default.
	Repeated bool
}

const (
	// smallImagePoints is the width or height on the page, in points, under
	// which an image is small: about the height of a line of text.
	smallImagePoints = 16.0
	// smallImagePixels is the width or height in pixels under which an
	// image is small however large it is drawn.
	smallImagePixels = 4
	// tileSlack is how far apart, in points, the edges of two images may be
	// and still touch.
	tileSlack = 1.0
	// maxTileImages caps the images of a page that are tested for being
	// tiles of one picture, which compares each with every other.
	maxTileImages = 4000
	// maxPageImages caps the images kept for one page, and
	// maxInlineImageBytes the data kept for those of them that are inline.
	maxPageImages       = 10_000
	maxInlineImageBytes = 16 << 20
	// defaultMaxImagePixels is the default of Limits.MaxImagePixels, and
	// maxJPEGMaskPixels caps a mask stored as JPEG, the one kind of image
	// that is decoded by code whose memory use this package does not set.
	defaultMaxImagePixels = 1 << 27
	maxJPEGMaskPixels     = 1 << 24
	// repeatReach is how many pages before and after a page it is compared
	// with to find repeated images, repeatSlack how far, in points, an
	// image may move between pages and still be in the same place, and
	// maxImageMarks how many images of a page are remembered for it.
	repeatReach   = furnitureReach
	repeatSlack   = 2.0
	maxImageMarks = 256
)

// Images returns the images on the page, in the order they are drawn, each
// with its position and size. Extract gives the bytes of one.
//
// By default the images that are rarely wanted are left out: the small ones
// and those that repeat from page to page. ImageOptions keeps them.
//
// Images drawn by a form or an annotation are included. Those outside the
// visible part of the page are not, nor are the pictures of a tiling
// pattern or of a font's glyphs, nor those that only shape a mask for
// other drawing, as the soft edge of a shadow does.
func (p *Page) Images(ctx context.Context, opts ImageOptions) ([]Image, error) {
	all, err := p.allImages(ctx)
	if err != nil {
		return nil, err
	}
	if err := p.markRepeated(ctx); err != nil {
		return nil, err
	}
	out := make([]Image, 0, len(all))
	for _, img := range all {
		if (img.Small && !opts.Small) || (img.Repeated && !opts.Repeated) {
			continue
		}
		out = append(out, img)
	}
	return out, nil
}

// allImages returns every image of the page, with Small set but not yet
// Repeated.
func (p *Page) allImages(ctx context.Context) ([]Image, error) {
	if p.imaged {
		return p.images, nil
	}
	pc, err := p.interpret(ctx)
	if err != nil {
		return nil, publicErr(err)
	}
	f := p.doc.f
	width, height := p.Size()
	var out []Image
	for i := range pc.images {
		ci := &pc.images[i]
		if ci.Hidden {
			continue
		}
		dict := ci.Dict
		if ci.Stream != nil {
			dict = ci.Stream.Dict
		}
		w, _ := f.Int(ctx, dict["Width"])
		h, _ := f.Int(ctx, dict["Height"])
		if w < 1 || h < 1 || w > math.MaxInt32 || h > math.MaxInt32 {
			continue // not an image that can be shown
		}
		// What shows of it lies inside the page.
		box := p.displayBox(Rect{ci.VX0, ci.VY0, ci.VX1, ci.VY1})
		box.X0, box.Y0 = math.Max(box.X0, 0), math.Max(box.Y0, 0)
		box.X1, box.Y1 = math.Min(box.X1, width), math.Min(box.Y1, height)
		if box.X1 <= box.X0 || box.Y1 <= box.Y0 {
			continue
		}
		img := Image{
			Box: box, Width: int(w), Height: int(h), Format: FormatPNG, Inline: ci.Inline,
			src: &imageSource{doc: p.doc, page: p.Number, dict: dict, stream: ci.Stream, inline: ci.Data, dropped: ci.Inline && ci.Data == nil && pc.inlineDropped},
		}
		filter, parms := f.ImageFilter(ctx, dict)
		switch filter {
		case "DCTDecode":
			img.Format = FormatJPEG
		case "JPXDecode":
			img.Format = FormatJPEG2000
		case "JBIG2Decode":
			img.Format = FormatJBIG2
		case "CCITTFaxDecode":
			img.Format = FormatCCITT
			img.CCITT = p.doc.ccittParams(ctx, parms, img.Height)
		}
		if img.Stencil, _ = f.Bool(ctx, dict["ImageMask"]); img.Stencil {
			img.BitsPerComponent = 1
		} else {
			bits, _ := f.Int(ctx, dict["BitsPerComponent"])
			img.BitsPerComponent = int(max(0, min(bits, 64)))
			img.ColorSpace = p.doc.colorSpaceName(ctx, dict["ColorSpace"])
			img.Masked = dict["SMask"] != nil || dict["Mask"] != nil
		}
		img.key = p.imageKey(ctx, &img)
		out = append(out, img)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	markSmall(out)
	p.images, p.imaged = out, true
	return out, nil
}

// ccittParams reads the decode parameters of CCITT fax data. The data may
// leave the number of rows open, and then it is the height of the image.
func (d *Document) ccittParams(ctx context.Context, parms pdfobj.Dict, height int) *CCITTParams {
	f := d.f
	num := func(key pdfobj.Name, def int) int {
		v, ok := f.Int(ctx, parms[key])
		if !ok || v < math.MinInt32 || v > math.MaxInt32 {
			return def
		}
		return int(v)
	}
	out := &CCITTParams{K: num("K", 0), Columns: num("Columns", 1728), Rows: num("Rows", 0)}
	out.BlackIs1, _ = f.Bool(ctx, parms["BlackIs1"])
	out.EncodedByteAlign, _ = f.Bool(ctx, parms["EncodedByteAlign"])
	if out.Rows <= 0 {
		out.Rows = height
	}
	return out
}

// imageKey works out what identifies the picture of an image.
func (p *Page) imageKey(ctx context.Context, img *Image) imageKey {
	src := img.src
	key := imageKey{width: img.Width, height: img.Height}
	switch {
	case src.stream == nil:
		if len(src.inline) == 0 {
			return imageKey{}
		}
		h := fnv.New64a()
		h.Write(src.inline)
		key.kind, key.a, key.b = keyInline, uint64(len(src.inline)), h.Sum64()
	case src.dict["Filter"] == nil:
		// The stored length of samples with no compression is the same for
		// every image of that size, so only the object itself will do.
		if src.stream.Num == 0 {
			return imageKey{}
		}
		key.kind, key.a = keyObject, uint64(src.stream.Num)
	default:
		// Two compressed images of the same size and the same stored length
		// are the same picture, stored twice.
		length, ok := p.doc.f.Int(ctx, src.dict["Length"])
		if !ok || length <= 0 {
			if src.stream.Num == 0 {
				return imageKey{}
			}
			key.kind, key.a = keyObject, uint64(src.stream.Num)
			break
		}
		key.kind, key.a = keyStream, uint64(length)
	}
	return key
}

// markSmall sets Small on the images too small to be pictures. An image
// that is one of the strips or tiles of a larger picture is not small:
// scanners and page-layout programs cut pictures up that way.
func markSmall(images []Image) {
	thin := func(img *Image) bool {
		return img.Box.X1-img.Box.X0 < smallImagePoints || img.Box.Y1-img.Box.Y0 < smallImagePoints ||
			img.Width < smallImagePixels || img.Height < smallImagePixels
	}
	// touches reports whether b lies against one side of a, along nearly
	// the whole of that side.
	touches := func(a, b Box) bool {
		along := func(a0, a1, b0, b1 float64) bool {
			return math.Min(a1, b1)-math.Max(a0, b0) >= 0.9*math.Max(a1-a0, b1-b0)
		}
		if math.Abs(a.Y1-b.Y0) <= tileSlack || math.Abs(a.Y0-b.Y1) <= tileSlack {
			if along(a.X0, a.X1, b.X0, b.X1) {
				return true
			}
		}
		if math.Abs(a.X1-b.X0) <= tileSlack || math.Abs(a.X0-b.X1) <= tileSlack {
			return along(a.Y0, a.Y1, b.Y0, b.Y1)
		}
		return false
	}
	for i := range images {
		img := &images[i]
		if !thin(img) {
			continue
		}
		img.Small = true
		if len(images) > maxTileImages {
			continue
		}
		for j := range images {
			if j != i && touches(img.Box, images[j].Box) {
				img.Small = false
				break
			}
		}
	}
}

// imageMark is an image of a page reduced to what tells whether another
// page shows the same picture in the same place.
type imageMark struct {
	key imageKey
	box Box
}

func (m imageMark) matches(key imageKey, box Box) bool {
	return m.key == key && math.Abs(m.box.X0-box.X0) <= repeatSlack && math.Abs(m.box.Y0-box.Y0) <= repeatSlack &&
		math.Abs(m.box.X1-box.X1) <= repeatSlack && math.Abs(m.box.Y1-box.Y1) <= repeatSlack
}

// imageMarks returns the marks of the images of page n.
func (d *Document) imageMarks(ctx context.Context, n int) ([]imageMark, error) {
	if marks, ok := d.markCache[n]; ok {
		return marks, nil
	}
	page, err := d.Page(ctx, n)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr
		}
		return nil, nil // a page that cannot be read shows no images
	}
	images, err := page.allImages(ctx)
	if err != nil {
		return nil, err
	}
	marks := []imageMark{}
	for i := range images {
		if images[i].key.kind != 0 && len(marks) < maxImageMarks {
			marks = append(marks, imageMark{images[i].key, images[i].Box})
		}
	}
	if d.markCache == nil || len(d.markCache) >= maxBandCache {
		d.markCache = map[int][]imageMark{}
	}
	d.markCache[n] = marks
	return marks, nil
}

// markRepeated sets Repeated on the images of the page that the pages
// around it show too, in the same place. A logo is on every page, or on
// every other page where left and right pages differ: so an image is
// repeated when at least half of the pages within reach have it, and at
// least two. Comparing with neighbours, and not with the whole document,
// keeps the work to a few pages, as for running heads.
func (p *Page) markRepeated(ctx context.Context) error {
	if p.repeatsMarked {
		return nil
	}
	d := p.doc
	total := d.NumPages()
	var around [][]imageMark
	if total >= 3 && len(p.images) > 0 {
		for m := p.Number - repeatReach; m <= p.Number+repeatReach; m++ {
			if m < 1 || m > total || m == p.Number {
				continue
			}
			marks, err := d.imageMarks(ctx, m)
			if err != nil {
				return err
			}
			around = append(around, marks)
		}
	}
	need := max(2, len(around)/2)
	for i := range p.images {
		img := &p.images[i]
		if img.key.kind == 0 {
			continue
		}
		hits := 0
		for _, marks := range around {
			for _, m := range marks {
				if m.matches(img.key, img.Box) {
					hits++
					break
				}
			}
		}
		img.Repeated = hits >= need
	}
	p.repeatsMarked = true
	return nil
}

// Extract returns the image as a file in the format that Format names: a
// JPEG or JPEG 2000 file, JBIG2 or CCITT data as the document holds it, or
// a PNG file made from the image's samples.
//
// A PNG keeps the samples as they are where it can: grey and RGB at their
// own bit depth, an indexed image as a palette image. CMYK, Lab and the
// rest are converted to 8-bit RGB by the plain formulas, with no colour
// management, and a single ink of a Separation space is shown as grey. A
// mask becomes the alpha channel. A stencil comes out black where it
// paints, on white.
//
// The error is an *UnsupportedError for a colour space that cannot be
// converted, and a *LimitError for an image over Limits.MaxImagePixels or
// wider or taller than 65,536 pixels. The Document must still be open.
func (img *Image) Extract(ctx context.Context) ([]byte, error) {
	src := img.src
	if src == nil {
		return nil, errors.New("unpdf: the image does not come from a page")
	}
	if src.dropped {
		return nil, &LimitError{What: "inline image data on one page", Limit: maxInlineImageBytes}
	}
	if img.Format != FormatPNG {
		data, err := src.encoded(ctx)
		if err != nil {
			return nil, err
		}
		if img.Format == FormatJBIG2 {
			return src.withGlobals(ctx, data), nil
		}
		return data, nil
	}

	d := src.doc
	ri, err := d.rasterImage(ctx, img)
	if err != nil {
		return nil, err
	}
	if ri.Data, err = src.encoded(ctx); err != nil {
		return nil, err
	}
	out, short, err := raster.PNG(ctx, ri)
	switch {
	case errors.Is(err, raster.ErrTooLarge):
		return nil, &LimitError{What: "image width or height", Limit: raster.MaxSide}
	case errors.Is(err, raster.ErrBadImage):
		return nil, fmt.Errorf("unpdf: page %d: the image is not a valid one", src.page)
	case err != nil:
		return nil, err
	}
	if short {
		d.f.Warn(WarnImage, "page %d: the data of an image of %d x %d pixels stops early; the rest is black", src.page, img.Width, img.Height)
	}
	return out, nil
}

// encoded returns the image's data with the filters undone that are not
// image formats themselves. The slice is the caller's to keep.
func (src *imageSource) encoded(ctx context.Context) ([]byte, error) {
	f := src.doc.f
	if src.stream != nil {
		data, err := f.DecodeImage(ctx, src.stream)
		return data, publicErr(err)
	}
	data, err := f.DecodeInline(ctx, src.dict, src.inline)
	if err != nil {
		return nil, publicErr(err)
	}
	if len(data) > 0 && len(src.inline) > 0 && &data[0] == &src.inline[0] {
		data = bytes.Clone(data)
	}
	return data, nil
}

// withGlobals puts the global segments of a JBIG2 image before its own.
func (src *imageSource) withGlobals(ctx context.Context, data []byte) []byte {
	f := src.doc.f
	_, parms := f.ImageFilter(ctx, src.dict)
	globals := f.Stream(ctx, parms["JBIG2Globals"])
	if globals == nil {
		return data
	}
	head, err := f.DecodeStream(ctx, globals)
	if err != nil {
		f.Warn(WarnImage, "page %d: the global segments of a JBIG2 image could not be read: %v", src.page, err)
		return data
	}
	return append(head[:len(head):len(head)], data...)
}

// rasterImage describes an image for conversion to PNG: everything but its
// samples.
func (d *Document) rasterImage(ctx context.Context, img *Image) (*raster.Image, error) {
	f := d.f
	dict := img.src.dict
	if int64(img.Width)*int64(img.Height) > d.maxImagePixels {
		return nil, &LimitError{What: "image pixels", Limit: d.maxImagePixels}
	}
	ri := &raster.Image{Width: img.Width, Height: img.Height, Decode: d.numbers(ctx, dict["Decode"], 64)}
	if img.Stencil {
		ri.Stencil = true
		return ri, nil
	}
	ri.Bits = img.BitsPerComponent
	space, err := d.colorSpace(ctx, dict["ColorSpace"], 0)
	if err != nil {
		return nil, err
	}
	ri.Space = space

	// A soft mask gives every pixel an opacity. Failing that, /Mask is a
	// stencil that says where the image shows, or a range of colours that
	// do not.
	if sm := f.Stream(ctx, dict["SMask"]); sm != nil {
		ri.Alpha = d.alpha(ctx, img.src.page, sm, false)
		if ri.Alpha != nil {
			ri.Alpha.Matte = d.numbers(ctx, sm.Dict["Matte"], 32)
		}
	} else {
		switch v := resolveQuiet(ctx, f, dict["Mask"]).(type) {
		case *pdfobj.Stream:
			ri.Alpha = d.alpha(ctx, img.src.page, v, true)
		case pdfobj.Array:
			for _, n := range d.numbers(ctx, v, 64) {
				ri.ColorKey = append(ri.ColorKey, int(math.Max(-1, math.Min(n, 1<<16))))
			}
		}
	}
	return ri, ctx.Err()
}

// alpha reads the mask of an image: a soft mask, or with stencil a stencil
// mask. A mask that cannot be read is left out, with a warning, so that
// the image is still returned.
func (d *Document) alpha(ctx context.Context, page int, st *pdfobj.Stream, stencil bool) *raster.Alpha {
	f := d.f
	skip := func(why string) *raster.Alpha {
		if ctx.Err() == nil {
			f.Warn(WarnImage, "page %d: the mask of an image was left out: %s", page, why)
		}
		return nil
	}
	w, _ := f.Int(ctx, st.Dict["Width"])
	h, _ := f.Int(ctx, st.Dict["Height"])
	if w < 1 || h < 1 || w > raster.MaxSide || h > raster.MaxSide || w*h > d.maxImagePixels {
		return skip("its size is missing or too large")
	}
	a := &raster.Alpha{Width: int(w), Height: int(h), Bits: 1, Decode: [2]float64{0, 1}}
	if stencil {
		a.Decode = [2]float64{1, 0} // a 0 bit is where the image shows
	} else {
		bits, _ := f.Int(ctx, st.Dict["BitsPerComponent"])
		a.Bits = int(max(0, min(bits, 64)))
	}
	if dec := d.numbers(ctx, st.Dict["Decode"], 2); len(dec) == 2 && dec[0] > dec[1] {
		a.Decode[0], a.Decode[1] = a.Decode[1], a.Decode[0]
	}
	data, err := f.DecodeImage(ctx, st)
	if err != nil {
		return skip(err.Error())
	}
	switch filter, _ := f.ImageFilter(ctx, st.Dict); filter {
	case "":
		a.Data = data
	case "DCTDecode":
		// A mask stored as a JPEG file, which the standard library reads.
		// Its decoder allocates by the size the file claims, so only a
		// grey image of the expected size, and not a huge one, is let in.
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width != a.Width || cfg.Height != a.Height || cfg.ColorModel != color.GrayModel {
			return skip("its JPEG data is not a grey image of its size")
		}
		if w*h > maxJPEGMaskPixels {
			return skip("it is a JPEG file too large to decode")
		}
		decoded, err := jpeg.Decode(bytes.NewReader(data))
		grey, ok := decoded.(*image.Gray)
		if err != nil || !ok || grey.Stride != a.Width || len(grey.Pix) < a.Width*a.Height {
			return skip("its JPEG data could not be decoded")
		}
		a.Bits, a.Data = 8, grey.Pix
	default:
		return skip("it is stored with " + filter)
	}
	return a
}

// numbers reads an array of numbers, at most limit of them. Anything else
// in the array ends it.
func (d *Document) numbers(ctx context.Context, o pdfobj.Object, limit int) []float64 {
	arr := d.f.Array(ctx, o)
	if len(arr) > limit {
		arr = arr[:limit]
	}
	var out []float64
	for _, item := range arr {
		v, ok := d.f.Number(ctx, item)
		if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
			break
		}
		out = append(out, v)
	}
	return out
}

// colorSpaceName returns the family of a colour space: the name it starts
// with, written out.
func (d *Document) colorSpaceName(ctx context.Context, o pdfobj.Object) string {
	var name pdfobj.Name
	switch v := resolveQuiet(ctx, d.f, o).(type) {
	case pdfobj.Name:
		name = v
	case pdfobj.Array:
		if len(v) > 0 {
			name = d.f.Name(ctx, v[0])
		}
	}
	switch name {
	case "G":
		return "DeviceGray"
	case "RGB":
		return "DeviceRGB"
	case "CMYK":
		return "DeviceCMYK"
	case "I":
		return "Indexed"
	}
	return string(name)
}

// maxColorSpaceDepth caps how deep one colour space may be defined in
// terms of another.
const maxColorSpaceDepth = 4

// colorSpace reads a colour space as far as converting an image needs.
func (d *Document) colorSpace(ctx context.Context, o pdfobj.Object, depth int) (raster.Space, error) {
	f := d.f
	unsupported := func(name pdfobj.Name) (raster.Space, error) {
		if name == "" {
			return raster.Space{}, errors.New("unpdf: the image has no usable colour space")
		}
		return raster.Space{}, &UnsupportedError{Feature: "image colour space " + string(name)}
	}
	if depth > maxColorSpaceDepth {
		return unsupported("")
	}
	var family pdfobj.Name
	var arr pdfobj.Array
	switch v := resolveQuiet(ctx, f, o).(type) {
	case pdfobj.Name:
		family = v
	case pdfobj.Array:
		if len(v) == 0 {
			return unsupported("")
		}
		family, arr = f.Name(ctx, v[0]), v
	default:
		return unsupported("")
	}
	arg := func(i int) pdfobj.Object {
		if i < len(arr) {
			return arr[i]
		}
		return nil
	}
	// The inks of the printing process, as a Separation or DeviceN space
	// may name them.
	inks := map[pdfobj.Name]int{"Cyan": 0, "Magenta": 1, "Yellow": 2, "Black": 3, "None": -1}

	switch family {
	case "DeviceGray", "G", "CalGray":
		return raster.Space{Model: raster.Gray}, nil
	case "DeviceRGB", "RGB", "CalRGB":
		return raster.Space{Model: raster.RGB}, nil
	case "DeviceCMYK", "CMYK":
		return raster.Space{Model: raster.CMYK}, nil
	case "Lab":
		space := raster.Space{Model: raster.Lab, Range: [4]float64{-100, 100, -100, 100}}
		if r := d.numbers(ctx, f.Dict(ctx, arg(1))["Range"], 4); len(r) == 4 && r[0] < r[1] && r[2] < r[3] {
			copy(space.Range[:], r)
		}
		return space, nil
	case "ICCBased":
		// The profile is not applied: the number of its components says
		// which device space stands in for it.
		profile := f.Dict(ctx, arg(1))
		switch n, _ := f.Int(ctx, profile["N"]); n {
		case 1:
			return raster.Space{Model: raster.Gray}, nil
		case 3:
			return raster.Space{Model: raster.RGB}, nil
		case 4:
			return raster.Space{Model: raster.CMYK}, nil
		}
		if alt := profile["Alternate"]; alt != nil {
			return d.colorSpace(ctx, alt, depth+1)
		}
		return unsupported(family)
	case "Indexed", "I":
		base, err := d.colorSpace(ctx, arg(1), depth+1)
		if err != nil {
			return raster.Space{}, err
		}
		if base.Model == raster.Indexed {
			return unsupported("")
		}
		hival, _ := f.Int(ctx, arg(2))
		space := raster.Space{Model: raster.Indexed, Base: &base, HiVal: int(max(0, min(hival, 255)))}
		switch table := resolveQuiet(ctx, f, arg(3)).(type) {
		case pdfobj.String:
			space.Lookup = table
		case *pdfobj.Stream:
			data, err := f.DecodeStream(ctx, table)
			if err != nil {
				return raster.Space{}, publicErr(err)
			}
			space.Lookup = data
		}
		if most := (space.HiVal + 1) * base.Components(); len(space.Lookup) > most {
			space.Lookup = space.Lookup[:most]
		}
		return space, nil
	case "Separation":
		// One ink. A process ink is that ink; any other is shown as grey,
		// since the function that gives its colour is not evaluated.
		if slot, ok := inks[f.Name(ctx, arg(1))]; ok && slot >= 0 && slot < 3 {
			return raster.Space{Model: raster.CMYK, Slots: []int{slot}}, nil
		}
		return raster.Space{Model: raster.Tint}, nil
	case "DeviceN":
		names := f.Array(ctx, arg(1))
		if len(names) == 0 || len(names) > 32 {
			return unsupported(family)
		}
		slots := make([]int, len(names))
		process := true
		for i, n := range names {
			slot, ok := inks[f.Name(ctx, n)]
			slots[i] = slot
			process = process && ok
		}
		switch {
		case process:
			return raster.Space{Model: raster.CMYK, Slots: slots}, nil
		case len(names) == 1:
			return raster.Space{Model: raster.Tint}, nil
		}
		return unsupported(family)
	}
	return unsupported(family)
}
