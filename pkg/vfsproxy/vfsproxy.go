package vfsproxy

import (
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"sync"

	_ "github.com/rclone/rclone/backend/local"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/fs/config/configstruct"
	"github.com/rclone/rclone/vfs"
	"github.com/rclone/rclone/vfs/vfscommon"
	"github.com/tgdrive/vfscache-proxy/backend/link"
)

type Options struct {
	FsName            string
	CacheDir          string
	CacheMaxAge       string
	CacheMaxSize      string
	CacheChunkSize    string
	CacheChunkStreams int
	StripQuery        bool
	StripDomain       bool
	ShardLevel        int

	// Additional VFS Options
	CacheMode         string
	WriteWait         string
	ReadWait          string
	WriteBack         string
	DirCacheTime      string
	FastFingerprint   bool
	CacheMinFreeSpace string
	CaseInsensitive   bool
	ReadOnly          bool
	NoModTime         bool
	NoChecksum        bool
	NoSeek            bool
	DirPerms          string
	FilePerms         string
}

// DefaultOptions returns Options with sensible defaults applied from rclone.
func DefaultOptions() Options {
	opt := Options{
		FsName:     "link-vfs",
		ShardLevel: 1,
	}

	// Fetch defaults from rclone vfscommon.Opt
	vfsOpt := vfscommon.Opt
	items, err := configstruct.Items(&vfsOpt)
	if err != nil {
		return opt // Fallback to minimal defaults on error
	}

	for _, item := range items {
		valStr, _ := configstruct.InterfaceToString(item.Value)
		switch item.Name {
		case "vfs_cache_mode":
			opt.CacheMode = valStr
		case "vfs_cache_max_age":
			opt.CacheMaxAge = valStr
		case "vfs_cache_max_size":
			opt.CacheMaxSize = valStr
		case "vfs_read_chunk_size":
			opt.CacheChunkSize = valStr
		case "vfs_read_chunk_streams":
			if i, err := strconv.Atoi(valStr); err == nil {
				opt.CacheChunkStreams = i
			}
		case "vfs_write_wait":
			opt.WriteWait = valStr
		case "vfs_read_wait":
			opt.ReadWait = valStr
		case "vfs_write_back":
			opt.WriteBack = valStr
		case "dir_cache_time":
			opt.DirCacheTime = valStr
		case "vfs_fast_fingerprint":
			opt.FastFingerprint = (valStr == "true")
		case "vfs_cache_min_free_space":
			opt.CacheMinFreeSpace = valStr
		case "vfs_case_insensitive":
			opt.CaseInsensitive = (valStr == "true")
		case "read_only":
			opt.ReadOnly = (valStr == "true")
		case "no_modtime":
			opt.NoModTime = (valStr == "true")
		case "no_checksum":
			opt.NoChecksum = (valStr == "true")
		case "no_seek":
			opt.NoSeek = (valStr == "true")
		case "dir_perms":
			opt.DirPerms = valStr
		case "file_perms":
			opt.FilePerms = valStr
		}
	}

	return opt
}

type Handler struct {
	VFS         *vfs.VFS
	mu          sync.RWMutex
	hashCache   map[string]string
	stripQuery  bool
	stripDomain bool
	shardLevel  int
}

