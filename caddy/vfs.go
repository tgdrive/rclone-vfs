package vfs

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"runtime/debug"
	"strconv"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"go.uber.org/zap"

	"github.com/tgdrive/vfscache-proxy/pkg/vfsproxy"
)

func init() {
	caddy.RegisterModule(VFS{})
	httpcaddyfile.RegisterHandlerDirective("vfs", parseCaddyfile)

	// Register directive order so vfs runs before reverse_proxy
	httpcaddyfile.RegisterDirectiveOrder("vfs", httpcaddyfile.Before, "reverse_proxy")
}

// VFS implements a Caddy HTTP handler that proxies requests to a VFS backend.
type VFS struct {
	// Upstream is the base URL to proxy requests to (required).
	Upstream string `json:"upstream,omitempty"`

	// Passthrough controls whether to call the next handler on 404.
	// If true, when a file is not found, the next handler in the chain is called.
	// If false (default), a 404 response is returned immediately.
	Passthrough bool `json:"passthrough,omitempty"`

	FsName            string `json:"fs_name,omitempty"`
	CacheDir          string `json:"cache_dir,omitempty"`
	CacheMaxAge       string `json:"cache_max_age,omitempty"`
	CacheMaxSize      string `json:"cache_max_size,omitempty"`
	CacheChunkSize    string `json:"cache_chunk_size,omitempty"`
	CacheChunkStreams int    `json:"cache_chunk_streams,omitempty"`
	StripQuery        bool   `json:"strip_query,omitempty"`
	StripDomain       bool   `json:"strip_domain,omitempty"`
	ShardLevel        int    `json:"shard-level,omitempty"`

	// VFS Options
	CacheMode         string `json:"cache_mode,omitempty"`
	WriteWait         string `json:"write_wait,omitempty"`
	ReadWait          string `json:"read_wait,omitempty"`
	WriteBack         string `json:"write_back,omitempty"`
	DirCacheTime      string `json:"dir_cache_time,omitempty"`
	FastFingerprint   bool   `json:"fast_fingerprint,omitempty"`
	CacheMinFreeSpace string `json:"cache_min_free_space,omitempty"`
	CaseInsensitive   bool   `json:"case_insensitive,omitempty"`
	ReadOnly          bool   `json:"read_only,omitempty"`
	NoModTime         bool   `json:"no_modtime,omitempty"`
	NoChecksum        bool   `json:"no_checksum,omitempty"`
	NoSeek            bool   `json:"no_seek,omitempty"`
	DirPerms          string `json:"dir_perms,omitempty"`
	FilePerms         string `json:"file_perms,omitempty"`

	handler     *vfsproxy.Handler
	logger      *zap.Logger
	upstreamURL *url.URL
}

// CaddyModule returns the Caddy module information.
func (VFS) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.vfs",
		New: func() caddy.Module { return &VFS{} },
	}
}

