// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package unpdf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"github.com/gomcpgo/unpdf/internal/pdftest"
)

// imagePage builds a one-page document and returns its page. xobjects adds
// objects and returns the entries of the page's XObject dictionary; extra
// is more entries for the page dictionary.
func imagePage(t *testing.T, content, extra string, xobjects func(b *pdftest.Builder) string) (*Document, *Page) {
	t.Helper()
	b := pdftest.New()
	entries := ""
	if xobjects != nil {
		entries = xobjects(b)
	}
	b.Pages(pdftest.PageSpec{Content: content, Extra: extra, Resources: "<< /XObject << " + entries + " >> >>"})
	doc := openBytes(t, b.Bytes(), Options{})
	p, err := doc.Page(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	return doc, p
}

var everyImage = ImageOptions{Small: true, Repeated: true}

// oneImage returns the pixels of the only image of a page whose content
// draws /Im, as 8-bit R, G, B, A.
func oneImage(t *testing.T, dict string, data []byte, more func(b *pdftest.Builder) string) []uint8 {
	t.Helper()
	_, p := imagePage(t, "q 100 0 0 100 0 0 cm /Im Do Q", "", func(b *pdftest.Builder) string {
		rest := ""
		if more != nil {
			rest = more(b)
		}
		return fmt.Sprintf("/Im %d 0 R", b.AddStream("<< /Subtype /Image "+dict+" "+rest+" >>", data))
	})
	images, err := p.Images(ctx, everyImage)
	if err != nil || len(images) != 1 {
		t.Fatalf("got %d images, err %v", len(images), err)
	}
	if images[0].Format != FormatPNG {
		t.Fatalf("format = %s, want png", images[0].Format)
	}
	out, err := images[0].Extract(ctx)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return pngPixels(t, out)
}

func pngPixels(t *testing.T, data []byte) []uint8 {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("the PNG does not decode: %v", err)
	}
	var out []uint8
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			out = append(out, c.R, c.G, c.B, c.A)
		}
	}
	return out
}

