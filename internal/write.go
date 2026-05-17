package internal

// WriteFileHandle is not yet implemented in the forked engine.
type WriteFileHandle struct {
	*baseHandle
}

// RWFileHandle is not yet implemented in the forked engine.
type RWFileHandle struct {
	*baseHandle
}

func newWriteFileHandle(d *Dir, f *File, remote string) (*WriteFileHandle, error) {
	return &WriteFileHandle{baseHandle: &baseHandle{}}, ENOSYS
}

func newRWFileHandle(d *Dir, f *File, remote string) (*RWFileHandle, error) {
	return &RWFileHandle{baseHandle: &baseHandle{}}, ENOSYS
}

func (fh *WriteFileHandle) Node() Node { return nil }
func (fh *WriteFileHandle) Flush() error { return nil }
func (fh *WriteFileHandle) Release() error { return nil }
func (fh *RWFileHandle) Node() Node { return nil }
func (fh *RWFileHandle) Flush() error { return nil }
func (fh *RWFileHandle) Release() error { return nil }
