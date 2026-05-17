// Package vfs provides a virtual filing system layer backed by an HTTP
// cache.  This is a fork of rclone's VFS with all rclone fs.Object/fs.Fs
// dependencies removed, replaced with a standalone httpFile type.
package vfs

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tgdrive/varc/vfs/vfscache"
	"github.com/tgdrive/varc/vfs/vfscommon"
)

// Node represents either a directory (*Dir) or a file (*File)
type Node interface {
	os.FileInfo
	IsFile() bool
	Inode() uint64
	SetModTime(modTime time.Time) error
	Sync() error
	Remove() error
	RemoveAll() error
	VFS() *VFS
	Open(flags int) (Handle, error)
	Truncate(size int64) error
	Path() string
	SetSys(any)
}

// Check interfaces
var (
	_ Node = (*File)(nil)
	_ Node = (*Dir)(nil)
)

// Nodes is a slice of Node
type Nodes []Node

func (ns Nodes) Len() int           { return len(ns) }
func (ns Nodes) Swap(i, j int)      { ns[i], ns[j] = ns[j], ns[i] }
func (ns Nodes) Less(i, j int) bool { return ns[i].Path() < ns[j].Path() }

// Noder represents something which can return a node
type Noder interface {
	fmt.Stringer
	Node() Node
}

// Check interfaces
var (
	_ Noder = (*ReadFileHandle)(nil)
)

// OsFiler is the methods on *os.File
type OsFiler interface {
	Chdir() error
	Chmod(mode os.FileMode) error
	Chown(uid, gid int) error
	Close() error
	Fd() uintptr
	Name() string
	Read(b []byte) (n int, err error)
	ReadAt(b []byte, off int64) (n int, err error)
	Readdir(n int) ([]os.FileInfo, error)
	Readdirnames(n int) (names []string, err error)
	Seek(offset int64, whence int) (ret int64, err error)
	Stat() (os.FileInfo, error)
	Sync() error
	Truncate(size int64) error
	Write(b []byte) (n int, err error)
	WriteAt(b []byte, off int64) (n int, err error)
	WriteString(s string) (n int, err error)
}

// Handle is the interface satisfied by open files or directories.
type Handle interface {
	OsFiler
	Flush() error
	Release() error
	Node() Node
	Lock() error
	Unlock() error
}

// baseHandle implements all the missing methods
type baseHandle struct{}

func (h baseHandle) Chdir() error                                         { return ENOSYS }
func (h baseHandle) Chmod(mode os.FileMode) error                         { return ENOSYS }
func (h baseHandle) Chown(uid, gid int) error                             { return ENOSYS }
func (h baseHandle) Close() error                                         { return ENOSYS }
func (h baseHandle) Fd() uintptr                                          { return 0 }
func (h baseHandle) Name() string                                         { return "" }
func (h baseHandle) Read(b []byte) (n int, err error)                     { return 0, ENOSYS }
func (h baseHandle) ReadAt(b []byte, off int64) (n int, err error)        { return 0, ENOSYS }
func (h baseHandle) Readdir(n int) ([]os.FileInfo, error)                 { return nil, ENOSYS }
func (h baseHandle) Readdirnames(n int) (names []string, err error)       { return nil, ENOSYS }
func (h baseHandle) Seek(offset int64, whence int) (ret int64, err error) { return 0, ENOSYS }
func (h baseHandle) Stat() (os.FileInfo, error)                           { return nil, ENOSYS }
func (h baseHandle) Sync() error                                          { return nil }
func (h baseHandle) Truncate(size int64) error                            { return ENOSYS }
func (h baseHandle) Write(b []byte) (n int, err error)                    { return 0, ENOSYS }
func (h baseHandle) WriteAt(b []byte, off int64) (n int, err error)       { return 0, ENOSYS }
func (h baseHandle) WriteString(s string) (n int, err error)              { return 0, ENOSYS }
func (h baseHandle) Flush() (err error)                                   { return ENOSYS }
func (h baseHandle) Release() (err error)                                 { return ENOSYS }
func (h baseHandle) Node() Node                                           { return nil }
func (h baseHandle) Unlock() error                                        { return os.ErrInvalid }
func (h baseHandle) Lock() error                                          { return os.ErrInvalid }

// Check interfaces
var (
	_ OsFiler = (*os.File)(nil)
	_ Handle  = (*baseHandle)(nil)
	_ Handle  = (*ReadFileHandle)(nil)
	_ Handle  = (*WriteFileHandle)(nil)
	_ Handle  = (*DirHandle)(nil)
)

// VFS represents the top level filing system
type VFS struct {
	ctx         context.Context
	root        *Dir
	Opt         vfscommon.Options
	cache       *vfscache.Cache
	cancel      context.CancelFunc
	cancelCache context.CancelFunc
	usageMu     sync.Mutex
	usageTime   time.Time
	pollChan    chan time.Duration
	inUse       atomic.Int32
}

