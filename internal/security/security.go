// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

// Package security implements the PDF standard security handler for
// reading: password checks and decryption with RC4, AES-128 and AES-256
// (revisions 2 to 6).
package security

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rc4"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"fmt"
	"unicode/utf8"
)

// Errors returned by New.
var (
	// ErrPassword means neither the empty password nor the supplied one
	// opens the document.
	ErrPassword = errors.New("security: password does not open the document")
	// ErrUnsupported means the document uses a security handler or an
	// algorithm this package does not implement. The wrapped message names it.
	ErrUnsupported = errors.New("security: unsupported encryption")
	// ErrInvalid means the encryption dictionary is malformed.
	ErrInvalid = errors.New("security: invalid encryption dictionary")
)

// CryptFilter is one entry of the /CF dictionary.
type CryptFilter struct {
	CFM    string // None, V2, AESV2 or AESV3
	Length int    // key length, in bytes or bits depending on the producer
}

// Params are the entries of the encryption dictionary, plus the first
// element of the file identifier.
type Params struct {
	Filter          string
	V, R            int
	Length          int // key length in bits
	O, U, OE, UE    []byte
	P               int32
	EncryptMetadata bool
	CF              map[string]CryptFilter
	StmF, StrF      string
	ID0             []byte
}

type method uint8

const (
	none method = iota
	rc4Method
	aesV2
	aesV3
)

// Handler decrypts the strings and streams of one document.
type Handler struct {
	key             []byte
	stm, str        method
	cf              map[string]method
	encryptMetadata bool
	owner           bool
}

// EncryptMetadata reports whether the document's metadata stream is
// encrypted along with everything else.
func (h *Handler) EncryptMetadata() bool { return h.encryptMetadata }

// OwnerAccess reports whether the document was opened with its owner
// password rather than its user password.
func (h *Handler) OwnerAccess() bool { return h.owner }

var padding = []byte{
	0x28, 0xBF, 0x4E, 0x5E, 0x4E, 0x75, 0x8A, 0x41, 0x64, 0x00, 0x4E, 0x56, 0xFF, 0xFA, 0x01, 0x08,
	0x2E, 0x2E, 0x00, 0xB6, 0xD0, 0x68, 0x3E, 0x80, 0x2F, 0x0C, 0xA9, 0xFE, 0x64, 0x53, 0x69, 0x7A,
}

// New checks the passwords and returns a handler. The empty user password
// is tried first, because most encrypted PDFs only restrict printing or
// copying and open without asking. If that fails, password is tried as the
// user password and then as the owner password.
func New(p Params, password string) (*Handler, error) {
	if p.Filter != "Standard" {
		return nil, fmt.Errorf("%w: security handler %q", ErrUnsupported, p.Filter)
	}
	h := &Handler{encryptMetadata: p.EncryptMetadata}

	switch p.V {
	case 1, 2:
		h.stm, h.str = rc4Method, rc4Method
	case 4, 5:
		h.cf = map[string]method{"Identity": none}
		for name, cf := range p.CF {
			switch cf.CFM {
			case "V2":
				h.cf[name] = rc4Method
			case "AESV2":
				h.cf[name] = aesV2
			case "AESV3":
				h.cf[name] = aesV3
			case "None", "":
				h.cf[name] = none
			default:
				return nil, fmt.Errorf("%w: crypt filter method %q", ErrUnsupported, cf.CFM)
			}
		}
		h.cf["Identity"] = none
		h.stm = h.cf[orIdentity(p.StmF)]
		h.str = h.cf[orIdentity(p.StrF)]
	default:
		return nil, fmt.Errorf("%w: algorithm version %d", ErrUnsupported, p.V)
	}

	switch {
	case p.R >= 2 && p.R <= 4:
		if len(p.O) < 32 || len(p.U) < 32 {
			return nil, fmt.Errorf("%w: /O or /U too short", ErrInvalid)
		}
		n := 5
		if p.R >= 3 {
			n = p.Length / 8
			if p.Length == 0 {
				n = 5
				if p.V == 4 {
					n = 16
				}
			}
			if n < 5 || n > 16 {
				return nil, fmt.Errorf("%w: key length %d bits", ErrInvalid, p.Length)
			}
		}
		for _, pw := range legacyPasswords(password) {
			if key, ok := userKey(p, pw, n); ok {
				h.key = key
				return h, nil
			}
		}
		for _, pw := range legacyPasswords(password) {
			if key, ok := userKey(p, ownerToUser(p, pw, n), n); ok {
				h.key, h.owner = key, true
				return h, nil
			}
		}
		return nil, ErrPassword
	case p.R == 5 || p.R == 6:
		if len(p.O) < 48 || len(p.U) < 48 || len(p.OE) < 32 || len(p.UE) < 32 {
			return nil, fmt.Errorf("%w: /O, /U, /OE or /UE too short", ErrInvalid)
		}
		for _, pw := range []string{"", password} {
			if key, owner, ok := key256(p, truncate127(pw)); ok {
				h.key, h.owner = key, owner
				return h, nil
			}
		}
		return nil, ErrPassword
	}
	return nil, fmt.Errorf("%w: security handler revision %d", ErrUnsupported, p.R)
}