// Provision sets up the VFS handler.
func (v *VFS) Provision(ctx caddy.Context) error {
	v.logger = ctx.Logger(v)

	// Parse upstream URL once during provisioning
	parsedURL, err := url.Parse(v.Upstream)
	if err != nil {
		return fmt.Errorf("invalid upstream URL: %w", err)
	}
	v.upstreamURL = parsedURL

	// Start with defaults and apply user overrides
	opt := vfsproxy.DefaultOptions()

	// Apply user-provided values (non-zero values override defaults)
	if v.FsName != "" {
		opt.FsName = v.FsName
	}
	if v.CacheDir != "" {
		opt.CacheDir = v.CacheDir
	}
	if v.CacheMaxAge != "" {
		opt.CacheMaxAge = v.CacheMaxAge
	}
	if v.CacheMaxSize != "" {
		opt.CacheMaxSize = v.CacheMaxSize
	}
	if v.CacheChunkSize != "" {
		opt.CacheChunkSize = v.CacheChunkSize
	}
	if v.CacheChunkStreams != 0 {
		opt.CacheChunkStreams = v.CacheChunkStreams
	}
	if v.CacheMode != "" {
		opt.CacheMode = v.CacheMode
	}
	if v.WriteWait != "" {
		opt.WriteWait = v.WriteWait
	}
	if v.ReadWait != "" {
		opt.ReadWait = v.ReadWait
	}
	if v.WriteBack != "" {
		opt.WriteBack = v.WriteBack
	}
	if v.DirCacheTime != "" {
		opt.DirCacheTime = v.DirCacheTime
	}
	if v.CacheMinFreeSpace != "" {
		opt.CacheMinFreeSpace = v.CacheMinFreeSpace
	}
	if v.DirPerms != "" {
		opt.DirPerms = v.DirPerms
	}
	if v.FilePerms != "" {
		opt.FilePerms = v.FilePerms
	}

	// Boolean flags (always apply as they have meaning when true)
	opt.StripQuery = v.StripQuery
	opt.StripDomain = v.StripDomain
	opt.FastFingerprint = v.FastFingerprint
	opt.CaseInsensitive = v.CaseInsensitive
	opt.ReadOnly = v.ReadOnly
	opt.NoModTime = v.NoModTime
	opt.NoChecksum = v.NoChecksum
	opt.NoSeek = v.NoSeek
	opt.ShardLevel = v.ShardLevel

	handler, err := vfsproxy.NewHandler(opt)
	if err != nil {
		return fmt.Errorf("failed to create VFS handler: %w", err)
	}

	v.handler = handler
	v.logger.Info("VFS handler provisioned",
		zap.String("upstream", v.Upstream),
		zap.String("cache_mode", opt.CacheMode),
		zap.String("cache_dir", opt.CacheDir),
	)
	return nil
}

// Validate ensures the configuration is valid.
func (v *VFS) Validate() error {
	if v.Upstream == "" {
		return fmt.Errorf("upstream URL is required")
	}

	// Validate upstream URL format
	if v.upstreamURL == nil {
		return fmt.Errorf("upstream URL was not parsed")
	}
	if v.upstreamURL.Scheme != "http" && v.upstreamURL.Scheme != "https" {
		return fmt.Errorf("upstream URL must use http or https scheme, got %q", v.upstreamURL.Scheme)
	}

	// Validate cache_mode if provided
	if v.CacheMode != "" {
		validModes := map[string]bool{"off": true, "minimal": true, "writes": true, "full": true}
		if !validModes[v.CacheMode] {
			return fmt.Errorf("invalid cache_mode %q: must be one of off, minimal, writes, full", v.CacheMode)
		}
	}

	// Validate chunk_streams if provided
	if v.CacheChunkStreams < 0 {
		return fmt.Errorf("chunk_streams must be non-negative, got %d", v.CacheChunkStreams)
	}

	return nil
}

// Cleanup cleans up the VFS resources.
func (v *VFS) Cleanup() error {
	if v.handler != nil {
		v.logger.Info("Shutting down VFS handler")
		v.handler.Shutdown()
	}
	return nil
}

// ServeHTTP serves the HTTP request.
func (v *VFS) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	// Build full URL using url.JoinPath for proper path handling
	fullURL := v.upstreamURL.JoinPath(r.URL.Path).String()
	if r.URL.RawQuery != "" {
		fullURL += "?" + r.URL.RawQuery
	}

	// Wrap in panic recovery
	defer func() {
		if rec := recover(); rec != nil {
			v.logger.Error("panic in ServeHTTP",
				zap.Any("panic", rec),
				zap.String("url", r.URL.String()),
				zap.String("method", r.Method),
				zap.String("stack", string(debug.Stack())),
			)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	}()

	// If passthrough is enabled, use Caddy's ResponseRecorder to buffer 404 responses
	if v.Passthrough && next != nil {
		buf := new(bytes.Buffer)
		shouldBuffer := func(status int, header http.Header) bool {
			return status == http.StatusNotFound
		}
		rec := caddyhttp.NewResponseRecorder(w, buf, shouldBuffer)
		v.handler.Serve(rec, r, fullURL)
		if rec.Buffered() {
			return next.ServeHTTP(w, r)
		}
		return nil
	}

	v.handler.Serve(w, r, fullURL)
	return nil
}