// New creates a new VFS and root directory.
func New(ctx context.Context, opt *vfscommon.Options) (*VFS, error) {
	ctx, cancel := context.WithCancel(ctx)
	vfs := &VFS{
		ctx:    ctx,
		cancel: cancel,
	}
	vfs.inUse.Store(1)

	if opt != nil {
		vfs.Opt = *opt
	}

	vfs.Opt.Init()

	// Create cache
	cache, err := vfscache.New(ctx, &vfs.Opt, vfs.addVirtual)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("vfs: failed to create cache: %w", err)
	}
	vfs.cache = cache

	// Create root directory
	vfs.root = newDir(vfs, nil, "/")

	vfs.SetCacheMode(vfs.Opt.CacheMode)

	return vfs, nil
}

// Keep track of active VFS keyed on the name
var (
	activeMu sync.Mutex
	active   = map[string][]*VFS{}
)

// SetCacheMode sets the VFS cache mode
func (vfs *VFS) SetCacheMode(cacheMode vfscommon.CacheMode) {
	vfs.Opt.CacheMode = cacheMode

	switch cacheMode {
	case vfscommon.CacheModeFull, vfscommon.CacheModeWrites:
		ctx, cancel := context.WithCancel(vfs.ctx)
		vfs.cancelCache = cancel
		cache, err := vfscache.New(ctx, &vfs.Opt, vfs.addVirtual)
		if err == nil {
			vfs.cache = cache
		}
	default:
		if vfs.cancelCache != nil {
			vfs.cancelCache()
			vfs.cancelCache = nil
		}
	}
}

// addVirtual is called when the cache creates a virtual entry
func (vfs *VFS) addVirtual(remote string, size int64, isDir bool) error {
	// In our fork, we don't create virtual entries in the VFS tree
	// This is a simplified no-op
	return nil
}

// Root returns the root directory
func (vfs *VFS) Root() *Dir {
	return vfs.root
}

// Usage returns the disk usage of the current VFS
func (vfs *VFS) Usage() (total, used int64) {
	vfs.usageMu.Lock()
	defer vfs.usageMu.Unlock()
	// Return a basic estimate based on cache metrics or return 0
	return 0, 0
}

// Cache returns the cache object
func (vfs *VFS) Cache() *vfscache.Cache {
	return vfs.cache
}

// Context returns the VFS context
func (vfs *VFS) Context() context.Context {
	return vfs.ctx
}

// OpenFile opens a file for reading
func (vfs *VFS) OpenFile(name string) (Handle, error) {
	// Resolve the name relative to root
	name = strings.Trim(name, "/")
	return vfs.root.OpenFile(name, os.O_RDONLY, 0777)
}



// Close shuts down the VFS
func (vfs *VFS) Close() error {
	if vfs.cancelCache != nil {
		vfs.cancelCache()
	}
	if vfs.cache != nil {
		_ = vfs.cache.CleanUp()
	}
	vfs.cancel()
	vfs.inUse.Store(0)
	return nil
}

// ReadFileInto reads a full file into the writer
func (vfs *VFS) ReadFileInto(ctx context.Context, name string, w io.Writer) error {
	handle, err := vfs.OpenFile(name)
	if err != nil {
		return err
	}
	defer handle.Close()
	_, err = io.Copy(w, handle)
	return err
}

var inodeCount atomic.Uint64

// newInode creates a new unique inode number
func newInode() (inode uint64) {
	return inodeCount.Add(1)
}

// Stat finds the Node by path starting from the root
func (vfs *VFS) Stat(path string) (node Node, err error) {
	return vfs.root.Stat(path)
}

// Open opens a file by path
func (vfs *VFS) Open(path string) (Handle, error) {
	path = strings.Trim(path, "/")
	if path == "" {
		return nil, os.ErrInvalid
	}
	return vfs.root.OpenFile(path, os.O_RDONLY, 0)
}

// OpenCached opens a file for reading, optionally associating a RemoteObject
// for cache population. If obj is nil, only the local cache is used.
func (vfs *VFS) OpenCached(filePath string, obj vfscommon.RemoteObject) (Handle, error) {
	filePath = strings.Trim(filePath, "/")
	if filePath == "" {
		return nil, os.ErrInvalid
	}

	// Get or create the cache item
	item := vfs.cache.Item(filePath)
	if item == nil {
		return nil, fmt.Errorf("failed to create cache item for %s", filePath)
	}

	// Open the cache item with the remote object if provided
	if obj != nil {
		if err := item.Open(obj); err != nil {
			return nil, fmt.Errorf("failed to open cache item: %w", err)
		}
	}

	// Ensure the file node exists in the VFS tree
	_, err := vfs.root.Stat(filePath)
	if err != nil {
		d := vfs.root
		f := newFile(vfs.ctx, d, filePath)
		if size, err := item.GetSize(); err == nil {
			f.size.Store(size)
		}
		d.AddChild(filePath, f)
	}

	return vfs.root.OpenFile(filePath, os.O_RDONLY, 0)
}

// CacheItem returns the cache item for a path, creating it if needed
func (vfs *VFS) CacheItem(path string) *vfscache.Item {
	return vfs.cache.Item(path)
}


