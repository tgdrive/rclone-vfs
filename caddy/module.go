package varc

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"runtime/debug"
	"strconv"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"go.uber.org/zap"

	"github.com/tgdrive/varc/pkg/proxy"
)

func init() {
	caddy.RegisterModule(Handler{})
	httpcaddyfile.RegisterHandlerDirective("vfs", parseCaddyfile)

	// Register directive order so vfs runs before reverse_proxy
	httpcaddyfile.RegisterDirectiveOrder("vfs", httpcaddyfile.Before, "reverse_proxy")
}

// Handler implements a Caddy HTTP handler that proxies requests through the varc cache.
type Handler struct {
	// Upstream is the base URL to proxy requests to (required).
	Upstream string `json:"upstream,omitempty"`

	// Passthrough controls whether to call the next handler on 404.
	// If true, when a file is not found, the next handler in the chain is called.
	// If false (default), a 404 response is returned immediately.
	Passthrough bool `json:"passthrough,omitempty"`

	proxy.Options

	handler     *proxy.Handler
	logger      *zap.Logger
	upstreamURL *url.URL
}

// CaddyModule returns the Caddy module information.
func (Handler) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.vfs",
		New: func() caddy.Module {
			return &Handler{
				Options: proxy.DefaultOptions(),
			}
		},
	}
}

// Provision sets up the handler.
func (h *Handler) Provision(ctx caddy.Context) error {
	h.logger = ctx.Logger(h)

	// Parse upstream URL once during provisioning
	parsedURL, err := url.Parse(h.Upstream)
	if err != nil {
		return fmt.Errorf("invalid upstream URL: %w", err)
	}
	h.upstreamURL = parsedURL

	handler, err := proxy.NewHandler(h.Options)
	if err != nil {
		return fmt.Errorf("failed to create cache handler: %w", err)
	}

	h.handler = handler
	h.logger.Info("cache handler provisioned",
		zap.String("upstream", h.Upstream),
		zap.String("cache_mode", h.CacheMode),
		zap.String("cache_dir", h.CacheDir),
	)
	return nil
}

// Validate ensures the configuration is valid.
func (h *Handler) Validate() error {
	if h.Upstream == "" {
		return fmt.Errorf("upstream URL is required")
	}

	// Validate upstream URL format
	if h.upstreamURL == nil {
		return fmt.Errorf("upstream URL was not parsed")
	}
	if h.upstreamURL.Scheme != "http" && h.upstreamURL.Scheme != "https" {
		return fmt.Errorf("upstream URL must use http or https scheme, got %q", h.upstreamURL.Scheme)
	}

	// Validate cache_mode if provided
	if h.CacheMode != "" {
		validModes := map[string]bool{"off": true, "minimal": true, "writes": true, "full": true}
		if !validModes[h.CacheMode] {
			return fmt.Errorf("invalid cache_mode %q: must be one of off, minimal, writes, full", h.CacheMode)
		}
	}

	// Validate chunk_streams if provided
	if h.CacheChunkStreams < 0 {
		return fmt.Errorf("chunk_streams must be non-negative, got %d", h.CacheChunkStreams)
	}

	return nil
}

// Cleanup cleans up the handler resources.
func (h *Handler) Cleanup() error {
	if h.handler != nil {
		h.logger.Info("Shutting down cache handler")
		h.handler.Shutdown()
	}
	return nil
}

// ServeHTTP serves the HTTP request through the cache proxy.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	// Build full URL using url.JoinPath for proper path handling
	fullURL := h.upstreamURL.JoinPath(r.URL.Path).String()
	if r.URL.RawQuery != "" {
		fullURL += "?" + r.URL.RawQuery
	}

	// Wrap in panic recovery
	defer func() {
		if rec := recover(); rec != nil {
			h.logger.Error("panic in ServeHTTP",
				zap.Any("panic", rec),
				zap.String("url", r.URL.String()),
				zap.String("method", r.Method),
				zap.String("stack", string(debug.Stack())),
			)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	}()

	// If passthrough is enabled, use Caddy's ResponseRecorder to buffer 404 responses
	if h.Passthrough && next != nil {
		buf := new(bytes.Buffer)
		shouldBuffer := func(status int, header http.Header) bool {
			return status == http.StatusNotFound
		}
		rec := caddyhttp.NewResponseRecorder(w, buf, shouldBuffer)
		h.handler.Serve(rec, r, fullURL)
		if rec.Buffered() {
			return next.ServeHTTP(w, r)
		}
		return nil
	}

	h.handler.Serve(w, r, fullURL)
	return nil
}

// parseCaddyfile parses the Caddyfile configuration.
func parseCaddyfile(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	v := &Handler{
		Options: proxy.DefaultOptions(),
	}
	err := v.UnmarshalCaddyfile(h.Dispenser)
	return v, err
}

// UnmarshalCaddyfile sets up the handler from Caddyfile tokens.
func (h *Handler) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	for d.Next() {
		if d.NextArg() {
			h.Upstream = d.Val()
		}
		if h.Upstream == "" {
			return d.Err("missing upstream URL")
		}

		for d.NextBlock(0) {
			directive := d.Val()

			switch directive {
			case "passthrough":
				h.Passthrough = true
				continue
			}

			// Try to match directive with Options tags
			found := false
			val := reflect.ValueOf(&h.Options).Elem()
			typ := val.Type()
			for i := 0; i < typ.NumField(); i++ {
				field := typ.Field(i)
				if field.Tag.Get("caddy") == directive {
					f := val.Field(i)
					switch f.Kind() {
					case reflect.Bool:
						f.SetBool(true)
					case reflect.String:
						if !d.NextArg() {
							return d.ArgErr()
						}
						f.SetString(d.Val())
					case reflect.Int:
						if !d.NextArg() {
							return d.ArgErr()
						}
						i, err := strconv.Atoi(d.Val())
						if err != nil {
							return d.Errf("invalid value for %s: %v", directive, err)
						}
						f.SetInt(int64(i))
					}
					found = true
					break
				}
			}

			if !found {
				return d.Errf("unknown subdirective '%s'", directive)
			}
		}
	}
	return nil
}

// Interface guards
var (
	_ caddy.Provisioner           = (*Handler)(nil)
	_ caddy.Validator             = (*Handler)(nil)
	_ caddy.CleanerUpper          = (*Handler)(nil)
	_ caddyhttp.MiddlewareHandler = (*Handler)(nil)
	_ caddyfile.Unmarshaler       = (*Handler)(nil)
)