func TestImagesListed(t *testing.T) {
	_, p := imagePage(t,
		"q 200 0 0 100 50 600 cm /RGB Do Q "+ // an image of its own
			"q 0 0 1 rg 72 0 0 72 300 600 cm /Stencil Do Q "+ // a stencil
			"q 1 0 0 1 100 300 cm /Form Do Q "+ // an image drawn by a form
			"q 50 0 0 50 400 100 cm BI /W 2 /H 1 /BPC 8 /CS /RGB /F /AHx ID ff0000 00ff00> EI Q "+ // an inline image
			"q 100 0 0 80 50 100 cm /JPEG Do Q q 100 0 0 80 200 100 cm /JPX Do Q "+
			"q 30 0 0 30 500 400 cm /Fax Do Q q 30 0 0 30 500 500 cm /JBIG Do Q",
		"", func(b *pdftest.Builder) string {
			rgb := b.AddStream("<< /Subtype /Image /Width 2 /Height 2 /ColorSpace /DeviceRGB /BitsPerComponent 8 >>", bytes.Repeat([]byte{9}, 12))
			stencil := b.AddStream("<< /Subtype /Image /Width 8 /Height 2 /ImageMask true >>", []byte{0xF0, 0x0F})
			mask := b.AddStream("<< /Subtype /Image /Width 3 /Height 1 /ColorSpace /DeviceGray /BitsPerComponent 8 >>", []byte{0, 128, 255})
			grey := b.AddStream(fmt.Sprintf("<< /Subtype /Image /Width 3 /Height 1 /ColorSpace [/ICCBased %d 0 R] /BitsPerComponent 8 /SMask %d 0 R >>",
				b.AddStream("<< /N 1 >>", nil), mask), []byte{1, 2, 3})
			form := b.AddStream(fmt.Sprintf("<< /Subtype /Form /BBox [0 0 100 100] /Resources << /XObject << /In %d 0 R >> >> >>", grey),
				[]byte("q 60 0 0 20 10 10 cm /In Do Q"))
			jpg := b.AddStream("<< /Subtype /Image /Width 640 /Height 480 /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter [/ASCIIHexDecode /DCTDecode] >>", []byte("FFD8 FFD9>"))
			jpx := b.AddStream("<< /Subtype /Image /Width 64 /Height 48 /Filter /JPXDecode >>", []byte("a JPEG 2000 file"))
			fax := b.AddStream("<< /Subtype /Image /Width 16 /Height 8 /ColorSpace /DeviceGray /BitsPerComponent 1 /Filter /CCITTFaxDecode "+
				"/DecodeParms << /K -1 /Columns 16 /BlackIs1 true >> >>", []byte("fax data"))
			globals := b.AddStream("<< >>", []byte("globals, "))
			jbig := b.AddStream(fmt.Sprintf("<< /Subtype /Image /Width 16 /Height 8 /ImageMask true /Filter /JBIG2Decode /DecodeParms << /JBIG2Globals %d 0 R >> >>", globals), []byte("segments"))
			return fmt.Sprintf("/RGB %d 0 R /Stencil %d 0 R /Form %d 0 R /JPEG %d 0 R /JPX %d 0 R /Fax %d 0 R /JBIG %d 0 R", rgb, stencil, form, jpg, jpx, fax, jbig)
		})
	images, err := p.Images(ctx, everyImage)
	if err != nil {
		t.Fatal(err)
	}
	type want struct {
		box           Box
		width, height int
		format        ImageFormat
		space         string
		bits          int
		flags         string
		data          string // what Extract returns, for the formats returned as they are
	}
	wants := []want{
		// The page is 792 points high; boxes are measured from its top. An
		// image of fewer than four pixels either way is small.
		{Box{50, 92, 250, 192}, 2, 2, FormatPNG, "DeviceRGB", 8, "small", ""},
		{Box{300, 120, 372, 192}, 8, 2, FormatPNG, "", 1, "stencil,small", ""},
		{Box{110, 462, 170, 482}, 3, 1, FormatPNG, "ICCBased", 8, "masked,small", ""},
		{Box{400, 642, 450, 692}, 2, 1, FormatPNG, "DeviceRGB", 8, "inline,small", ""},
		{Box{50, 612, 150, 692}, 640, 480, FormatJPEG, "DeviceRGB", 8, "", "\xFF\xD8\xFF\xD9"},
		{Box{200, 612, 300, 692}, 64, 48, FormatJPEG2000, "", 0, "", "a JPEG 2000 file"},
		{Box{500, 362, 530, 392}, 16, 8, FormatCCITT, "DeviceGray", 1, "", "fax data"},
		{Box{500, 262, 530, 292}, 16, 8, FormatJBIG2, "", 1, "stencil", "globals, segments"},
	}
	if len(images) != len(wants) {
		t.Fatalf("got %d images, want %d: %+v", len(images), len(wants), images)
	}
	for i, w := range wants {
		img := images[i]
		var flags []string
		for _, f := range []struct {
			name string
			set  bool
		}{{"stencil", img.Stencil}, {"masked", img.Masked}, {"inline", img.Inline}, {"small", img.Small}, {"repeated", img.Repeated}} {
			if f.set {
				flags = append(flags, f.name)
			}
		}
		if !closeTo(img.Box, w.box) || img.Width != w.width || img.Height != w.height || img.Format != w.format ||
			img.ColorSpace != w.space || img.BitsPerComponent != w.bits || strings.Join(flags, ",") != w.flags {
			t.Errorf("image %d: %+v\n flags %v; want %+v", i+1, img, flags, w)
		}
		data, err := img.Extract(ctx)
		if err != nil {
			t.Errorf("image %d: Extract: %v", i+1, err)
			continue
		}
		if w.format == FormatPNG {
			if cfg, err := png.DecodeConfig(bytes.NewReader(data)); err != nil || cfg.Width != w.width || cfg.Height != w.height {
				t.Errorf("image %d: the PNG is %d x %d, err %v", i+1, cfg.Width, cfg.Height, err)
			}
		} else if string(data) != w.data {
			t.Errorf("image %d: Extract = %q, want %q", i+1, data, w.data)
		}
	}
	if c := images[6].CCITT; c == nil || *c != (CCITTParams{K: -1, Columns: 16, Rows: 8, BlackIs1: true}) {
		t.Errorf("CCITT parameters = %+v", c)
	}
	// The inline image, and the masked one with its alpha.
	if got := pngPixels(t, must(images[3].Extract(ctx))); !bytes.Equal(got, []uint8{255, 0, 0, 255, 0, 255, 0, 255}) {
		t.Errorf("inline image = %v", got)
	}
	if got := pngPixels(t, must(images[2].Extract(ctx))); !bytes.Equal(got, []uint8{1, 1, 1, 0, 2, 2, 2, 128, 3, 3, 3, 255}) {
		t.Errorf("masked image = %v", got)
	}
	for f, ext := range map[ImageFormat]string{FormatPNG: ".png", FormatJPEG: ".jpg", FormatJPEG2000: ".jp2", FormatJBIG2: ".jb2", FormatCCITT: ".ccitt"} {
		if f.Extension() != ext {
			t.Errorf("%s: extension %s, want %s", f, f.Extension(), ext)
		}
	}
}

