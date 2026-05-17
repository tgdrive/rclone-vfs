// Package chunkedreader provides functionality for reading a remote file in chunks.
//
// This is a fork of rclone's fs/chunkedreader adapted to use the local
// types.RemoteObject interface instead of rclone's fs.Object.
package chunkedreader

import (
	"context"
	"errors"
	"io"

	"github.com/tgdrive/varc/internal/types"
)

// io related errors returned by ChunkedReader
var (
	ErrorFileClosed  = errors.New("file already closed")
	ErrorInvalidSeek = errors.New("invalid seek position")
)

// ChunkedReader describes what a chunked reader can do.
type ChunkedReader interface {
	io.Reader
	io.Seeker
	io.Closer
	RangeSeeker
	Open() (ChunkedReader, error)
}

// RangeSeeker is the interface that allows a reader to seek by specifying a length.
type RangeSeeker interface {
	RangeSeek(ctx context.Context, offset int64, whence int, length int64) (int64, error)
}

// New returns a ChunkedReader for the RemoteObject.
//
// An initialChunkSize of <= 0 will disable chunked reading.
// If maxChunkSize is greater than initialChunkSize, the chunk size will be
// doubled after each chunk read with a maximum of maxChunkSize.
// A Seek or RangeSeek will reset the chunk size to its initial value
func New(ctx context.Context, o types.RemoteObject, initialChunkSize int64, maxChunkSize int64, streams int) ChunkedReader {
	if initialChunkSize <= 0 {
		initialChunkSize = -1
	}
	if maxChunkSize != -1 && maxChunkSize < initialChunkSize {
		maxChunkSize = initialChunkSize
	}
	if streams < 0 {
		streams = 0
	}
	if streams <= 1 || o.Size() < 0 {
		return newSequential(ctx, o, initialChunkSize, maxChunkSize)
	}
	return newParallel(ctx, o, initialChunkSize, streams)
}
