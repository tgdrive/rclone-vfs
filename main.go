package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/tgdrive/varc/pkg/vfsproxy"

	"github.com/spf13/pflag"
)

var (
	port = pflag.String("port", "8080", "Port to listen on")
	cacheDir = pflag.String("cache-dir", filepath.Join(os.TempDir(), "varc_cache"), "Cache directory")
	cacheMode = pflag.String("cache-mode", "minimal", "VFS cache mode (off, minimal, writes, full)")
	chunkSize = pflag.String("chunk-size", "", "Chunk size for reading (e.g., 4M)")
	chunkStreams = pflag.Int("chunk-streams", 2, "Number of parallel chunk streams")
	stripQuery = pflag.Bool("strip-query", false, "Strip query parameters from URL for caching")
	stripDomain = pflag.Bool("strip-domain", false, "Strip domain from URL for caching")
	shardLevel = pflag.Int("shard-level", 1, "Number of shard levels for cache paths")
)

func main() {
	pflag.Parse()

	opt := vfsproxy.Options{
		CacheDir:          *cacheDir,
		CacheMode:         *cacheMode,
		CacheChunkSize:    *chunkSize,
		CacheChunkStreams: *chunkStreams,
		StripQuery:        *stripQuery,
		StripDomain:       *stripDomain,
		ShardLevel:        *shardLevel,
	}

	handler, err := vfsproxy.NewHandler(opt)
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()

	mainHandler := func(w http.ResponseWriter, r *http.Request) {
		targetURL := r.URL.Query().Get("url")

		// Check for Base64 URL in path
		if targetURL == "" && strings.HasPrefix(r.URL.Path, "/stream/") {
			encodedURL := strings.TrimPrefix(r.URL.Path, "/stream/")
			if decoded, err := base64.RawURLEncoding.DecodeString(encodedURL); err == nil {
				targetURL = string(decoded)
			} else if decoded, err := base64.URLEncoding.DecodeString(encodedURL); err == nil {
				targetURL = string(decoded)
			}
		}

		if targetURL == "" {
			http.Error(w, "Missing 'url' parameter or base64 path", http.StatusBadRequest)
			return
		}

		handler.Serve(w, r, targetURL)
	}

	mux.HandleFunc("/stream", mainHandler)
	mux.HandleFunc("/stream/", mainHandler)

	// Health check endpoint
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status":"ok","cache_dir":"%s"}`, handler.VFS.Opt.CacheDir)
	})

	srv := &http.Server{
		Addr:    ":" + *port,
		Handler: mux,
	}

	// Channel to listen for signals
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("VFS Proxy listening on :%s", *port)
		log.Printf("VFS Cache Mode: %v", handler.VFS.Opt.CacheMode)
		log.Printf("VFS Cache Dir: %s", handler.VFS.Opt.CacheDir)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %s\n", err)
		}
	}()

	<-stop

	log.Println("Shutting down gracefully...")

	// Create a context with timeout for the shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Shutting down VFS...")
	handler.Shutdown()

	log.Println("Exit")
}