// parseCaddyfile parses the Caddyfile configuration.
//
// Syntax:
//
//	vfs <upstream> {
//	    passthrough
//	    cache_dir <path>
//	    max_age <duration>
//	    max_size <size>
//	    chunk_size <size>
//	    chunk_streams <number>
//	    strip_query
//	    strip_domain
//	    cache_mode <off|minimal|writes|full>
//	    ...
//	}
func parseCaddyfile(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	var v VFS
	err := v.UnmarshalCaddyfile(h.Dispenser)
	return &v, err
}

// UnmarshalCaddyfile sets up the handler from Caddyfile tokens.
func (v *VFS) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	// Helper for parsing string arguments
	parseString := func(target *string) error {
		if !d.NextArg() {
			return d.ArgErr()
		}
		*target = d.Val()
		return nil
	}

	for d.Next() {
		if d.NextArg() {
			v.Upstream = d.Val()
		}
		if v.Upstream == "" {
			return d.Err("missing upstream URL")
		}

		for d.NextBlock(0) {
			directive := d.Val()
			var err error

			switch directive {
			// String options
			case "fs_name":
				err = parseString(&v.FsName)
			case "cache_dir":
				err = parseString(&v.CacheDir)
			case "max_age":
				err = parseString(&v.CacheMaxAge)
			case "max_size":
				err = parseString(&v.CacheMaxSize)
			case "chunk_size":
				err = parseString(&v.CacheChunkSize)
			case "cache_mode":
				err = parseString(&v.CacheMode)
			case "write_wait":
				err = parseString(&v.WriteWait)
			case "read_wait":
				err = parseString(&v.ReadWait)
			case "write_back":
				err = parseString(&v.WriteBack)
			case "dir_cache_time":
				err = parseString(&v.DirCacheTime)
			case "min_free_space":
				err = parseString(&v.CacheMinFreeSpace)
			case "dir_perms":
				err = parseString(&v.DirPerms)
			case "file_perms":
				err = parseString(&v.FilePerms)

			// Integer options
			case "chunk_streams":
				if !d.NextArg() {
					return d.ArgErr()
				}
				streams, parseErr := strconv.Atoi(d.Val())
				if parseErr != nil {
					return d.Errf("invalid chunk_streams: %v", parseErr)
				}
				v.CacheChunkStreams = streams

			case "shard-level":
				if !d.NextArg() {
					return d.ArgErr()
				}
				level, parseErr := strconv.Atoi(d.Val())
				if parseErr != nil {
					return d.Errf("invalid shard-level: %v", parseErr)
				}
				v.ShardLevel = level

			// Boolean flags (no argument needed)
			case "passthrough":
				v.Passthrough = true
			case "strip_query":
				v.StripQuery = true
			case "strip_domain":
				v.StripDomain = true
			case "fast_fingerprint":
				v.FastFingerprint = true
			case "case_insensitive":
				v.CaseInsensitive = true
			case "read_only":
				v.ReadOnly = true
			case "no_modtime":
				v.NoModTime = true
			case "no_checksum":
				v.NoChecksum = true
			case "no_seek":
				v.NoSeek = true

			default:
				return d.Errf("unknown subdirective '%s'", directive)
			}

			if err != nil {
				return err
			}
		}
	}
	return nil
}

// Interface guards
var (
	_ caddy.Provisioner           = (*VFS)(nil)
	_ caddy.Validator             = (*VFS)(nil)
	_ caddy.CleanerUpper          = (*VFS)(nil)
	_ caddyhttp.MiddlewareHandler = (*VFS)(nil)
	_ caddyfile.Unmarshaler       = (*VFS)(nil)
)
