package vfsproxy

import (
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tgdrive/rclone-vfs/vfs"
	"github.com/tgdrive/rclone-vfs/vfs/vfscommon"
)

// Options holds configuration for the VFS proxy handler
type Options struct {
	CacheDir          string `caddy:"cache_dir"`
	CacheMaxAge       string `caddy:"max_age"`
	CacheMaxSize      string `caddy:"max_size"`
	CacheChunkSize    string `caddy:"chunk_size"`
	CacheChunkStreams int    `caddy:"chunk_streams"`
	CacheMode         string `caddy:"cache_mode"`
	StripQuery        bool   `caddy:"strip_query"`
	StripDomain       bool   `caddy:"strip_domain"`
	ShardLevel        int    `caddy:"shard_level"`
}

// DefaultOptions returns Options with sensible defaults
func DefaultOptions() Options {
	return Options{
		ShardLevel:        1,
		CacheChunkStreams: 2,
		CacheMode:         "minimal",
	}
}

// mapping tracks URL-to-cache-path mappings so the VFS knows
// which upstream URL and headers to use for each cache path
type mapping struct {
	mu          sync.RWMutex
	entries     map[string]cacheEntry
}

type cacheEntry struct {
	url     string
	headers http.Header
}

func newMapping() *mapping {
	return &mapping{entries: make(map[string]cacheEntry)}
}

func (m *mapping) put(url, cachePath string, headers http.Header) {
	m.mu.Lock()
	m.entries[cachePath] = cacheEntry{url: url, headers: headers.Clone()}
	m.mu.Unlock()
}

func (m *mapping) get(cachePath string) (cacheEntry, bool) {
	m.mu.RLock()
	e, ok := m.entries[cachePath]
	m.mu.RUnlock()
	return e, ok
}

// Handler is the VFS proxy HTTP handler
type Handler struct {
	VFS  *vfs.VFS
	mapping *mapping
	client  *http.Client

	stripQuery  bool
	stripDomain bool
	shardLevel  int
}

// NewHandler creates a new Handler
func NewHandler(opt Options) (*Handler, error) {
	ctx := context.Background()

	cacheDir := opt.CacheDir
	if cacheDir == "" {
		cacheDir = filepath.Join(os.TempDir(), "rclone_vfs_cache")
	}

	// Build VFS options
	vfsOpt := &vfscommon.Options{
		CacheDir: cacheDir,
	}

	if opt.CacheMaxAge != "" {
		d, err := time.ParseDuration(opt.CacheMaxAge)
		if err == nil {
			vfsOpt.CacheMaxAge = d
		} else {
			return nil, fmt.Errorf("invalid cache-max-age: %w", err)
		}
	}
	if opt.CacheMaxSize != "" {
		s, err := parseSize(opt.CacheMaxSize)
		if err == nil {
			vfsOpt.CacheMaxSize = s
		}
	}
	if opt.CacheChunkSize != "" {
		s, err := parseSize(opt.CacheChunkSize)
		if err == nil {
			vfsOpt.ChunkSize = s
		}
	}
	vfsOpt.ChunkStreams = opt.CacheChunkStreams

	switch strings.ToLower(opt.CacheMode) {
	case "off":
		vfsOpt.CacheMode = vfscommon.CacheModeOff
	case "minimal":
		vfsOpt.CacheMode = vfscommon.CacheModeMinimal
	case "writes":
		vfsOpt.CacheMode = vfscommon.CacheModeWrites
	case "full":
		vfsOpt.CacheMode = vfscommon.CacheModeFull
	default:
		vfsOpt.CacheMode = vfscommon.CacheModeMinimal
	}

	vfsOpt.Init()

	vfsInstance, err := vfs.New(ctx, vfsOpt)
	if err != nil {
		return nil, fmt.Errorf("failed to create VFS: %w", err)
	}

	return &Handler{
		VFS:         vfsInstance,
		mapping:     newMapping(),
		client:      &http.Client{Timeout: 30 * time.Second},
		stripQuery:  opt.StripQuery,
		stripDomain: opt.StripDomain,
		shardLevel:  opt.ShardLevel,
	}, nil
}

// Shutdown shuts down the handler
func (h *Handler) Shutdown() {
	h.VFS.Close()
}

