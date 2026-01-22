package main

import (
	"context"
	"crypto/md5"
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

	"vfs/backend/link"

	_ "github.com/rclone/rclone/backend/local"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/fs/config/configstruct"
	"github.com/rclone/rclone/vfs"
	"github.com/rclone/rclone/vfs/vfscommon"
	"github.com/spf13/pflag"
)

var (
	port              = pflag.String("port", "8080", "Port to listen on")
	cacheChunkSize    = pflag.String("chunk-size", "64M", "Default Chunk size of read request")
	cacheMaxAge       = pflag.String("max-age", "1h", "Max age of files in cache")
	cacheMaxSize      = pflag.String("max-size", "off", "Max total size of objects in cache")
	cacheDir          = pflag.String("cache-dir", "", "Cache directory")
	cacheChunkStreams = pflag.Int("chunk-streams", 2, "The number of parallel streams to read at once")
	stripQuery        = pflag.Bool("strip-query", false, "Strip query parameters from URL for caching")
)

var globalVFS *vfs.VFS

func initVFS() error {
	pflag.Parse()

	regInfo, _ := fs.Find("link")
	if regInfo == nil {
		return fmt.Errorf("could not find link backend")
	}

	// Backend options
	backendOpt := configmap.Simple{
		"strip_query": fmt.Sprintf("%v", *stripQuery),
	}

	f, err := regInfo.NewFs(context.Background(), "link-vfs", "", backendOpt)
	if err != nil {
		return err
	}

	// Map our selected flags to Rclone VFS options
	m := configmap.Simple{
		"vfs_cache_mode":         "full",
		"vfs_cache_max_age":      *cacheMaxAge,
		"vfs_cache_max_size":     *cacheMaxSize,
		"vfs-read-chunk-size":    *cacheChunkSize,
		"dir_cache_time":         "0s",
		"vfs_read_chunk_streams": fmt.Sprintf("%d", *cacheChunkStreams),
	}

	opt := vfscommon.Opt
	if err := configstruct.Set(m, &opt); err != nil {
		return fmt.Errorf("failed to parse VFS options: %w", err)
	}

	// Setup Cache Directory
	actualCacheDir := *cacheDir
	if actualCacheDir == "" {
		actualCacheDir = filepath.Join(os.TempDir(), "rclone_vfs_cache")
	}
	_ = config.SetCacheDir(actualCacheDir)

	globalVFS = vfs.New(f, &opt)
	return nil
}

func streamHandler(w http.ResponseWriter, r *http.Request) {
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

	hashBytes := md5.Sum([]byte(targetURL))
	fileHash := fmt.Sprintf("%x", hashBytes)

	log.Printf("Request: %s -> %s", targetURL, fileHash)
	link.Register(fileHash, targetURL)

	handle, err := globalVFS.OpenFile(fileHash, os.O_RDONLY, 0)
	if err != nil {
		log.Printf("VFS error: %v", err)
		http.Error(w, "File error", http.StatusNotFound)
		return
	}
	defer handle.Close()

	info, err := handle.Stat()
	if err != nil {
		http.Error(w, "Stat error", http.StatusInternalServerError)
		return
	}

	http.ServeContent(w, r, fileHash, info.ModTime(), handle)
}

func main() {
	if err := initVFS(); err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/stream", streamHandler)
	mux.HandleFunc("/stream/", streamHandler)

	srv := &http.Server{
		Addr:    ":" + *port,
		Handler: mux,
	}

	// Channel to listen for signals
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("VFS Proxy listening on :%s", *port)
		log.Printf("VFS Cache Mode: %v", globalVFS.Opt.CacheMode)
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
	globalVFS.Shutdown()

	log.Println("Exit")
}
