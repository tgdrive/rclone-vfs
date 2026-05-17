// Package pool provides a concurrent read-write buffer used by the parallel chunked reader.
package pool

import (
	"context"
	"io"
	"sync"
)

// RW is a concurrent read-write buffer.
//
// One goroutine writes into the buffer via ReadFrom while another reads
// from it via Read. WaitWrite synchronizes the reader with the writer.
type RW struct {
	mu      sync.Mutex
	cond    *sync.Cond
	buf     []byte
	rOffset int   // current read position in buf
	closed  bool
}

// NewRW creates a new RW with the given initial capacity.
func NewRW(capacity int) *RW {
	rw := &RW{
		buf: make([]byte, 0, capacity),
	}
	rw.cond = sync.NewCond(&rw.mu)
	return rw
}

// ReadFrom reads data from r into the buffer. It is intended to be called
// from a single background goroutine.
func (rw *RW) ReadFrom(r io.Reader) (int64, error) {
	tmp := make([]byte, 4096)
	var total int64
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			rw.mu.Lock()
			rw.buf = append(rw.buf, tmp[:n]...)
			rw.cond.Broadcast()
			rw.mu.Unlock()
			total += int64(n)
		}
		if err == io.EOF {
			return total, nil
		}
		if err != nil {
			return total, err
		}
	}
}

// Read copies up to len(p) bytes from the buffer into p.
func (rw *RW) Read(p []byte) (int, error) {
	rw.mu.Lock()
	defer rw.mu.Unlock()

	if rw.rOffset >= len(rw.buf) {
		return 0, io.EOF
	}

	n := copy(p, rw.buf[rw.rOffset:])
	rw.rOffset += n
	return n, nil
}

// WaitWrite blocks until new data is available to read or ctx is done.
func (rw *RW) WaitWrite(ctx context.Context) {
	rw.mu.Lock()
	defer rw.mu.Unlock()

	ch := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			rw.cond.Broadcast()
		case <-ch:
		}
	}()

	for rw.rOffset >= len(rw.buf) && !rw.closed {
		rw.cond.Wait()
	}
	close(ch)
}

// Close marks the buffer as closed and wakes any waiters.
func (rw *RW) Close() error {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	rw.closed = true
	rw.cond.Broadcast()
	return nil
}

// Size returns the total number of bytes that have been written to the buffer.
func (rw *RW) Size() int64 {
	rw.mu.Lock()
	defer rw.mu.Unlock()
	return int64(len(rw.buf))
}

// Seek sets the read position for the next Read.
func (rw *RW) Seek(offset int64, whence int) (int64, error) {
	rw.mu.Lock()
	defer rw.mu.Unlock()

	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = int64(rw.rOffset) + offset
	case io.SeekEnd:
		abs = int64(len(rw.buf)) + offset
	default:
		return 0, io.EOF
	}
	if abs < 0 {
		return 0, io.EOF
	}
	if abs > int64(len(rw.buf)) {
		abs = int64(len(rw.buf))
	}
	rw.rOffset = int(abs)
	return abs, nil
}