// hashCachePath computes a cache path from a URL
func (h *Handler) hashCachePath(targetURL string) string {
	keyURL := targetURL
	if h.stripQuery {
		if idx := strings.Index(keyURL, "?"); idx >= 0 {
			keyURL = keyURL[:idx]
		}
	}
	if h.stripDomain {
		if idx := strings.Index(keyURL, "://"); idx >= 0 {
			rest := keyURL[idx+3:]
			if slashIdx := strings.Index(rest, "/"); slashIdx >= 0 {
				rest = rest[slashIdx:]
			} else {
				rest = "/"
			}
			keyURL = rest
		}
	}

	hash := fmt.Sprintf("%x", md5.Sum([]byte(keyURL)))

	if h.shardLevel > 0 {
		sharded := ""
		for i := 0; i < h.shardLevel && i*2 < len(hash); i++ {
			sharded += string(hash[i*2]) + string(hash[i*2+1]) + "/"
		}
		return sharded + hash
	}
	return hash
}

// Serve handles an HTTP request for the given targetURL.
//
// It opens the file through the VFS cache, associating it with the
// upstream URL so that the VFS can fetch the file on cache misses.
func (h *Handler) Serve(w http.ResponseWriter, r *http.Request, targetURL string) {
	if targetURL == "" {
		http.Error(w, "Target URL is required", http.StatusBadRequest)
		return
	}

	cachePath := h.hashCachePath(targetURL)

	// Build upstream headers by combining request headers (minus per-request ones)
	upstreamHeaders := make(http.Header)
	for k, vv := range r.Header {
		switch k {
		case "Range", "If-Range", "If-Modified-Since", "If-Unmodified-Since", "If-None-Match", "If-Match":
			continue
		}
		for _, v := range vv {
			upstreamHeaders.Add(k, v)
		}
	}

	h.mapping.put(targetURL, cachePath, upstreamHeaders)

	// Create an httpFile to associate with this cache path
	httpFile := h.newHTTPFile(cachePath)

	// Open through VFS cache with the httpFile
	fh, err := h.VFS.OpenCached(cachePath, httpFile)
	if err != nil {
		http.Error(w, "Failed to open file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer fh.Close()

	// Get file info
	info, err := fh.Stat()
	if err != nil {
		http.Error(w, "Failed to stat file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	size := info.Size()
	modTime := info.ModTime()

	// Set response headers
	if size >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	mimeType := mime.TypeByExtension(path.Ext(cachePath))
	if mimeType != "" {
		w.Header().Set("Content-Type", mimeType)
	}
	if !modTime.IsZero() {
		w.Header().Set("Last-Modified", modTime.UTC().Format(http.TimeFormat))
	}

	// Serve content (handles Range requests via http.ServeContent)
	if size >= 0 {
		http.ServeContent(w, r, cachePath, modTime, fh)
	} else {
		if r.Header.Get("Range") != "" {
			http.Error(w, "Cannot use Range on files of unknown length", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		io.Copy(w, fh)
	}
}

// newHTTPFile creates an httpFile for the given cache path, looking up
// the upstream URL and headers from the mapping.
func (h *Handler) newHTTPFile(cachePath string) *remoteFile {
	entry, ok := h.mapping.get(cachePath)
	if !ok {
		return &remoteFile{size: -1}
	}

	// First do a HEAD request to get metadata
	size := int64(-1)
	modTime := time.Time{}

	req, err := http.NewRequest("HEAD", entry.url, nil)
	if err == nil {
		for k, vv := range entry.headers {
			for _, v := range vv {
				req.Header.Add(k, v)
			}
		}
		resp, err := h.client.Do(req)
		if err == nil {
			if resp.StatusCode == http.StatusOK {
				if cl := resp.Header.Get("Content-Length"); cl != "" {
					if parsed, err := strconv.ParseInt(cl, 10, 64); err == nil {
						size = parsed
					}
				}
				if lm := resp.Header.Get("Last-Modified"); lm != "" {
					if parsed, err := http.ParseTime(lm); err == nil {
						modTime = parsed
					}
				}
			}
			resp.Body.Close()
		}
	}

	return newHTTPFile(entry.url, entry.headers, size, modTime, h.client)
}

// parseSize parses a size string like "100M", "1G", etc.
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}

	multiplier := int64(1)
	switch s[len(s)-1] {
	case 'k', 'K':
		multiplier = 1 << 10
		s = s[:len(s)-1]
	case 'M', 'm':
		multiplier = 1 << 20
		s = s[:len(s)-1]
	case 'G', 'g':
		multiplier = 1 << 30
		s = s[:len(s)-1]
	case 'T', 't':
		multiplier = 1 << 40
		s = s[:len(s)-1]
	}

	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size: %w", err)
	}
	return v * multiplier, nil
}
