package link

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/fs/fserrors"
	"github.com/rclone/rclone/fs/fshttp"
	"github.com/rclone/rclone/fs/hash"
	"github.com/rclone/rclone/lib/pacer"
)

var retryErrorCodes = []int{
	429, // Too Many Requests
	500, // Internal Server Error
	502, // Bad Gateway
	503, // Service Unavailable
	504, // Gateway Timeout
	509, // Bandwidth Limit Exceeded

}

func shouldRetry(ctx context.Context, resp *http.Response, err error) (bool, error) {
	if fserrors.ContextError(ctx, &err) {
		return false, err
	}
	return fserrors.ShouldRetry(err) || fserrors.ShouldRetryHTTP(resp, retryErrorCodes), err
}

var (
	errorReadOnly = errors.New("link: read only")
	urlMap        sync.Map
)

type entry struct {
	url     string
	header  http.Header
	size    int64     // pre-provided size (0 means fetch needed)
	modTime time.Time // pre-provided modTime
}

// Register stores a URL mapping. Metadata will be fetched on first access.
// Returns true if this is a new entry, false if already registered.
func Register(remote, url string, header http.Header) bool {
	_, loaded := urlMap.LoadOrStore(remote, &entry{url: url, header: header})
	return !loaded
}

// RegisterWithSize stores a URL mapping with known size to skip metadata fetch.
// Returns true if this is a new entry, false if already registered.
func RegisterWithSize(remote, url string, header http.Header, size int64) bool {
	_, loaded := urlMap.LoadOrStore(remote, &entry{url: url, header: header, size: size, modTime: time.Now()})
	return !loaded
}

func Load(remote string) (string, bool) {
	val, ok := urlMap.Load(remote)
	if !ok {
		return "", false
	}
	return val.(*entry).url, true
}

func init() {
	fs.Register(&fs.RegInfo{
		Name:        "link",
		Description: "Multi-Link Dynamic Backend with Hash Sharding",
		NewFs:       NewFs,
	})
}

type Fs struct {
	name        string
	root        string
	features    *fs.Features
	stripQuery  bool
	stripDomain bool
	shardLevel  int
	pacer       *fs.Pacer
}

func NewFs(ctx context.Context, name, root string, m configmap.Mapper) (fs.Fs, error) {
	f := &Fs{
		name:  name,
		root:  root,
		pacer: fs.NewPacer(ctx, pacer.NewDefault()),
	}

	if val, ok := m.Get("strip_query"); ok && val == "true" {
		f.stripQuery = true
	}

	if val, ok := m.Get("strip_domain"); ok && val == "true" {
		f.stripDomain = true
	}

	if val, ok := m.Get("shard_level"); ok && val != "" {
		if level, err := strconv.Atoi(val); err == nil {
			f.shardLevel = level
		}
	} else {
		f.shardLevel = 1
	}

	f.features = (&fs.Features{
		ReadMetadata: true,
	}).Fill(ctx, f)

	return f, nil
}

func (f *Fs) Name() string { return f.name }

func (f *Fs) Root() string { return f.root }

func (f *Fs) String() string { return "link:" }

func (f *Fs) Precision() time.Duration { return time.Second }

func (f *Fs) Hashes() hash.Set { return hash.Set(hash.None) }

func (f *Fs) Features() *fs.Features { return f.features }

func (f *Fs) List(ctx context.Context, dir string) (fs.DirEntries, error) {

	var entries fs.DirEntries

	cleanDir := path.Clean(dir)

	if cleanDir == "." {
		cleanDir = ""
	}

	dirMap := make(map[string]struct{})

	urlMap.Range(func(key, value any) bool {

		remote := key.(string)

		sharded := ShardedPath(remote, f.shardLevel)

		objDir := path.Dir(sharded)

		if objDir == "." {
			objDir = ""
		}

		if objDir == cleanDir {
			obj, err := f.NewObject(ctx, sharded)
			if err == nil {
				entries = append(entries, obj)
			}
			return true

		}
		var relativePath string

		if cleanDir == "" {
			relativePath = sharded
		} else if strings.HasPrefix(sharded, cleanDir+"/") {
			relativePath = sharded[len(cleanDir)+1:]
		} else {
			return true
		}

		parts := strings.Split(relativePath, "/")

		if len(parts) > 1 {
			subDirName := parts[0]
			fullDirPath := subDirName
			if cleanDir != "" {
				fullDirPath = path.Join(cleanDir, subDirName)
			}
			if _, exists := dirMap[fullDirPath]; !exists {
				dirMap[fullDirPath] = struct{}{}
				entries = append(entries, fs.NewDir(fullDirPath, time.Now()))
			}
		}
		return true
	})

	return entries, nil
}