func NewHandler(opt Options) (*Handler, error) {
	ctx := context.Background()

	m := configmap.Simple{
		"type":         "link",
		"strip_query":  strconv.FormatBool(opt.StripQuery),
		"strip_domain": strconv.FormatBool(opt.StripDomain),
		"shard_level":  strconv.Itoa(opt.ShardLevel),
	}

	// Create a new file system for the link backend
	f, err := fs.NewFs(ctx, opt.FsName+":")
	if err != nil {
		// Fallback to manual creation if not in rclone config
		f, err = link.NewFs(ctx, opt.FsName, "", m)
		if err != nil {
			return nil, fmt.Errorf("failed to create link backend: %w", err)
		}
	}

	// Configure VFS options
	vfsOpt := vfscommon.Opt
	optMap := configmap.Simple{
		"vfs_cache_mode":           opt.CacheMode,
		"vfs_cache_max_age":        opt.CacheMaxAge,
		"vfs_cache_max_size":       opt.CacheMaxSize,
		"vfs_read_chunk_size":      opt.CacheChunkSize,
		"vfs_read_chunk_streams":   strconv.Itoa(opt.CacheChunkStreams),
		"vfs_write_wait":           opt.WriteWait,
		"vfs_read_wait":            opt.ReadWait,
		"vfs_write_back":           opt.WriteBack,
		"dir_cache_time":           opt.DirCacheTime,
		"vfs_fast_fingerprint":     strconv.FormatBool(opt.FastFingerprint),
		"vfs_cache_min_free_space": opt.CacheMinFreeSpace,
		"vfs_case_insensitive":     strconv.FormatBool(opt.CaseInsensitive),
		"read_only":                strconv.FormatBool(opt.ReadOnly),
		"no_modtime":               strconv.FormatBool(opt.NoModTime),
		"no_checksum":              strconv.FormatBool(opt.NoChecksum),
		"no_seek":                  strconv.FormatBool(opt.NoSeek),
		"dir_perms":                opt.DirPerms,
		"file_perms":               opt.FilePerms,
	}

	if err := configstruct.Set(optMap, &vfsOpt); err != nil {
		return nil, fmt.Errorf("failed to parse VFS options: %w", err)
	}
	vfsOpt.Init() // Initialize options (sets up permissions, etc.)

	actualCacheDir := opt.CacheDir
	if actualCacheDir == "" {
		actualCacheDir = filepath.Join(os.TempDir(), "rclone_vfs_cache")
	}
	if err := config.SetCacheDir(actualCacheDir); err != nil {
		return nil, fmt.Errorf("failed to set cache directory: %w", err)
	}

	vfsInstance := vfs.New(f, &vfsOpt)
	return &Handler{
		VFS:         vfsInstance,
		hashCache:   make(map[string]string),
		stripQuery:  opt.StripQuery,
		stripDomain: opt.StripDomain,
		shardLevel:  opt.ShardLevel,
	}, nil
}

func (h *Handler) Shutdown() {
	h.VFS.Shutdown()
}

func (h *Handler) getFileHash(targetURL string) string {
	h.mu.RLock()
	fileHash, exists := h.hashCache[targetURL]
	h.mu.RUnlock()

	if exists {
		return fileHash
	}

	// Apply stripping to the URL before hashing
	keyURL := link.StripURL(targetURL, h.stripQuery, h.stripDomain)

	hashBytes := md5.Sum([]byte(keyURL))
	computedHash := fmt.Sprintf("%x", hashBytes)

	// Double-checked locking to avoid duplicate computation
	h.mu.Lock()
	if fileHash, exists = h.hashCache[targetURL]; exists {
		h.mu.Unlock()
		return fileHash
	}
	h.hashCache[targetURL] = computedHash
	h.mu.Unlock()

	return computedHash
}

func (h *Handler) Serve(w http.ResponseWriter, r *http.Request, targetURL string) {
	if targetURL == "" {
		http.Error(w, "Target URL is required", http.StatusBadRequest)
		return
	}

	fileHash := h.getFileHash(targetURL)

	link.Register(fileHash, targetURL, r.Header.Clone())

	h.ServeFile(w, r, link.ShardedPath(fileHash, h.shardLevel))
}

func (h *Handler) ServeFile(w http.ResponseWriter, r *http.Request, remote string) {
	ctx := r.Context()
	node, err := h.VFS.Stat(remote)
	if err == vfs.ENOENT {
		fs.Infof(remote, "%s: File not found", r.RemoteAddr)
		http.Error(w, "File not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, "Failed to find file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if !node.IsFile() {
		http.Error(w, "Not a file", http.StatusNotFound)
		return
	}

	entry := node.DirEntry()
	if entry == nil {
		http.Error(w, "Can't open file being written", http.StatusNotFound)
		return
	}
	obj := entry.(fs.Object)
	file := node.(*vfs.File)

	knownSize := obj.Size() >= 0
	if knownSize {
		w.Header().Set("Content-Length", strconv.FormatInt(node.Size(), 10))
	}

	mimeType := fs.MimeType(ctx, obj)
	if mimeType == "application/octet-stream" && path.Ext(remote) == "" {
	} else {
		w.Header().Set("Content-Type", mimeType)
	}
	w.Header().Set("Last-Modified", file.ModTime().UTC().Format(http.TimeFormat))

	if r.Method == "HEAD" {
		return
	}

	// open the object
	in, err := file.Open(os.O_RDONLY)
	if err != nil {
		http.Error(w, "Failed to open file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer func() {
		_ = in.Close()
	}()

	if knownSize {
		http.ServeContent(w, r, remote, file.ModTime(), in)
	} else {
		if rangeRequest := r.Header.Get("Range"); rangeRequest != "" {
			http.Error(w, "Can't use Range: on files of unknown length", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		n, err := io.Copy(w, in)
		if err != nil {
			fs.Errorf(obj, "Didn't finish writing GET request (wrote %d/unknown bytes): %v", n, err)
			return
		}
	}
}
