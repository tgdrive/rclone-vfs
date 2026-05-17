//go:build linux

package file

import "os"

// SetSparse makes the file be a sparse file
// On Linux the file is already created as sparse by default, so this is a no-op.
func SetSparse(out *os.File) error {
	return nil
}
