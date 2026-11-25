// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package reader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/gomcpgo/unpdf/internal/filter"
	"github.com/gomcpgo/unpdf/internal/pdfobj"
	"github.com/gomcpgo/unpdf/internal/security"
)

// objStream is a decoded object stream: the objects' numbers, their offsets
// within data, and the data itself.
type objStream struct {
	nums []int
	offs []int
	data []byte
}

// RawStream returns the bytes of a stream as stored in the file, decrypted
// but with its filters still applied.
func (f *File) RawStream(ctx context.Context, st *pdfobj.Stream) ([]byte, error) {
	raw, err := f.streamBytes(ctx, st)
	if err != nil {
		return nil, err
	}
	return f.decryptStream(ctx, st, raw), nil
}

// DecodeStream returns the fully decoded data of a stream. Image filters
// (DCT, JPX, JBIG2, CCITT) are not decoded: a stream that uses one yields an
// *UnsupportedError naming the filter.
func (f *File) DecodeStream(ctx context.Context, st *pdfobj.Stream) ([]byte, error) {
	return f.decode(ctx, st, true)
}

// IsImageFilter reports whether name is a filter whose data is an image in
// a format of its own: JPEG, JPEG 2000, JBIG2 or CCITT fax.
func IsImageFilter(name string) bool {
	switch name {
	case "DCTDecode", "JPXDecode", "JBIG2Decode", "CCITTFaxDecode":
		return true
	}
	return false
}

// ImageFilter returns the image filter among the filters of a stream
// dictionary, with its decode parameters, or "" when there is none. It
// reads the dictionary only.
func (f *File) ImageFilter(ctx context.Context, d pdfobj.Dict) (string, pdfobj.Dict) {
	names, _, dicts := f.filters(ctx, d)
	for i, name := range names {
		if IsImageFilter(name) {
			return name, dicts[i]
		}
	}
	return "", nil
}

// DecodeImage returns the data of an image stream with its filters undone
// as far as the image filter, if it has one: what comes back for a stream
// with DCTDecode is a JPEG file, whatever other filters wrap it. A stream
// with no image filter is decoded in full, as by DecodeStream.
func (f *File) DecodeImage(ctx context.Context, st *pdfobj.Stream) ([]byte, error) {
	return f.decodeTo(ctx, st, true, true)
}

// DecodeInline does for the data of an inline image, given with its
// dictionary, what DecodeImage does for an image stream. The result may be
// data itself.
func (f *File) DecodeInline(ctx context.Context, d pdfobj.Dict, data []byte) ([]byte, error) {
	return f.unfilter(ctx, d, 0, data, true)
}

func (f *File) decode(ctx context.Context, st *pdfobj.Stream, decrypt bool) ([]byte, error) {
	return f.decodeTo(ctx, st, decrypt, false)
}

func (f *File) decodeTo(ctx context.Context, st *pdfobj.Stream, decrypt, image bool) ([]byte, error) {
	if st.Dict["F"] != nil {
		return nil, &UnsupportedError{"stream data in an external file"}
	}
	data, err := f.streamBytes(ctx, st)
	if err != nil {
		return nil, err
	}
	if decrypt {
		data = f.decryptStream(ctx, st, data)
	}
	return f.unfilter(ctx, st.Dict, st.Num, data, image)
}

// unfilter undoes the filters that the dictionary d names. With image set
// it stops at an image filter and returns the data in that filter's format.
func (f *File) unfilter(ctx context.Context, d pdfobj.Dict, num int, data []byte, image bool) ([]byte, error) {
	names, parms, _ := f.filters(ctx, d)
	for i, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if name == "Crypt" {
			continue // handled by decryptStream
		}
		if image && IsImageFilter(name) {
			if i != len(names)-1 {
				return nil, &UnsupportedError{"filter " + name + " under another filter"}
			}
			return data, nil
		}
		if !filter.Known(name) {
			return nil, &UnsupportedError{"filter " + name}
		}
		out, partial, err := filter.Decode(name, data, parms[i], int(min(f.lim.MaxStreamBytes, 1<<31-1)))
		if errors.Is(err, filter.ErrTooLarge) {
			return nil, &LimitError{"decoded stream size", f.lim.MaxStreamBytes}
		}
		if err != nil {
			return nil, fmt.Errorf("object %d: %s: %w", num, name, err)
		}
		if partial {
			f.warn(WarnStreamData, "object %d: %s data is damaged; decoded what could be read", num, name)
		}
		data = out
	}
	return data, nil
}