func must(data []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return data
}

func TestImageColour(t *testing.T) {
	const size = "/Width 2 /Height 1 /BitsPerComponent 8"
	tests := []struct {
		name, dict string
		data       []byte
		more       func(b *pdftest.Builder) string
		want       []uint8
	}{
		{"grey", size + " /ColorSpace /DeviceGray", []byte{0, 200}, nil, []uint8{0, 0, 0, 255, 200, 200, 200, 255}},
		{"grey reversed", size + " /ColorSpace /DeviceGray /Decode [1 0]", []byte{0, 200}, nil, []uint8{255, 255, 255, 255, 55, 55, 55, 255}},
		{"calibrated grey", size + " /ColorSpace [/CalGray << /WhitePoint [1 1 1] >>]", []byte{0, 200}, nil, []uint8{0, 0, 0, 255, 200, 200, 200, 255}},
		{"RGB", size + " /ColorSpace /DeviceRGB", []byte{1, 2, 3, 4, 5, 6}, nil, []uint8{1, 2, 3, 255, 4, 5, 6, 255}},
		{"RGB, compressed with a predictor", size + " /ColorSpace /DeviceRGB /Filter /FlateDecode /DecodeParms << /Predictor 12 /Colors 3 /Columns 2 >>",
			pdftest.Flate([]byte{1, 10, 20, 30, 5, 5, 5}), nil, []uint8{10, 20, 30, 255, 15, 25, 35, 255}}, // PNG "sub": each byte adds to the one a pixel before
		{"CMYK", size + " /ColorSpace /DeviceCMYK", []byte{0, 0, 0, 255, 0, 255, 255, 0}, nil, []uint8{0, 0, 0, 255, 255, 0, 0, 255}},
		{"ICC profile of three components", size, []byte{1, 2, 3, 4, 5, 6}, func(b *pdftest.Builder) string {
			return fmt.Sprintf("/ColorSpace [/ICCBased %d 0 R]", b.AddStream("<< /N 3 >>", nil))
		}, []uint8{1, 2, 3, 255, 4, 5, 6, 255}},
		{"ICC profile with only an alternate", size, []byte{0, 0, 0, 255, 0, 0, 0, 0}, func(b *pdftest.Builder) string {
			return fmt.Sprintf("/ColorSpace [/ICCBased %d 0 R]", b.AddStream("<< /Alternate /DeviceCMYK >>", nil))
		}, []uint8{0, 0, 0, 255, 255, 255, 255, 255}},
		{"indexed, table in a string", size + " /ColorSpace [/Indexed /DeviceRGB 1 <0A0B0C FFFEFD>]", []byte{1, 0}, nil, []uint8{255, 254, 253, 255, 10, 11, 12, 255}},
		{"indexed, table in a stream", size, []byte{1, 0}, func(b *pdftest.Builder) string {
			return fmt.Sprintf("/ColorSpace [/Indexed /DeviceGray 1 %d 0 R]", b.AddStream("<< /Filter /FlateDecode >>", pdftest.Flate([]byte{7, 99})))
		}, []uint8{99, 99, 99, 255, 7, 7, 7, 255}},
		{"a single ink", size + " /ColorSpace [/Separation /Gold /DeviceCMYK << /FunctionType 2 /Domain [0 1] /N 1 >>]", []byte{0, 255}, nil, []uint8{255, 255, 255, 255, 0, 0, 0, 255}},
		{"a process ink", size + " /ColorSpace [/Separation /Magenta /DeviceCMYK << >>]", []byte{0, 255}, nil, []uint8{255, 255, 255, 255, 255, 0, 255, 255}},
		{"two process inks", "/Width 1 /Height 1 /BitsPerComponent 8 /ColorSpace [/DeviceN [/Cyan /Black] /DeviceCMYK << >>]", []byte{255, 0}, nil, []uint8{0, 255, 255, 255}},
		{"Lab", "/Width 1 /Height 1 /BitsPerComponent 8 /ColorSpace [/Lab << /WhitePoint [0.96 1 0.82] /Range [-128 127 -128 127] >>]", []byte{0, 128, 128}, nil, []uint8{0, 0, 0, 255}},
		{"colour key", size + " /ColorSpace /DeviceGray /Mask [200 255]", []byte{199, 200}, nil, []uint8{199, 199, 199, 255, 200, 200, 200, 0}},
		{"stencil", "/Width 8 /Height 1 /ImageMask true", []byte{0b00001111}, nil,
			[]uint8{0, 0, 0, 255, 0, 0, 0, 255, 0, 0, 0, 255, 0, 0, 0, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255}},
		{"soft mask", size + " /ColorSpace /DeviceGray", []byte{10, 20}, func(b *pdftest.Builder) string {
			return fmt.Sprintf("/SMask %d 0 R", b.AddStream("<< /Subtype /Image /Width 2 /Height 1 /ColorSpace /DeviceGray /BitsPerComponent 8 >>", []byte{255, 51}))
		}, []uint8{10, 10, 10, 255, 20, 20, 20, 51}},
		{"soft mask, reversed and of another size", size + " /ColorSpace /DeviceGray", []byte{10, 20}, func(b *pdftest.Builder) string {
			return fmt.Sprintf("/SMask %d 0 R", b.AddStream("<< /Subtype /Image /Width 1 /Height 1 /ColorSpace /DeviceGray /BitsPerComponent 8 /Decode [1 0] >>", []byte{255}))
		}, []uint8{10, 10, 10, 0, 20, 20, 20, 0}},
		{"stencil mask", size + " /ColorSpace /DeviceGray", []byte{10, 20}, func(b *pdftest.Builder) string {
			return fmt.Sprintf("/Mask %d 0 R", b.AddStream("<< /Subtype /Image /Width 2 /Height 1 /ImageMask true >>", []byte{0b01000000}))
		}, []uint8{10, 10, 10, 255, 20, 20, 20, 0}},
		{"soft mask stored as JPEG", "/Width 8 /Height 8 /BitsPerComponent 8 /ColorSpace /DeviceGray", bytes.Repeat([]byte{77}, 64), func(b *pdftest.Builder) string {
			var buf bytes.Buffer
			white := image.NewGray(image.Rect(0, 0, 8, 8))
			for i := range white.Pix {
				white.Pix[i] = 255
			}
			jpeg.Encode(&buf, white, nil)
			return fmt.Sprintf("/SMask %d 0 R", b.AddStream("<< /Subtype /Image /Width 8 /Height 8 /ColorSpace /DeviceGray /BitsPerComponent 8 /Filter /DCTDecode >>", buf.Bytes()))
		}, bytes.Repeat([]uint8{77, 77, 77, 255}, 64)},
	}
	for _, tt := range tests {
		if got := oneImage(t, tt.dict, tt.data, tt.more); !bytes.Equal(got, tt.want) {
			t.Errorf("%s:\n got %v\nwant %v", tt.name, got, tt.want)
		}
	}
}

