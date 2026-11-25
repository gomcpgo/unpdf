// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package reader

import (
	"bytes"
	"io"
	"math/rand"
	"testing"
)

// countingReader counts the reads that reach it.
type countingReader struct {
	r     *bytes.Reader
	reads int
}

func (c *countingReader) ReadAt(p []byte, off int64) (int, error) {
	c.reads++
	return c.r.ReadAt(p, off)
}

func TestWindow(t *testing.T) {
	data := make([]byte, 3*windowSize+777)
	rng := rand.New(rand.NewSource(1))
	rng.Read(data)
	plain := bytes.NewReader(data)
	src := &countingReader{r: bytes.NewReader(data)}
	w := &window{src: src}

	// Whatever is asked, the answer is the one the source would give.
	for i := 0; i < 5000; i++ {
		off := rng.Int63n(int64(len(data)) + 200)
		size := rng.Intn(3000)
		if i%50 == 0 {
			size = windowSize/2 + rng.Intn(windowSize)
		}
		got, want := make([]byte, size), make([]byte, size)
		n1, err1 := w.ReadAt(got, off)
		n2, err2 := plain.ReadAt(want, off)
		if n1 != n2 || !bytes.Equal(got[:n1], want[:n2]) || (err1 == nil) != (err2 == nil) || (err1 == io.EOF) != (err2 == io.EOF) {
			t.Fatalf("read %d at %d: got %d bytes, %v; want %d bytes, %v", size, off, n1, err1, n2, err2)
		}
	}
	if _, err := w.ReadAt(make([]byte, 10), -1); err == nil {
		t.Error("a negative offset was accepted")
	}

	// Small reads that follow one another cost one read of the source.
	src.reads = 0
	buf := make([]byte, 100)
	for off := int64(0); off < windowSize-100; off += 100 {
		w.ReadAt(buf, off)
	}
	if src.reads > 2 {
		t.Errorf("%d reads of the source for one window of small reads", src.reads)
	}
}