func orIdentity(name string) string {
	if name == "" {
		return "Identity"
	}
	return name
}

// legacyPasswords returns the byte forms of a password to try for revisions
// 2 to 4, always starting with the empty password. Those revisions expect
// PDFDocEncoding; producers in practice also use raw UTF-8.
func legacyPasswords(password string) [][]byte {
	out := [][]byte{pad(nil)}
	if password == "" {
		return out
	}
	latin := make([]byte, 0, len(password))
	ok := true
	for _, r := range password {
		if r > 0xFF {
			ok = false
			break
		}
		latin = append(latin, byte(r))
	}
	if ok {
		out = append(out, pad(latin))
	}
	if !ok || !bytes.Equal(latin, []byte(password)) {
		out = append(out, pad([]byte(password)))
	}
	return out
}

// pad extends or cuts a password to exactly 32 bytes (Algorithm 2, step a).
func pad(pw []byte) []byte {
	out := make([]byte, 32)
	n := copy(out, pw)
	copy(out[n:], padding)
	return out
}

func truncate127(pw string) []byte {
	b := []byte(pw)
	if len(b) > 127 {
		b = b[:127]
		// Do not cut a UTF-8 sequence in half.
		for len(b) > 0 && !utf8.Valid(b) {
			b = b[:len(b)-1]
		}
	}
	return b
}

// userKey derives the file key from a padded user password (Algorithm 2)
// and reports whether it matches /U (Algorithms 4 to 6).
func userKey(p Params, padded []byte, n int) ([]byte, bool) {
	h := md5.New()
	h.Write(padded)
	h.Write(p.O[:32])
	var pbytes [4]byte
	binary.LittleEndian.PutUint32(pbytes[:], uint32(p.P))
	h.Write(pbytes[:])
	h.Write(p.ID0)
	if p.R >= 4 && !p.EncryptMetadata {
		h.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF})
	}
	sum := h.Sum(nil)
	if p.R >= 3 {
		for i := 0; i < 50; i++ {
			s := md5.Sum(sum[:n])
			sum = s[:]
		}
	}
	key := sum[:n]

	if p.R == 2 {
		return key, bytes.Equal(rc4Crypt(key, padding), p.U[:32])
	}
	h = md5.New()
	h.Write(padding)
	h.Write(p.ID0)
	x := rc4Crypt(key, h.Sum(nil))
	for i := 1; i <= 19; i++ {
		x = rc4Crypt(xorKey(key, byte(i)), x)
	}
	return key, bytes.Equal(x, p.U[:16])
}

// ownerToUser recovers the padded user password from /O, given a padded
// owner password (Algorithm 7).
func ownerToUser(p Params, padded []byte, n int) []byte {
	sum := md5.Sum(padded)
	if p.R >= 3 {
		for i := 0; i < 50; i++ {
			sum = md5.Sum(sum[:])
		}
	}
	key := sum[:n]
	if p.R == 2 {
		return rc4Crypt(key, p.O[:32])
	}
	x := append([]byte(nil), p.O[:32]...)
	for i := 19; i >= 0; i-- {
		x = rc4Crypt(xorKey(key, byte(i)), x)
	}
	return x
}

// key256 checks a password against /U and then /O for revisions 5 and 6 and
// unwraps the file key (Algorithm 2.A).
func key256(p Params, pw []byte) (key []byte, owner, ok bool) {
	hash := func(salt, udata []byte) []byte {
		if p.R == 5 {
			h := sha256.New()
			h.Write(pw)
			h.Write(salt)
			h.Write(udata)
			return h.Sum(nil)
		}
		return hash2B(pw, salt, udata)
	}
	u, o := p.U[:48], p.O[:48]
	if bytes.Equal(hash(u[32:40], nil), u[:32]) {
		return unwrap(hash(u[40:48], nil), p.UE[:32]), false, true
	}
	if bytes.Equal(hash(o[32:40], u), o[:32]) {
		return unwrap(hash(o[40:48], u), p.OE[:32]), true, true
	}
	return nil, false, false
}

