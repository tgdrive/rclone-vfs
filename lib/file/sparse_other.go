//go:build !windows && !linux

package file

import "os"

// SetSparse makes the file be a sparse file
// On this platform SetSparse is a no-op.
func SetSparse(out *os.File) error {
	return nil
}