// filters returns the filter names of a stream dictionary with their decode
// parameters, in the order they must be applied. The parameters come twice:
// as the filters of this package take them, and as the dictionaries they
// were read from, nil where a filter has none.
func (f *File) filters(ctx context.Context, d pdfobj.Dict) ([]string, []filter.Params, []pdfobj.Dict) {
	var names []string
	switch v := f.resolveQuiet(ctx, d["Filter"]).(type) {
	case pdfobj.Name:
		names = []string{string(v)}
	case pdfobj.Array:
		for _, x := range v {
			names = append(names, string(f.Name(ctx, x)))
		}
	}
	parms := make([]filter.Params, len(names))
	var dicts []pdfobj.Dict
	dp := d["DecodeParms"]
	if dp == nil {
		dp = d["DP"]
	}
	switch v := f.resolveQuiet(ctx, dp).(type) {
	case pdfobj.Dict:
		dicts = []pdfobj.Dict{v}
	case pdfobj.Array:
		for _, x := range v {
			dicts = append(dicts, f.Dict(ctx, x))
		}
	}
	for len(dicts) < len(names) {
		dicts = append(dicts, nil)
	}
	dicts = dicts[:len(names)]
	for i := range parms {
		if dicts[i] == nil {
			continue
		}
		get := func(key pdfobj.Name) int {
			v, _ := f.Int(ctx, dicts[i][key])
			return int(max(min(v, 1<<30), -1))
		}
		parms[i] = filter.Params{
			Predictor:        get("Predictor"),
			Colors:           get("Colors"),
			BitsPerComponent: get("BitsPerComponent"),
			Columns:          get("Columns"),
		}
		if dicts[i]["EarlyChange"] != nil {
			ec := get("EarlyChange")
			parms[i].EarlyChange = &ec
		}
	}
	return names, parms, dicts
}

// decryptStream decrypts stream data when the document is encrypted and the
// stream is one that encryption covers.
func (f *File) decryptStream(ctx context.Context, st *pdfobj.Stream, data []byte) []byte {
	if f.sec == nil || st.Num == 0 || isType(st.Dict, "XRef") {
		return data
	}
	if isType(st.Dict, "Metadata") && (!f.sec.EncryptMetadata() || looksLikeXMP(data)) {
		// Some producers leave the metadata stream in the clear even when
		// the encryption dictionary does not say so.
		return data
	}
	// A stream can name its own crypt filter through a /Crypt filter entry.
	cryptName := ""
	names, _, _ := f.filters(ctx, st.Dict)
	for i, name := range names {
		if name != "Crypt" {
			continue
		}
		cryptName = "Identity"
		dp := f.resolveQuiet(ctx, st.Dict["DecodeParms"])
		if arr, ok := dp.(pdfobj.Array); ok && i < len(arr) {
			dp = f.resolveQuiet(ctx, arr[i])
		}
		if d, ok := dp.(pdfobj.Dict); ok {
			if n := f.Name(ctx, d["Name"]); n != "" {
				cryptName = string(n)
			}
		}
	}
	return f.sec.DecryptStream(st.Num, st.Gen, data, cryptName)
}

// streamBytes reads the stored bytes of a stream. It trusts /Length only
// when "endstream" really follows; otherwise it searches for the keyword,
// because a wrong /Length is one of the commonest defects in real files.
func (f *File) streamBytes(ctx context.Context, st *pdfobj.Stream) ([]byte, error) {
	if st.Offset < 0 || st.Offset > f.size {
		return nil, fmt.Errorf("object %d: stream data is outside the file", st.Num)
	}
	length := int64(-1)
	if v, err := f.Resolve(ctx, st.Dict["Length"]); err == nil {
		if n, ok := pdfobj.Int(v); ok && n >= 0 && st.Offset+n <= f.size && f.endstreamAt(st.Offset+n) {
			length = n
		}
	}
	if length < 0 {
		end, ok := f.findEndstream(ctx, st.Offset)
		if !ok {
			return nil, fmt.Errorf("object %d: stream has no usable /Length and no endstream", st.Num)
		}
		length = end - st.Offset
		f.warn(WarnStreamLength, "object %d: stream /Length is missing or wrong; used the endstream keyword", st.Num)
	}
	if length > f.lim.MaxStreamBytes {
		return nil, &LimitError{"stream size", f.lim.MaxStreamBytes}
	}
	data := make([]byte, length)
	n, err := f.src.ReadAt(data, st.Offset)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("object %d: reading stream: %w", st.Num, err)
	}
	return data[:n], nil
}