func (f *Fs) NewObject(ctx context.Context, remote string) (fs.Object, error) {
	originalRemote := path.Base(remote)
	
	val, ok := urlMap.Load(originalRemote)

	if !ok {
		return nil, fs.ErrorObjectNotFound
	}

	e := val.(*entry)

	// If size is already known, skip metadata fetch entirely
	if e.size > 0 {
		return &Object{
			fs:      f,
			remote:  remote,
			url:     e.url,
			size:    e.size,
			modTime: e.modTime,
		}, nil
	}

	// Fetch metadata (slow path)
	modTime, size, err := f.fetchMetadata(ctx, e.url, e.header, originalRemote)
	if err != nil {
		log.Printf("[ERROR] Metadata fetch failed for %s: %v", originalRemote, err)
		return nil, err
	}

	return &Object{
		fs:      f,
		remote:  remote,
		url:     e.url,
		size:    size,
		modTime: modTime,
	}, nil
}

func (f *Fs) fetchMetadata(ctx context.Context, urlStr string, header http.Header, remote string) (time.Time, int64, error) {
	client := fshttp.NewClient(ctx)

	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		return time.Time{}, 0, err
	}

	for k, vv := range header {
		for _, v := range vv {
			req.Header.Set(k, v)
		}
	}

	// Use GET with Range header to fetch only 1 byte + headers
	// Many backends (like teldrive) don't support HEAD requests
	req.Header.Set("Range", "bytes=0-0")
	
	var resp *http.Response
	err = f.pacer.Call(func() (bool, error) {
		resp, err = client.Do(req)
		return shouldRetry(ctx, resp, err)
	})
	if err != nil {
		return time.Time{}, 0, err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return time.Time{}, 0, fmt.Errorf("metadata fetch failed: status %d", resp.StatusCode)
	}

	size := resp.ContentLength
	if resp.StatusCode == http.StatusPartialContent {
		if contentRange := resp.Header.Get("Content-Range"); contentRange != "" {
			var rangeStart, end, total int64
			_, err := fmt.Sscanf(contentRange, "bytes %d-%d/%d", &rangeStart, &end, &total)
			if err == nil {
				size = total
			}
		}
	}

	modTime := time.Now()
	if lastMod := resp.Header.Get("Last-Modified"); lastMod != "" {
		if t, err := http.ParseTime(lastMod); err == nil {
			modTime = t
		}
	}

	if size < 0 {
		return time.Time{}, 0, fmt.Errorf("metadata fetch failed: unknown file size")
	}

	return modTime, size, nil
}

func (f *Fs) Put(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) (fs.Object, error) {
	return nil, errorReadOnly
}

func (f *Fs) Mkdir(ctx context.Context, dir string) error { return nil }
func (f *Fs) Rmdir(ctx context.Context, dir string) error { return errorReadOnly }

type Object struct {
	fs       *Fs
	remote   string
	url      string
	size     int64
	modTime  time.Time
	mimeType string
}

func (o *Object) Fs() fs.Info    { return o.fs }
func (o *Object) String() string { return o.remote }
func (o *Object) Remote() string { return o.remote }
func (o *Object) Hash(ctx context.Context, r hash.Type) (string, error) {
	return "", hash.ErrUnsupported
}
func (o *Object) Size() int64                                             { return o.size }
func (o *Object) ModTime(ctx context.Context) time.Time                   { return o.modTime }
func (o *Object) MimeType(ctx context.Context) string                     { return o.mimeType }
func (o *Object) Storable() bool                                          { return true }
func (o *Object) SetModTime(ctx context.Context, modTime time.Time) error { return errorReadOnly }
func (o *Object) Remove(ctx context.Context) error                        { return errorReadOnly }
func (o *Object) Update(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) error {
	return errorReadOnly
}

func (o *Object) Open(ctx context.Context, options ...fs.OpenOption) (io.ReadCloser, error) {
	client := fshttp.NewClient(ctx)
	req, err := http.NewRequestWithContext(ctx, "GET", o.url, nil)
	if err != nil {
		return nil, err
	}

	// Apply stored headers from urlMap dynamically
	originalRemote := path.Base(o.remote)
	if val, ok := urlMap.Load(originalRemote); ok {
		e := val.(*entry)
		if e.header != nil {
			for k, vv := range e.header {
				for _, v := range vv {
					req.Header.Set(k, v)
				}
			}
		}
	}

	// Apply OpenOptions (can override stored headers)
	for k, v := range fs.OpenOptionHeaders(options) {
		req.Header.Set(k, v)
	}

	var resp *http.Response
	err = o.fs.pacer.Call(func() (bool, error) {
		resp, err = client.Do(req)
		return shouldRetry(ctx, resp, err)
	})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		resp.Body.Close()
		return nil, fmt.Errorf("GET failed: %s (status %d)", resp.Status, resp.StatusCode)
	}
	return resp.Body, nil
}

var (
	_ fs.Fs     = &Fs{}
	_ fs.Object = &Object{}
)
