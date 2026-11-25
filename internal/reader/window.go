// Copyright 2026 The unpdf Authors
// SPDX-License-Identifier: Apache-2.0

package reader

import "io"

// windowSize is how much of the file is read at a time for small reads.
const windowSize = 128 << 10

// window is an io.ReaderAt that reads ahead. Objects are small and those
// that are used together tend to lie together in the file, so reading a
// block at a time turns most object loads into a copy from memory. Reads of
// half a window or more, which are stream data, go straight to the source.
type window struct {
	src io.ReaderAt
	buf []byte
	off int64 // offset in the file of buf[0]
}

func (w *window) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || len(p) >= windowSize/2 {
		return w.src.ReadAt(p, off)
	}
	if off >= w.off && off+int64(len(p)) <= w.off+int64(len(w.buf)) {
		return copy(p, w.buf[off-w.off:]), nil
	}
	if cap(w.buf) < windowSize {
		w.buf = make([]byte, windowSize)
	}
	n, err := w.src.ReadAt(w.buf[:windowSize], off)
	w.buf, w.off = w.buf[:n], off
	if n >= len(p) {
		return copy(p, w.buf), nil
	}
	// The file ends inside the request.
	if err == nil {
		err = io.EOF
	}
	return copy(p, w.buf), err
}
