package types

import (
	"context"
	"fmt"
	"io"
)

// OpenOption is an interface for options passed to RemoteObject.Open.
type OpenOption interface {
	// Header returns the HTTP header key and value for this option.
	Header() (key, value string)
}

// RangeOption sets the Range header on HTTP requests.
type RangeOption struct {
	Start, End int64
}

func (o *RangeOption) Header() (key, value string) {
	if o.End < 0 {
		return "Range", fmt.Sprintf("bytes=%d-", o.Start)
	}
	return "Range", fmt.Sprintf("bytes=%d-%d", o.Start, o.End)
}

// RemoteObject is the interface that a remote HTTP file must satisfy.
// This replaces the rclone fs.Object interface in the forked VFS.
type RemoteObject interface {
	// Open opens the remote file for reading with the given options.
	// Options typically include RangeOption for seeking.
	Open(ctx context.Context, options ...OpenOption) (io.ReadCloser, error)
	// Size returns the file size in bytes, or -1 if unknown.
	Size() int64
	// String returns a human-readable representation (e.g., the URL).
	String() string
}
