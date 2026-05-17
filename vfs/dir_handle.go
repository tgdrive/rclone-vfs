package vfs

import (
	"io"
	"os"
)

// DirHandle represents an open directory
type DirHandle struct {
	*baseHandle
	d      *Dir
	read   bool
	offset int
}

func newDirHandle(d *Dir) *DirHandle {
	return &DirHandle{
		baseHandle: &baseHandle{},
		d:          d,
	}
}

// ReadDir reads the directory contents
func (fh *DirHandle) ReadDir(n int) ([]os.FileInfo, error) {
	return nil, io.EOF
}

// Readdirnames reads the directory names
func (fh *DirHandle) Readdirnames(n int) ([]string, error) {
	return nil, io.EOF
}

// Node returns the underlying Dir
func (fh *DirHandle) Node() Node {
	return fh.d
}

// Flush flushes the directory handle
func (fh *DirHandle) Flush() error {
	return nil
}

// Release releases the directory handle
func (fh *DirHandle) Release() error {
	return nil
}

// Stat returns info about the directory
func (fh *DirHandle) Stat() (os.FileInfo, error) {
	return fh.d, nil
}