// endstreamAt reports whether the "endstream" keyword follows off, allowing
// for the end-of-line that precedes it.
func (f *File) endstreamAt(off int64) bool {
	var buf [16]byte
	n, _ := f.src.ReadAt(buf[:min(int64(len(buf)), f.size-off)], off)
	b := buf[:n]
	for len(b) > 0 && pdfobj.IsSpace(b[0]) {
		b = b[1:]
	}
	return bytes.HasPrefix(b, []byte("endstream")) || bytes.HasPrefix(b, []byte("endobj"))
}

// findEndstream searches forward from off for "endstream" and returns the
// offset where the stream data ends, with the line end before the keyword
// removed.
func (f *File) findEndstream(ctx context.Context, off int64) (int64, bool) {
	const chunk = 64 << 10
	key := []byte("endstream")
	buf := make([]byte, chunk+len(key))
	limit := min(f.size, off+f.lim.MaxStreamBytes+int64(len(key))+2)
	for pos := off; pos < limit; pos += chunk {
		if ctx.Err() != nil {
			return 0, false
		}
		n, _ := f.src.ReadAt(buf[:min(int64(len(buf)), f.size-pos)], pos)
		if n == 0 {
			break
		}
		if i := bytes.Index(buf[:n], key); i >= 0 {
			end := pos + int64(i)
			// Drop one end-of-line before the keyword.
			var tail [2]byte
			if end-off >= 2 {
				f.src.ReadAt(tail[:], end-2)
				switch {
				case tail[0] == '\r' && tail[1] == '\n':
					end -= 2
				case tail[1] == '\n' || tail[1] == '\r':
					end--
				}
			} else if end-off == 1 {
				f.src.ReadAt(tail[:1], end-1)
				if tail[0] == '\n' || tail[0] == '\r' {
					end--
				}
			}
			return end, true
		}
	}
	return 0, false
}

// objStream returns the decoded object stream numbered num.
func (f *File) objStream(ctx context.Context, num int) (*objStream, error) {
	if os, ok := f.stmCache[num]; ok {
		return os, nil
	}
	// The container must be an ordinary object: an object stream inside an
	// object stream is not allowed, and following one could loop.
	if f.entry(num).kind != inUse {
		return nil, errors.New("object stream is not a top-level object")
	}
	// Decoding may resolve /Length or /Filter, and a hostile file can point
	// those back into this very stream.
	if f.stmLoading[num] {
		return nil, errors.New("object stream refers to itself")
	}
	f.stmLoading[num] = true
	defer delete(f.stmLoading, num)

	obj, err := f.Get(ctx, num)
	if err != nil {
		return nil, err
	}
	st, ok := obj.(*pdfobj.Stream)
	if !ok {
		return nil, errors.New("not a stream")
	}
	n, _ := pdfobj.Int(st.Dict["N"])
	first, _ := pdfobj.Int(st.Dict["First"])
	if n < 0 || n > int64(f.lim.MaxObjects) || first < 0 {
		return nil, errors.New("bad /N or /First")
	}
	data, err := f.DecodeStream(ctx, st)
	if err != nil {
		return nil, err
	}
	if first > int64(len(data)) {
		return nil, errors.New("/First is beyond the end of the data")
	}
	os := &objStream{data: data[first:]}
	lex := pdfobj.NewBytesLexer(data[:first], f.plim)
	for i := int64(0); i < n; i++ {
		a, err1 := lex.Next()
		b, err2 := lex.Next()
		if err1 != nil || err2 != nil || a.Kind != pdfobj.Integer || b.Kind != pdfobj.Integer {
			break // a short index: keep what was read
		}
		if a.Int < 0 || a.Int > 1<<31-1 || b.Int < 0 || b.Int > int64(len(os.data)) {
			continue
		}
		os.nums = append(os.nums, int(a.Int))
		os.offs = append(os.offs, int(b.Int))
	}

	if len(data) > maxCachedStmBytes/4 {
		return os, nil // too big to be worth caching
	}
	if f.stmBytes+len(data) > maxCachedStmBytes {
		clear(f.stmCache)
		f.stmBytes = 0
	}
	f.stmCache[num] = os
	f.stmBytes += len(data)
	return os, nil
}

