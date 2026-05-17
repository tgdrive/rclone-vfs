package internal

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// Dir represents a directory entry
type Dir struct {
	engine *Engine
	inode  uint64 // read only: inode number
	mu     sync.RWMutex
	parent *Dir   // parent directory
	name   string // the directory name relative to the root
	modTime time.Time
	children map[string]Node // direct children known to this engine
}

// newDir creates a new directory
func newDir(eng *Engine, parent *Dir, name string) *Dir {
	name = strings.Trim(name, "/")
	d := &Dir{
		engine:   eng,
		inode:    newInode(),
		parent:   parent,
		name:     name,
		modTime:  time.Now(),
		children: make(map[string]Node),
	}
	return d
}

// Name returns the directory name
func (d *Dir) Name() string {
	return d.name
}

// Path returns the path relative to VFS root
func (d *Dir) Path() string {
	return d.name
}

// IsFile returns false for directories
func (d *Dir) IsFile() bool {
	return false
}

// IsDir returns true for directories
func (d *Dir) IsDir() bool {
	return true
}

// ModTime returns the modification time
func (d *Dir) ModTime() time.Time {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.modTime
}

// Size returns 0 for directories
func (d *Dir) Size() int64 {
	return 0
}

// Mode returns the directory mode
func (d *Dir) Mode() os.FileMode {
	return os.ModeDir | 0755
}

// Sys returns nil
func (d *Dir) Sys() any {
	return nil
}

// SetSys is a no-op
func (d *Dir) SetSys(x any) {}

// Inode returns the inode number
func (d *Dir) Inode() uint64 {
	return d.inode
}

// Engine returns the parent Engine
func (d *Dir) Engine() *Engine {
	return d.engine
}

// DirEntry returns the DirEntry for this directory
func (d *Dir) DirEntry() os.FileInfo {
	return d
}

// Sync syncs the directory - no-op
func (d *Dir) Sync() error {
	return nil
}

// Remove removes this directory
func (d *Dir) Remove() error {
	return nil
}

// RemoveAll removes this directory and its contents
func (d *Dir) RemoveAll() error {
	return nil
}

// Truncate returns error for directories
func (d *Dir) Truncate(size int64) error {
	return ENOSYS
}

// SetModTime sets the modification time
func (d *Dir) SetModTime(modTime time.Time) error {
	d.mu.Lock()
	d.modTime = modTime
	d.mu.Unlock()
	return nil
}

// Open opens the directory for reading - not fully supported yet
func (d *Dir) Open(flags int) (Handle, error) {
	return nil, ENOSYS
}

// AddChild adds a child node to the directory
func (d *Dir) AddChild(name string, node Node) {
	d.mu.Lock()
	d.children[name] = node
	d.mu.Unlock()
}

// Stat finds the Node by path starting from this directory
func (d *Dir) Stat(filePath string) (node Node, err error) {
	filePath = strings.Trim(filePath, "/")
	if filePath == "" {
		return d, nil
	}

	// Look for the file directly
	d.mu.RLock()
	node, found := d.children[filePath]
	d.mu.RUnlock()
	if found {
		return node, nil
	}

	// If the file is in the cache, create a node for it
	if d.engine.cache != nil {
		cachePath := d.engine.cache.Item(filePath)
		if cachePath != nil && cachePath.Exists() {
			f := newFile(d.engine.Context(), d, filePath)
			if size, err := cachePath.GetSize(); err == nil {
				f.size.Store(size)
			}
			d.AddChild(filePath, f)
			return f, nil
		}
	}

	return nil, os.ErrNotExist
}

// OpenFile opens a file by path with the given flags
func (d *Dir) OpenFile(filePath string, flags int, perm os.FileMode) (Handle, error) {
	filePath = strings.Trim(filePath, "/")
	if filePath == "" {
		return nil, os.ErrInvalid
	}

	// Get or create the file node
	node, err := d.Stat(filePath)
	if err != nil {
		return nil, err
	}

	if !node.IsFile() {
		return nil, fmt.Errorf("%w: is a directory", os.ErrInvalid)
	}

	return node.Open(flags)
}

// ReadDir reads the directory contents - not fully supported
func (d *Dir) ReadDir() (Nodes, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var nodes Nodes
	for _, node := range d.children {
		nodes = append(nodes, node)
	}
	return nodes, nil
}

