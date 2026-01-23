package main

import (
	"context"
	"encoding/base64"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/tgdrive/vfscache-proxy/pkg/vfsproxy"

	"github.com/rclone/rclone/fs/config"
	"github.com/spf13/pflag"
)

var (
	port = pflag.String("port", "8080", "Port to listen on")

	// Use DefaultOptions to get all defaults
	defaults = vfsproxy.DefaultOptions()

	// Core options
	fsName            = pflag.String("fs-name", defaults.FsName, "The name of the VFS file system")
	cacheDir          = pflag.String("cache-dir", defaults.CacheDir, "Cache directory")
	cacheMaxAge       = pflag.String("max-age", defaults.CacheMaxAge, "Max age of files in cache")
	cacheMaxSize      = pflag.String("max-size", defaults.CacheMaxSize, "Max total size of objects in cache")
	cacheChunkSize    = pflag.String("chunk-size", defaults.CacheChunkSize, "Default Chunk size of read request")
	cacheChunkStreams = pflag.Int("chunk-streams", defaults.CacheChunkStreams, "The number of parallel streams to read at once")
	stripQuery        = pflag.Bool("strip-query", defaults.StripQuery, "Strip query parameters from URL for caching")
	stripDomain       = pflag.Bool("strip-domain", defaults.StripDomain, "Strip domain and protocol from URL for caching")
	shardLevel        = pflag.Int("shard-level", defaults.ShardLevel, "Number of shard levels")

	// Additional VFS flags
	cacheMode         = pflag.String("cache-mode", defaults.CacheMode, "VFS cache mode (off, minimal, writes, full)")
	writeWait         = pflag.String("write-wait", defaults.WriteWait, "VFS write wait time")
	readWait          = pflag.String("read-wait", defaults.ReadWait, "VFS read wait time")
	writeBack         = pflag.String("write-back", defaults.WriteBack, "VFS write back time")
	dirCacheTime      = pflag.String("dir-cache-time", defaults.DirCacheTime, "VFS directory cache time")
	fastFingerprint   = pflag.Bool("fast-fingerprint", defaults.FastFingerprint, "Use fast fingerprinting")
	cacheMinFreeSpace = pflag.String("min-free-space", defaults.CacheMinFreeSpace, "VFS minimum free space in cache")
	caseInsensitive   = pflag.Bool("case-insensitive", defaults.CaseInsensitive, "VFS case insensitive")
	readOnly          = pflag.Bool("read-only", defaults.ReadOnly, "VFS read only")
	noModTime         = pflag.Bool("no-modtime", defaults.NoModTime, "VFS no modtime")
	noChecksum        = pflag.Bool("no-checksum", defaults.NoChecksum, "VFS no checksum")
	noSeek            = pflag.Bool("no-seek", defaults.NoSeek, "VFS no seek")
	dirPerms          = pflag.String("dir-perms", defaults.DirPerms, "VFS directory permissions")
	filePerms         = pflag.String("file-perms", defaults.FilePerms, "VFS file permissions")
)

func main() {
	pflag.Parse()

	opt := vfsproxy.Options{
		FsName:            *fsName,
		CacheDir:          *cacheDir,
		CacheMaxAge:       *cacheMaxAge,
		CacheMaxSize:      *cacheMaxSize,
		CacheChunkSize:    *cacheChunkSize,
		CacheChunkStreams: *cacheChunkStreams,
		StripQuery:        *stripQuery,
		StripDomain:       *stripDomain,
		ShardLevel:        *shardLevel,

		// Map additional VFS flags
		CacheMode:         *cacheMode,
		WriteWait:         *writeWait,
		ReadWait:          *readWait,
		WriteBack:         *writeBack,
		DirCacheTime:      *dirCacheTime,
		FastFingerprint:   *fastFingerprint,
		CacheMinFreeSpace: *cacheMinFreeSpace,
		CaseInsensitive:   *caseInsensitive,
		ReadOnly:          *readOnly,
		NoModTime:         *noModTime,
		NoChecksum:        *noChecksum,
		NoSeek:            *noSeek,
		DirPerms:          *dirPerms,
		FilePerms:         *filePerms,
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
		log.Printf("VFS Cache Dir: %s", config.GetCacheDir())
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