func (f *File) loadCompressed(ctx context.Context, num int, e entry) (pdfobj.Object, error) {
	os, err := f.objStream(ctx, int(e.off))
	if err != nil {
		return nil, fmt.Errorf("object %d: object stream %d: %w", num, e.off, err)
	}
	idx := int(e.gen)
	if idx >= len(os.nums) || os.nums[idx] != num {
		// The index is wrong; look the object up by number.
		idx = -1
		for i, n := range os.nums {
			if n == num {
				idx = i
				break
			}
		}
		if idx < 0 {
			return nil, nil
		}
	}
	p := pdfobj.NewParser(pdfobj.NewBytesLexer(os.data[os.offs[idx]:], f.plim))
	obj, err := p.ParseObject()
	if err != nil {
		return nil, fmt.Errorf("object %d: %w", num, err)
	}
	return obj, nil
}

// setupSecurity reads the encryption dictionary, if there is one, and checks
// the password.
func (f *File) setupSecurity(ctx context.Context, password string) error {
	f.sec, f.encryptObj = nil, 0
	encRef := f.Trailer["Encrypt"]
	if encRef == nil {
		return nil
	}
	if ref, ok := encRef.(pdfobj.Ref); ok {
		f.encryptObj = ref.Num
	}
	obj, err := f.Resolve(ctx, encRef)
	if err != nil {
		return fmt.Errorf("encryption dictionary: %w", err)
	}
	d, ok := obj.(pdfobj.Dict)
	if !ok {
		return errors.New("encryption dictionary is missing")
	}

	str := func(key pdfobj.Name) []byte {
		s, _ := f.String(ctx, d[key])
		return s
	}
	num := func(key pdfobj.Name) int {
		v, _ := f.Int(ctx, d[key])
		return int(max(min(v, 1<<31-1), -1<<31))
	}
	p := security.Params{
		Filter: string(f.Name(ctx, d["Filter"])),
		V:      num("V"), R: num("R"), Length: num("Length"),
		O: str("O"), U: str("U"), OE: str("OE"), UE: str("UE"),
		P:               int32(uint32(num("P"))),
		EncryptMetadata: true,
		StmF:            string(f.Name(ctx, d["StmF"])),
		StrF:            string(f.Name(ctx, d["StrF"])),
	}
	if v, ok := f.Bool(ctx, d["EncryptMetadata"]); ok {
		p.EncryptMetadata = v
	}
	if cf := f.Dict(ctx, d["CF"]); cf != nil {
		p.CF = map[string]security.CryptFilter{}
		for name, v := range cf {
			fd := f.Dict(ctx, v)
			length, _ := f.Int(ctx, fd["Length"])
			p.CF[string(name)] = security.CryptFilter{CFM: string(f.Name(ctx, fd["CFM"])), Length: int(length)}
		}
	}
	if ids := f.Array(ctx, f.Trailer["ID"]); len(ids) > 0 {
		id0, _ := f.String(ctx, ids[0])
		p.ID0 = id0
	}

	h, err := security.New(p, password)
	switch {
	case errors.Is(err, security.ErrPassword):
		if password == "" {
			return ErrPasswordRequired
		}
		return ErrWrongPassword
	case errors.Is(err, security.ErrUnsupported):
		return &UnsupportedError{"encryption with " + strings.TrimPrefix(err.Error(), security.ErrUnsupported.Error()+": ")}
	case err != nil:
		return err
	}
	f.sec = h
	f.dropCache() // anything cached so far was read without decryption
	return nil
}

// looksLikeXMP reports whether data starts like an unencrypted XMP packet.
func looksLikeXMP(data []byte) bool {
	data = bytes.TrimLeft(data, "\xef\xbb\xbf \t\r\n")
	return bytes.HasPrefix(data, []byte("<?xpacket")) || bytes.HasPrefix(data, []byte("<x:xmpmeta")) ||
		bytes.HasPrefix(data, []byte("<?xml"))
}