func TestImageErrorsAndWarnings(t *testing.T) {
	extract := func(dict string, data []byte, opts Options) (*Document, error) {
		b := pdftest.New()
		img := b.AddStream("<< /Subtype /Image "+dict+" >>", data)
		b.Pages(pdftest.PageSpec{Content: "q 100 0 0 100 0 0 cm /Im Do Q", Resources: fmt.Sprintf("<< /XObject << /Im %d 0 R >> >>", img)})
		doc := openBytes(t, b.Bytes(), opts)
		p, _ := doc.Page(ctx, 1)
		images, err := p.Images(ctx, everyImage)
		if err != nil || len(images) != 1 {
			t.Fatalf("%s: got %d images, err %v", dict, len(images), err)
		}
		_, err = images[0].Extract(ctx)
		return doc, err
	}
	const grey = "/ColorSpace /DeviceGray /BitsPerComponent 8"

	var unsupported *UnsupportedError
	for _, space := range []string{"/Pattern", "[/DeviceN [/Gold /Silver] /DeviceCMYK << >>]", "[/Indexed /Pattern 1 <00>]", "/NoSuchSpace"} {
		if _, err := extract("/Width 1 /Height 1 /BitsPerComponent 8 /ColorSpace "+space, []byte{0, 0}, Options{}); !errors.As(err, &unsupported) ||
			!strings.HasPrefix(unsupported.Feature, "image colour space ") {
			t.Errorf("colour space %s: got %v, want an UnsupportedError that names it", space, err)
		}
	}
	if _, err := extract("/Width 1 /Height 1 /BitsPerComponent 8", []byte{0}, Options{}); err == nil || errors.As(err, &unsupported) {
		t.Errorf("no colour space: got %v, want a plain error", err)
	}
	if _, err := extract("/Width 1 /Height 1 /BitsPerComponent 7 "+grey[:22], []byte{0}, Options{}); err == nil {
		t.Error("an image of 7 bits per component was converted")
	}

	var limit *LimitError
	if _, err := extract("/Width 100 /Height 100 "+grey, nil, Options{Limits: Limits{MaxImagePixels: 9999}}); !errors.As(err, &limit) || limit.Limit != 9999 {
		t.Errorf("over MaxImagePixels: got %v", err)
	}
	if _, err := extract("/Width 100 /Height 100 "+grey, nil, Options{Limits: Limits{MaxImagePixels: 10000}}); err != nil {
		t.Errorf("at MaxImagePixels: %v", err)
	}
	if _, err := extract("/Width 70000 /Height 1 "+grey, nil, Options{}); !errors.As(err, &limit) || limit.What != "image width or height" {
		t.Errorf("an image 70,000 pixels wide: got %v", err)
	}
	if _, err := extract("/Width 4 /Height 4 "+grey+" /Filter /FlateDecode", pdftest.Flate(make([]byte, 5000)), Options{Limits: Limits{MaxStreamBytes: 4096}}); !errors.As(err, &limit) {
		t.Errorf("data over the stream limit: got %v", err)
	}

	// Data that stops early gives the image as far as it goes, and says so.
	doc, err := extract("/Width 2 /Height 2 "+grey, []byte{1, 2, 3}, Options{})
	if err != nil {
		t.Fatalf("short data: %v", err)
	}
	if !hasWarning(doc, WarnImage, "stops early") {
		t.Errorf("short data: warnings = %v", doc.Warnings())
	}
	// A mask that cannot be read is left out, and the image still comes.
	b := pdftest.New()
	mask := b.AddStream("<< /Subtype /Image /Width 1 /Height 1 /ColorSpace /DeviceGray /BitsPerComponent 8 /Filter /JPXDecode >>", []byte("x"))
	img := b.AddStream(fmt.Sprintf("<< /Subtype /Image /Width 1 /Height 1 %s /SMask %d 0 R >>", grey, mask), []byte{5})
	b.Pages(pdftest.PageSpec{Content: "q 100 0 0 100 0 0 cm /Im Do Q", Resources: fmt.Sprintf("<< /XObject << /Im %d 0 R >> >>", img)})
	doc = openBytes(t, b.Bytes(), Options{})
	p, _ := doc.Page(ctx, 1)
	images, _ := p.Images(ctx, everyImage)
	if got := pngPixels(t, must(images[0].Extract(ctx))); !bytes.Equal(got, []uint8{5, 5, 5, 255}) || !hasWarning(doc, WarnImage, "JPXDecode") {
		t.Errorf("unreadable mask: pixels %v, warnings %v", got, doc.Warnings())
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	fresh, _ := openBytes(t, b.Bytes(), Options{}).Page(ctx, 1)
	if _, err := fresh.Images(cancelled, everyImage); !errors.Is(err, context.Canceled) {
		t.Errorf("Images with a cancelled context: %v", err)
	}
	if _, err := images[0].Extract(cancelled); !errors.Is(err, context.Canceled) {
		t.Errorf("Extract with a cancelled context: %v", err)
	}
	if _, err := new(Image).Extract(ctx); err == nil {
		t.Error("Extract on an Image that came from nowhere returned no error")
	}
}

func hasWarning(doc *Document, code, part string) bool {
	for _, w := range doc.Warnings() {
		if w.Code == code && strings.Contains(w.Message, part) {
			return true
		}
	}
	return false
}

func TestImagesThatDoNotShow(t *testing.T) {
	const img = "/Subtype /Image /Width 1 /Height 1 /ColorSpace /DeviceGray /BitsPerComponent 8"
	_, p := imagePage(t,
		"q 100 0 0 100 150 150 cm /Im Do Q "+ // half inside the crop box
			"q 50 0 0 50 500 700 cm /Im Do Q "+ // outside it
			"q 0 0 10 10 re W n 50 0 0 50 300 300 cm /Im Do Q "+ // clipped away
			"q 310 310 20 20 re W n 50 0 0 50 300 300 cm /Im Do Q "+ // cropped by a clipping path
			"q 50 0 0 50 300 400 cm /NoSize Do /NotAnImage Do Q",
		"/CropBox [100 100 400 500] /Rotate 90", func(b *pdftest.Builder) string {
			return fmt.Sprintf("/Im %d 0 R /NoSize %d 0 R /NotAnImage %d 0 R", b.AddStream("<< "+img+" >>", []byte{0}),
				b.AddStream("<< /Subtype /Image /Width 0 /Height 1 >>", nil), b.AddStream("<< /Subtype /PS >>", nil))
		})
	images, err := p.Images(ctx, everyImage)
	if err != nil {
		t.Fatal(err)
	}
	// The page is shown turned a quarter: 400 wide and 300 high, with the
	// bottom-left corner of the crop box at the top left.
	want := []Box{{50, 50, 150, 150}, {210, 210, 230, 230}}
	if len(images) != len(want) {
		t.Fatalf("got %d images, want %d: %+v", len(images), len(want), images)
	}
	for i, w := range want {
		if !closeTo(images[i].Box, w) {
			t.Errorf("image %d at %v, want %v", i+1, images[i].Box, w)
		}
	}
}

func TestSmallImages(t *testing.T) {
	_, p := imagePage(t,
		"q 200 0 0 150 50 600 cm /Im Do Q "+ // a picture
			"q 8 0 0 8 50 500 cm /Im Do Q "+ // a bullet
			"q 400 0 0 2 50 480 cm /Im Do Q "+ // a rule
			"q 300 0 0 300 50 100 cm /Dot Do Q "+ // one pixel, stretched
			// A picture cut into strips of 10 points, one above the other.
			"q 300 0 0 10 300 700 cm /Im Do Q q 300 0 0 10 300 690 cm /Im Do Q q 300 0 0 10 300 680 cm /Im Do Q "+
			// Two rules that meet at a corner are still rules.
			"q 100 0 0 2 450 400 cm /Im Do Q q 2 0 0 100 450 300 cm /Im Do Q",
		"", func(b *pdftest.Builder) string {
			const grey = "/Subtype /Image /ColorSpace /DeviceGray /BitsPerComponent 8"
			return fmt.Sprintf("/Im %d 0 R /Dot %d 0 R", b.AddStream("<< "+grey+" /Width 40 /Height 30 >>", make([]byte, 1200)),
				b.AddStream("<< "+grey+" /Width 1 /Height 1 >>", []byte{0}))
		})
	all, err := p.Images(ctx, everyImage)
	if err != nil {
		t.Fatal(err)
	}
	small := ""
	for _, img := range all {
		if img.Small {
			small += "s"
		} else {
			small += "-"
		}
	}
	if small != "-sss---ss" {
		t.Errorf("small images: %s, want -sss---ss", small)
	}
	kept, _ := p.Images(ctx, ImageOptions{})
	if len(kept) != 4 {
		t.Errorf("Images leaves %d images by default, want 4", len(kept))
	}
	if some, _ := p.Images(ctx, ImageOptions{Small: true}); len(some) != len(all) {
		t.Errorf("with Small, Images returns %d of %d", len(some), len(all))
	}
}

// logoBook builds a document where every page shows a logo in one corner
// and a figure of its own, and the odd pages a second logo. With copies set
// each page has its own copy of the logo, as some programs write them.
func logoBook(pages int, copies bool) []byte {
	b := pdftest.New()
	const img = "<< /Subtype /Image /Width 4 /Height 4 /ColorSpace /DeviceGray /BitsPerComponent 8 /Filter /FlateDecode >>"
	logo := func() int { return b.AddStream(img, pdftest.Flate(bytes.Repeat([]byte{1, 2, 3, 4}, 4))) }
	shared, odd := logo(), b.AddStream(img, pdftest.Flate(bytes.Repeat([]byte{9, 8, 7, 6, 5}, 4)[:16]))
	var specs []pdftest.PageSpec
	for i := 1; i <= pages; i++ {
		figure := b.AddStream("<< /Subtype /Image /Width 4 /Height 4 /ColorSpace /DeviceGray /BitsPerComponent 8 >>", bytes.Repeat([]byte{byte(i)}, 16))
		own := shared
		if copies {
			own = logo()
		}
		content := "q 60 0 0 30 500 740 cm /Logo Do Q q 300 0 0 200 100 300 cm /Fig Do Q "
		if i%2 == 1 {
			content += "q 60 0 0 30 40 40 cm /Odd Do Q "
		}
		if i == 2 {
			content += "q 60 0 0 30 100 100 cm /Logo Do Q " // the logo again, somewhere else
		}
		specs = append(specs, pdftest.PageSpec{Content: content,
			Resources: fmt.Sprintf("<< /XObject << /Logo %d 0 R /Odd %d 0 R /Fig %d 0 R >> >>", own, odd, figure)})
	}
	b.Pages(specs...)
	return b.Bytes()
}

func TestRepeatedImages(t *testing.T) {
	flags := func(data []byte, n int) string {
		doc := openBytes(t, data, Options{})
		p, err := doc.Page(ctx, n)
		if err != nil {
			t.Fatal(err)
		}
		images, err := p.Images(ctx, everyImage)
		if err != nil {
			t.Fatal(err)
		}
		out := ""
		for _, img := range images {
			if img.Repeated {
				out += "r"
			} else {
				out += "-"
			}
		}
		return out
	}
	for _, copies := range []bool{false, true} {
		data := logoBook(9, copies)
		// The logo of every page, the figure, and the logo of the odd pages:
		// at the ends of the document as well as in the middle.
		for n, want := range map[int]string{1: "r-r", 2: "r--", 5: "r-r", 8: "r-", 9: "r-r"} {
			if got := flags(data, n); got != want {
				t.Errorf("copies %v, page %d: repeated = %s, want %s", copies, n, got, want)
			}
		}
	}
	// A figure that happens to be on two pages running is not a logo, and
	// two pages are too few to tell.
	if got := flags(logoBook(2, false), 1); got != "---" {
		t.Errorf("two pages: repeated = %s, want ---", got)
	}
	if got := flags(logoBook(3, false), 1); got != "r--" {
		t.Errorf("three pages: repeated = %s, want r--", got)
	}

	doc := openBytes(t, logoBook(9, false), Options{})
	p, _ := doc.Page(ctx, 5)
	if kept, _ := p.Images(ctx, ImageOptions{}); len(kept) != 1 || kept[0].Width != 4 || kept[0].Box.X1-kept[0].Box.X0 != 300 {
		t.Errorf("Images by default: %+v, want the figure alone", kept)
	}
	if with, _ := p.Images(ctx, ImageOptions{Repeated: true}); len(with) != 3 {
		t.Errorf("with Repeated, Images returns %d images, want 3", len(with))
	}
}

func TestInlineImageLimit(t *testing.T) {
	// Each inline image holds a megabyte; the page may keep sixteen.
	var content strings.Builder
	data := strings.Repeat("\x00", 1<<20)
	for i := 0; i < 18; i++ {
		fmt.Fprintf(&content, "q 100 0 0 100 0 0 cm BI /W 1024 /H 1024 /BPC 8 /CS /G ID %s\nEI Q\n", data)
	}
	_, p := imagePage(t, content.String(), "", nil)
	images, err := p.Images(ctx, everyImage)
	if err != nil || len(images) != 18 {
		t.Fatalf("got %d images, err %v", len(images), err)
	}
	if _, err := images[15].Extract(ctx); err != nil {
		t.Errorf("the sixteenth inline image: %v", err)
	}
	var limit *LimitError
	if _, err := images[16].Extract(ctx); !errors.As(err, &limit) {
		t.Errorf("the seventeenth inline image: got %v, want a LimitError", err)
	}
}

func TestImagesOfEncryptedDocument(t *testing.T) {
	b := pdftest.New()
	jpg := b.AddStream("<< /Subtype /Image /Width 8 /Height 8 /ColorSpace /DeviceGray /BitsPerComponent 8 /Filter /DCTDecode >>", []byte("a JPEG file"))
	raw := b.AddStream("<< /Subtype /Image /Width 2 /Height 2 /ColorSpace /DeviceGray /BitsPerComponent 8 >>", []byte{1, 2, 3, 4})
	b.Pages(pdftest.PageSpec{Content: "q 100 0 0 100 0 0 cm /A Do Q q 100 0 0 100 200 0 cm /B Do BI /W 1 /H 1 /BPC 8 /CS /G ID \x09\nEI Q",
		Resources: fmt.Sprintf("<< /XObject << /A %d 0 R /B %d 0 R >> >>", jpg, raw)})
	b.Encrypt = &pdftest.Encryption{R: 4}
	doc := openBytes(t, b.Bytes(), Options{})
	if !doc.Info().Encrypted {
		t.Fatal("the document is not encrypted")
	}
	p, _ := doc.Page(ctx, 1)
	images, err := p.Images(ctx, everyImage)
	if err != nil || len(images) != 3 {
		t.Fatalf("got %d images, err %v", len(images), err)
	}
	if data, err := images[0].Extract(ctx); err != nil || string(data) != "a JPEG file" {
		t.Errorf("JPEG: got %q, err %v", data, err)
	}
	if got := pngPixels(t, must(images[1].Extract(ctx))); !bytes.Equal(got, []uint8{1, 1, 1, 255, 2, 2, 2, 255, 3, 3, 3, 255, 4, 4, 4, 255}) {
		t.Errorf("samples: got %v", got)
	}
	if got := pngPixels(t, must(images[2].Extract(ctx))); !bytes.Equal(got, []uint8{9, 9, 9, 255}) {
		t.Errorf("inline image: got %v", got)
	}
}
