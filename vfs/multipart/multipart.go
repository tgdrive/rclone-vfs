// Package multipart provides the RW buffer type used by the parallel chunked reader.
package multipart

import "github.com/tgdrive/rclone-vfs/vfs/pool"

// BufferSize is fixed at 1 MiB to match the parallel reader's alignment needs.
const BufferSize = 1024 * 1024

// NewRW returns a new RW buffer.
func NewRW() *pool.RW {
	return pool.NewRW(BufferSize)
}
