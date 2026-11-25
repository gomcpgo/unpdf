// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package pdftest

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rc4"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"

	"github.com/gomcpgo/unpdf/internal/pdfobj"
)

// Encryption describes how a test file is encrypted.
type Encryption struct {
	// R is the security handler revision: 2 (RC4, 40 bits), 3 (RC4, 128
	// bits), 4 (AES-128), 5 or 6 (AES-256).
	R int
	// User and Owner are the passwords. An empty user password is the
	// common "opens without asking" case.
	User, Owner string
	// ClearMetadata leaves metadata streams unencrypted (revision 4 up).
	ClearMetadata bool
}

// encryptor is a second, independent implementation of the standard
// security handler, in the writing direction. Keeping it separate from
// package security means a misreading of the specification there does not
// cancel itself out in a round-trip test.
type encryptor struct {
	e    Encryption
	key  []byte
	dict pdfobj.Dict
	iv   uint64
}

var padBytes = []byte("\x28\xBF\x4E\x5E\x4E\x75\x8A\x41\x64\x00\x4E\x56\xFF\xFA\x01\x08" +
	"\x2E\x2E\x00\xB6\xD0\x68\x3E\x80\x2F\x0C\xA9\xFE\x64\x53\x69\x7A")

func padPassword(pw string) []byte {
	out := make([]byte, 32)
	n := copy(out, pw)
	copy(out[n:], padBytes)
	return out
}

func rc4With(key, data []byte) []byte {
	c, _ := rc4.NewCipher(key)
	out := make([]byte, len(data))
	c.XORKeyStream(out, data)
	return out
}

func xored(key []byte, v byte) []byte {
	out := make([]byte, len(key))
	for i := range key {
		out[i] = key[i] ^ v
	}
	return out
}

func newEncryptor(e Encryption, id []byte) *encryptor {
	enc := &encryptor{e: e}
	perms := int32(-44)
	d := pdfobj.Dict{"Filter": pdfobj.Name("Standard"), "R": int64(e.R), "P": int64(perms)}
	if e.Owner == "" {
		e.Owner = e.User
	}

	if e.R >= 5 {
		enc.key = sum256([]byte("file key"), nil, nil)
		salt := func(s string) []byte { return sum256([]byte(s), nil, nil)[:8] }
		hash := func(pw, salt, udata []byte) []byte {
			if e.R == 5 {
				return sum256(pw, salt, udata)
			}
			return hardenedHash(pw, salt, udata)
		}
		wrap := func(kek []byte) []byte {
			block, _ := aes.NewCipher(kek)
			out := make([]byte, 32)
			cipher.NewCBCEncrypter(block, make([]byte, 16)).CryptBlocks(out, enc.key)
			return out
		}
		uv, uk := salt("user validation"), salt("user key")
		u := append(append(hash([]byte(e.User), uv, nil), uv...), uk...)
		ov, ok := salt("owner validation"), salt("owner key")
		o := append(append(hash([]byte(e.Owner), ov, u), ov...), ok...)
		d["V"] = int64(5)
		d["Length"] = int64(256)
		d["U"], d["UE"] = pdfobj.String(u), pdfobj.String(wrap(hash([]byte(e.User), uk, nil)))
		d["O"], d["OE"] = pdfobj.String(o), pdfobj.String(wrap(hash([]byte(e.Owner), ok, u)))
		d["CF"] = pdfobj.Dict{"StdCF": pdfobj.Dict{"CFM": pdfobj.Name("AESV3"), "Length": int64(32)}}
		d["StmF"], d["StrF"] = pdfobj.Name("StdCF"), pdfobj.Name("StdCF")

		block := make([]byte, 16)
		binary.LittleEndian.PutUint32(block, uint32(perms))
		copy(block[4:], "\xFF\xFF\xFF\xFFTadb")
		if e.ClearMetadata {
			block[8] = 'F'
		}
		ecb, _ := aes.NewCipher(enc.key)
		ecb.Encrypt(block, block)
		d["Perms"] = pdfobj.String(block)
	} else {
		n := 5
		if e.R >= 3 {
			n = 16
		}
		// Owner entry: the padded user password, encrypted with a key made
		// from the owner password.
		sum := md5.Sum(padPassword(e.Owner))
		if e.R >= 3 {
			for i := 0; i < 50; i++ {
				sum = md5.Sum(sum[:])
			}
		}
		ownerKey := sum[:n]
		o := rc4With(ownerKey, padPassword(e.User))
		if e.R >= 3 {
			for i := 1; i <= 19; i++ {
				o = rc4With(xored(ownerKey, byte(i)), o)
			}
		}
		// File key.
		h := md5.New()
		h.Write(padPassword(e.User))
		h.Write(o)
		var p [4]byte
		binary.LittleEndian.PutUint32(p[:], uint32(perms))
		h.Write(p[:])
		h.Write(id)
		if e.R >= 4 && e.ClearMetadata {
			h.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF})
		}
		key := h.Sum(nil)
		if e.R >= 3 {
			for i := 0; i < 50; i++ {
				s := md5.Sum(key[:n])
				key = s[:]
			}
		}
		enc.key = key[:n]
		// User entry.
		var u []byte
		if e.R == 2 {
			u = rc4With(enc.key, padBytes)
		} else {
			h := md5.New()
			h.Write(padBytes)
			h.Write(id)
			u = rc4With(enc.key, h.Sum(nil))
			for i := 1; i <= 19; i++ {
				u = rc4With(xored(enc.key, byte(i)), u)
			}
			u = append(u, make([]byte, 16)...)
		}
		d["O"], d["U"] = pdfobj.String(o), pdfobj.String(u)
		switch e.R {
		case 2:
			d["V"] = int64(1)
		case 3:
			d["V"], d["Length"] = int64(2), int64(128)
		case 4:
			d["V"], d["Length"] = int64(4), int64(128)
			d["CF"] = pdfobj.Dict{"StdCF": pdfobj.Dict{"CFM": pdfobj.Name("AESV2"), "Length": int64(16)}}
			d["StmF"], d["StrF"] = pdfobj.Name("StdCF"), pdfobj.Name("StdCF")
		}
	}
	if e.ClearMetadata && e.R >= 4 {
		d["EncryptMetadata"] = false
	}
	enc.e = e
	enc.dict = d
	return enc
}