// unwrap decrypts a wrapped file key: AES-256 in CBC mode, zero IV, no
// padding.
func unwrap(kek, wrapped []byte) []byte {
	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil
	}
	out := make([]byte, 32)
	cipher.NewCBCDecrypter(block, make([]byte, aes.BlockSize)).CryptBlocks(out, wrapped)
	return out
}

// hash2B is the revision 6 password hash (Algorithm 2.B).
func hash2B(pw, salt, udata []byte) []byte {
	h := sha256.New()
	h.Write(pw)
	h.Write(salt)
	h.Write(udata)
	k := h.Sum(nil)

	for round := 1; ; round++ {
		one := make([]byte, 0, len(pw)+len(k)+len(udata))
		one = append(one, pw...)
		one = append(one, k...)
		one = append(one, udata...)
		k1 := bytes.Repeat(one, 64)

		block, err := aes.NewCipher(k[:16])
		if err != nil {
			return nil
		}
		e := make([]byte, len(k1))
		cipher.NewCBCEncrypter(block, k[16:32]).CryptBlocks(e, k1)

		// The first 16 bytes, read as a big-endian number, modulo 3. Since
		// 256 is 1 modulo 3, that is the byte sum modulo 3.
		sum := 0
		for _, b := range e[:16] {
			sum += int(b)
		}
		switch sum % 3 {
		case 0:
			s := sha256.Sum256(e)
			k = s[:]
		case 1:
			s := sha512.Sum384(e)
			k = s[:]
		default:
			s := sha512.Sum512(e)
			k = s[:]
		}
		if round >= 64 && int(e[len(e)-1]) <= round-32 {
			return k[:32]
		}
	}
}

func rc4Crypt(key, data []byte) []byte {
	c, err := rc4.NewCipher(key)
	if err != nil {
		return append([]byte(nil), data...)
	}
	out := make([]byte, len(data))
	c.XORKeyStream(out, data)
	return out
}

func xorKey(key []byte, b byte) []byte {
	out := make([]byte, len(key))
	for i, k := range key {
		out[i] = k ^ b
	}
	return out
}

// objectKey derives the key for one object (Algorithm 1). AES-256 uses the
// file key as it is.
func (h *Handler) objectKey(m method, num, gen int) []byte {
	if m == aesV3 {
		return h.key
	}
	d := md5.New()
	d.Write(h.key)
	d.Write([]byte{byte(num), byte(num >> 8), byte(num >> 16), byte(gen), byte(gen >> 8)})
	if m == aesV2 {
		d.Write([]byte("sAlT"))
	}
	sum := d.Sum(nil)
	return sum[:min(len(h.key)+5, 16)]
}

func (h *Handler) decrypt(m method, num, gen int, data []byte) []byte {
	switch m {
	case rc4Method:
		return rc4Crypt(h.objectKey(m, num, gen), data)
	case aesV2, aesV3:
		return aesCBC(h.objectKey(m, num, gen), data)
	}
	return data
}

// aesCBC decrypts data laid out as a 16-byte IV followed by ciphertext with
// PKCS#7 padding. Damaged input yields as much plaintext as can be
// recovered, never a panic.
func aesCBC(key, data []byte) []byte {
	if len(data) < 2*aes.BlockSize {
		return nil
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil
	}
	iv, body := data[:aes.BlockSize], data[aes.BlockSize:]
	body = body[:len(body)-len(body)%aes.BlockSize]
	out := make([]byte, len(body))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, body)
	if n := int(out[len(out)-1]); n >= 1 && n <= aes.BlockSize && n <= len(out) {
		out = out[:len(out)-n]
	}
	return out
}

// DecryptString decrypts a string that belongs to object num gen.
func (h *Handler) DecryptString(num, gen int, s []byte) []byte {
	return h.decrypt(h.str, num, gen, s)
}

// DecryptStream decrypts the data of stream object num gen. cryptFilter is
// the name from the stream's own /Crypt filter, or "" when it has none and
// the document default applies.
func (h *Handler) DecryptStream(num, gen int, data []byte, cryptFilter string) []byte {
	m := h.stm
	if cryptFilter != "" && h.cf != nil {
		if named, ok := h.cf[cryptFilter]; ok {
			m = named
		}
	}
	return h.decrypt(m, num, gen, data)
}