func sum256(parts ...[]byte) []byte {
	h := sha256.New()
	for _, p := range parts {
		h.Write(p)
	}
	return h.Sum(nil)
}

// hardenedHash is the revision 6 password hash.
func hardenedHash(pw, salt, udata []byte) []byte {
	k := sum256(pw, salt, udata)
	for i := 0; ; i++ {
		var k1 []byte
		for j := 0; j < 64; j++ {
			k1 = append(append(append(k1, pw...), k...), udata...)
		}
		block, _ := aes.NewCipher(k[:16])
		e := make([]byte, len(k1))
		cipher.NewCBCEncrypter(block, k[16:32]).CryptBlocks(e, k1)
		mod := 0
		for _, b := range e[:16] {
			mod = (mod + int(b)) % 3
		}
		switch mod {
		case 0:
			k = sum256(e)
		case 1:
			s := sha512.Sum384(e)
			k = s[:]
		case 2:
			s := sha512.Sum512(e)
			k = s[:]
		}
		// i counts completed rounds minus one.
		if i >= 63 && int(e[len(e)-1]) <= i-31 {
			return k[:32]
		}
	}
}

func (enc *encryptor) crypt(data []byte, num int) []byte {
	if enc.e.R >= 5 {
		return enc.aes(enc.key, data)
	}
	h := md5.New()
	h.Write(enc.key)
	h.Write([]byte{byte(num), byte(num >> 8), byte(num >> 16), 0, 0})
	if enc.e.R == 4 {
		h.Write([]byte("sAlT"))
	}
	key := h.Sum(nil)[:min(len(enc.key)+5, 16)]
	if enc.e.R == 4 {
		return enc.aes(key, data)
	}
	return rc4With(key, data)
}

func (enc *encryptor) aes(key, data []byte) []byte {
	enc.iv++
	iv := sum256([]byte{byte(enc.iv), byte(enc.iv >> 8)})[:16]
	padLen := 16 - len(data)%16
	padded := append(append([]byte(nil), data...), make([]byte, padLen)...)
	for i := len(data); i < len(padded); i++ {
		padded[i] = byte(padLen)
	}
	block, _ := aes.NewCipher(key)
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, padded)
	return append(iv, out...)
}

// strings returns a copy of o with every string encrypted for object num.
func (enc *encryptor) strings(o pdfobj.Object, num int) pdfobj.Object {
	switch v := o.(type) {
	case pdfobj.String:
		return pdfobj.String(enc.crypt(v, num))
	case pdfobj.Array:
		out := make(pdfobj.Array, len(v))
		for i := range v {
			out[i] = enc.strings(v[i], num)
		}
		return out
	case pdfobj.Dict:
		out := make(pdfobj.Dict, len(v))
		for k, x := range v {
			out[k] = enc.strings(x, num)
		}
		return out
	}
	return o
}

func (enc *encryptor) stream(data []byte, num int) []byte {
	return enc.crypt(data, num)
}
