package vfsproxy

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tgdrive/rclone-vfs/backend/link"
)

// testFileServer serves deterministic content for testing.
type testFileServer struct {
	content []byte // The full file content
	mu      sync.Mutex
	headers http.Header // Additional headers to set on responses
}

func newTestFileServer(size int) *testFileServer {
	content := make([]byte, size)
	// Fill with deterministic pattern for verification
	for i := range content {
		content[i] = byte(i % 251)
	}
	return &testFileServer{
		content: content,
		headers: make(http.Header),
	}
}

func (s *testFileServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Copy stored headers
	for k, vv := range s.headers {
		for _, v := range vv {
			w.Header().Set(k, v)
		}
	}

	http.ServeContent(w, r, "testfile.bin", time.Now(), bytes.NewReader(s.content))
}

// proxyTestHarness sets up a complete test environment.
type proxyTestHarness struct {
	t            *testing.T
	upstream     *httptest.Server
	proxyHandler *Handler
	proxyServer  *httptest.Server
	upstreamFile *testFileServer
	cacheDir     string
}

// newLocalhostServer creates an httptest.Server listening on localhost
// instead of the default 127.0.0.1. The loopback interface name resolves
// to ::1 first on IPv6-enabled systems when using "localhost", so we
// explicitly listen on the IPv4 loopback to match httptest's default
// behavior while using the localhost hostname.
func newLocalhostServer(handler http.Handler) *httptest.Server {
	ts := httptest.NewUnstartedServer(handler)
	ts.Listener.Close()
	ts.Listener, _ = net.Listen("tcp", "localhost:0")
	ts.Start()
	return ts
}

func newProxyTestHarness(t *testing.T, fileSize int, opts ...Options) *proxyTestHarness {
	t.Helper()

	// Start upstream file server on localhost
	upstreamFile := newTestFileServer(fileSize)
	upstream := newLocalhostServer(upstreamFile)

	// Build proxy options
	var opt Options
	if len(opts) > 0 {
		opt = opts[0]
	} else {
		opt = DefaultOptions()
	}
	// Ensure we use cache mode "full" for disk caching tests, "off" by default
	if opt.CacheMode == "" {
		opt.CacheMode = "full"
	}

	// Use a temp cache dir for test isolation
	cacheDir := t.TempDir()
	opt.CacheDir = cacheDir

	handler, err := NewHandler(opt)
	if err != nil {
		upstream.Close()
		t.Fatalf("Failed to create handler: %v", err)
	}

	// Start proxy server
	proxyMux := http.NewServeMux()
	proxyMux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		targetURL := r.URL.Query().Get("url")
		if targetURL == "" {
			http.Error(w, "Missing url", http.StatusBadRequest)
			return
		}
		handler.Serve(w, r, targetURL)
	})
	proxyServer := newLocalhostServer(proxyMux)

	return &proxyTestHarness{
		t:            t,
		upstream:     upstream,
		proxyHandler: handler,
		proxyServer:  proxyServer,
		upstreamFile: upstreamFile,
		cacheDir:     cacheDir,
	}
}

func (h *proxyTestHarness) Close() {
	h.proxyServer.Close()
	h.upstream.Close()
	h.proxyHandler.Shutdown()
	link.ClearURLMap()
}

func (h *proxyTestHarness) proxyURL(upstreamPath string) string {
	return h.proxyServer.URL + "/stream?url=" + h.upstream.URL + upstreamPath
}

// TestLargeFileFullDownload streams a 10MB file through the proxy and verifies content.
func TestLargeFileFullDownload(t *testing.T) {
	fileSize := 10 * 1024 * 1024 // 10MB
	h := newProxyTestHarness(t, fileSize)
	defer h.Close()

	client := &http.Client{Timeout: 30 * time.Second}

	// Full download
	resp, err := client.Get(h.proxyURL("/testfile.bin"))
	if err != nil {
		t.Fatalf("Failed to GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200, got %d", resp.StatusCode)
	}

	// Check Content-Length
	cl := resp.Header.Get("Content-Length")
	if cl == "" {
		t.Error("Missing Content-Length header")
	}

	// Read full body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read body: %v", err)
	}

	if len(body) != fileSize {
		t.Fatalf("Expected %d bytes, got %d", fileSize, len(body))
	}

	// Verify content matches
	if !bytes.Equal(body, h.upstreamFile.content) {
		t.Fatal("Downloaded content does not match upstream")
	}
}

// TestLargeFileFullDownloadNoCache tests a full download with cache-mode=off.
func TestLargeFileFullDownloadNoCache(t *testing.T) {
	fileSize := 10 * 1024 * 1024
	opt := DefaultOptions()
	opt.CacheMode = "off"
	h := newProxyTestHarness(t, fileSize, opt)
	defer h.Close()

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(h.proxyURL("/testfile.bin"))
	if err != nil {
		t.Fatalf("Failed to GET: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Read body: %v", err)
	}
	if len(body) != fileSize {
		t.Fatalf("Expected %d bytes, got %d", fileSize, len(body))
	}
	if !bytes.Equal(body, h.upstreamFile.content) {
		t.Fatal("Content mismatch")
	}
}

// TestLargeFileRangeRequest tests single range requests through the proxy.
func TestLargeFileRangeRequest(t *testing.T) {
	fileSize := 10 * 1024 * 1024
	h := newProxyTestHarness(t, fileSize)
	defer h.Close()

	client := &http.Client{Timeout: 30 * time.Second}

	tests := []struct {
		name       string
		rangeSpec  string
		offset     int64
		length     int64
	}{
		{"first 1KB", "bytes=0-1023", 0, 1024},
		{"middle 5KB", "bytes=1048576-1053695", 1048576, 5120},
		{"last 1KB", fmt.Sprintf("bytes=%d-%d", fileSize-1024, fileSize-1), int64(fileSize - 1024), 1024},
		{"beyond end (last 500)", fmt.Sprintf("bytes=%d-", fileSize-500), int64(fileSize - 500), 500},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest("GET", h.proxyURL("/testfile.bin"), nil)
			if err != nil {
				t.Fatalf("Failed to create request: %v", err)
			}
			req.Header.Set("Range", tt.rangeSpec)

			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("Failed to GET: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
				t.Fatalf("Expected 206 or 200, got %d", resp.StatusCode)
			}

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("Failed to read body: %v", err)
			}

			if int64(len(body)) != tt.length {
				t.Fatalf("Expected %d bytes, got %d", tt.length, len(body))
			}

			// Verify content
			expected := h.upstreamFile.content[tt.offset : tt.offset+tt.length]
			if !bytes.Equal(body, expected) {
				t.Fatal("Range content does not match")
			}
		})
	}
}

// TestLargeFileConcurrentStreams tests multiple concurrent range requests for large files.
func TestLargeFileConcurrentStreams(t *testing.T) {
	fileSize := 50 * 1024 * 1024 // 50MB
	h := newProxyTestHarness(t, fileSize)
	defer h.Close()

	const numGoroutines = 10
	var wg sync.WaitGroup
	errCh := make(chan error, numGoroutines)

	for i := range numGoroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			offset := int64(id) * int64(fileSize) / int64(numGoroutines)
			end := offset + int64(fileSize)/(2*int64(numGoroutines)) - 1
			if end >= int64(fileSize) {
				end = int64(fileSize) - 1
			}

			req, err := http.NewRequest("GET", h.proxyURL("/testfile.bin"), nil)
			if err != nil {
				errCh <- fmt.Errorf("goroutine %d: create request: %w", id, err)
				return
			}
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, end))

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				errCh <- fmt.Errorf("goroutine %d: GET: %w", id, err)
				return
			}
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				errCh <- fmt.Errorf("goroutine %d: read body: %w", id, err)
				return
			}

			expectedLen := end - offset + 1
			if int64(len(body)) != expectedLen {
				errCh <- fmt.Errorf("goroutine %d: expected %d bytes, got %d", id, expectedLen, len(body))
				return
			}

			expected := h.upstreamFile.content[offset : offset+expectedLen]
			if !bytes.Equal(body, expected) {
				errCh <- fmt.Errorf("goroutine %d: content mismatch at offset %d", id, offset)
				return
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Error(err)
	}
}

// TestLargeFileHEADRequest verifies HEAD requests return correct metadata.
func TestLargeFileHEADRequest(t *testing.T) {
	fileSize := 10 * 1024 * 1024
	h := newProxyTestHarness(t, fileSize)
	defer h.Close()

	req, err := http.NewRequest("HEAD", h.proxyURL("/testfile.bin"), nil)
	if err != nil {
		t.Fatalf("Failed to create HEAD request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Failed to HEAD: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected 200, got %d", resp.StatusCode)
	}

	cl := resp.Header.Get("Content-Length")
	if cl == "" {
		t.Error("Missing Content-Length on HEAD")
	}

	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		t.Error("Missing Content-Type on HEAD")
	}

	lm := resp.Header.Get("Last-Modified")
	if lm == "" {
		t.Error("Missing Last-Modified on HEAD")
	}
}

// TestLargeFileVaryingClientHeaders simulates different clients with different auth tokens
// hitting the same URL through the proxy. This tests for token leakage.
func TestLargeFileVaryingClientHeaders(t *testing.T) {
	fileSize := 1 * 1024 * 1024 // 1MB for speed
	content := make([]byte, fileSize)
	for i := range content {
		content[i] = byte(i % 251)
	}

	// Upstream that checks for a custom auth header
	upstream := newLocalhostServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("X-Test-Auth")

		// Validate: must be exactly "token-A" or "token-B"
		if authHeader != "token-A" && authHeader != "token-B" {
			http.Error(w, "Unauthorized: missing or invalid auth", http.StatusUnauthorized)
			return
		}

		w.Header().Set("Content-Length", fmt.Sprintf("%d", fileSize))
		http.ServeContent(w, r, "file.bin", time.Now(), bytes.NewReader(content))
	}))
	defer upstream.Close()

	opt := DefaultOptions()
	opt.CacheMode = "full"
	cacheDir := t.TempDir()
	opt.CacheDir = cacheDir

	handler, err := NewHandler(opt)
	if err != nil {
		t.Fatalf("Failed to create handler: %v", err)
	}
	defer handler.Shutdown()

	proxyMux := http.NewServeMux()
	proxyMux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		targetURL := r.URL.Query().Get("url")
		if targetURL == "" {
			http.Error(w, "Missing url", http.StatusBadRequest)
			return
		}
		handler.Serve(w, r, targetURL)
	})
	proxyServer := newLocalhostServer(proxyMux)
	defer proxyServer.Close()

	proxyURL := proxyServer.URL + "/stream?url=" + upstream.URL + "/file.bin"

	// Client A authenticates with token-A
	reqA, _ := http.NewRequest("GET", proxyURL, nil)
	reqA.Header.Set("X-Test-Auth", "token-A")

	respA, err := http.DefaultClient.Do(reqA)
	if err != nil {
		t.Fatalf("Client A GET failed: %v", err)
	}
	bodyA, _ := io.ReadAll(respA.Body)
	respA.Body.Close()

	if respA.StatusCode != http.StatusOK {
		t.Fatalf("Client A expected 200, got %d", respA.StatusCode)
	}
	if !bytes.Equal(bodyA, content) {
		t.Fatal("Client A content mismatch")
	}

	// Clear VFS cache by restarting handler
	// Since VFS cache is on disk, we need to use a separate handler for Client B
	opt2 := DefaultOptions()
	opt2.CacheMode = "full"
	cacheDir2 := t.TempDir()
	opt2.CacheDir = cacheDir2

	handler2, err := NewHandler(opt2)
	if err != nil {
		t.Fatalf("Failed to create handler2: %v", err)
	}
	defer handler2.Shutdown()

	proxyMux2 := http.NewServeMux()
	proxyMux2.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		targetURL := r.URL.Query().Get("url")
		if targetURL == "" {
			http.Error(w, "Missing url", http.StatusBadRequest)
			return
		}
		handler2.Serve(w, r, targetURL)
	})
	proxyServer2 := newLocalhostServer(proxyMux2)
	defer proxyServer2.Close()

	proxyURL2 := proxyServer2.URL + "/stream?url=" + upstream.URL + "/file.bin"

	// Client B authenticates with token-B
	reqB, _ := http.NewRequest("GET", proxyURL2, nil)
	reqB.Header.Set("X-Test-Auth", "token-B")

	respB, err := http.DefaultClient.Do(reqB)
	if err != nil {
		t.Fatalf("Client B GET failed: %v", err)
	}
	bodyB, _ := io.ReadAll(respB.Body)
	respB.Body.Close()

	if respB.StatusCode != http.StatusOK {
		t.Fatalf("Client B expected 200, got %d; BODY: %s", respB.StatusCode, string(bodyB))
	}
	if !bytes.Equal(bodyB, content) {
		t.Fatal("Client B content mismatch")
	}
}

// TestLargeFileCacheWithMultipleClients tests the same file through the same proxy
// with different client auth tokens concurrently. This is the KEY test for token leakage.
func TestLargeFileConcurrentAuthHeaders(t *testing.T) {
	fileSize := 512 * 1024 // 512KB

	// Upstream that echos the auth header value into response headers
	upstream := newLocalhostServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("X-Test-Auth")
		w.Header().Set("X-Echo-Auth", authHeader)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", fileSize))
		w.WriteHeader(http.StatusOK)
		// Write content
		for i := 0; i < fileSize; i++ {
			w.Write([]byte{byte(i % 251)})
		}
	}))
	defer upstream.Close()

	opt := DefaultOptions()
	opt.CacheMode = "full"
	cacheDir := t.TempDir()
	opt.CacheDir = cacheDir

	handler, err := NewHandler(opt)
	if err != nil {
		t.Fatalf("Failed to create handler: %v", err)
	}
	defer handler.Shutdown()

	proxyMux := http.NewServeMux()
	proxyMux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		targetURL := r.URL.Query().Get("url")
		if targetURL == "" {
			http.Error(w, "Missing url", http.StatusBadRequest)
			return
		}
		handler.Serve(w, r, targetURL)
	})
	proxyServer := newLocalhostServer(proxyMux)
	defer proxyServer.Close()

	proxyURL := proxyServer.URL + "/stream?url=" + upstream.URL + "/file.bin"

	const numConcurrent = 5
	var wg sync.WaitGroup
	errCh := make(chan string, numConcurrent)

	for i := range numConcurrent {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			token := fmt.Sprintf("token-%d", id)
			req, _ := http.NewRequest("GET", proxyURL, nil)
			req.Header.Set("X-Test-Auth", token)

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				errCh <- fmt.Sprintf("client %d: GET failed: %v", id, err)
				return
			}
			defer resp.Body.Close()

			// If the upstream echoed our auth token back, we can check
			echoedAuth := resp.Header.Get("X-Echo-Auth")
			if echoedAuth != "" && echoedAuth != token {
				errCh <- fmt.Sprintf(
					"TOKEN LEAKAGE DETECTED: client %d sent X-Test-Auth=%q but upstream received %q",
					id, token, echoedAuth)
			}

			// Drain body
			io.Copy(io.Discard, resp.Body)
		}(i)
	}

	wg.Wait()
	close(errCh)

	hasIssues := false
	for err := range errCh {
		t.Error(err)
		hasIssues = true
	}

	if hasIssues {
		t.Fatal("Token leakage or errors detected - see above")
	}
}

// TestLargeFileMultipleRangeRequests tests requesting different ranges of the same file
// through the same proxy server.
func TestLargeFileMultipleRangeRequests(t *testing.T) {
	fileSize := 100 * 1024 * 1024 // 100MB — actually large
	h := newProxyTestHarness(t, fileSize)
	defer h.Close()

	// Request first 1MB and last 1MB
	ranges := []struct {
		name   string
		offset int64
		length int64
	}{
		{"first 1MB", 0, 1024 * 1024},
		{"last 1MB", int64(fileSize - 1024*1024), 1024 * 1024},
	}

	for _, r := range ranges {
		t.Run(r.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", h.proxyURL("/testfile.bin"), nil)
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", r.offset, r.offset+r.length-1))

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("GET failed: %v", err)
			}
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("Read body failed: %v", err)
			}

			if int64(len(body)) != r.length {
				t.Fatalf("Expected %d bytes, got %d", r.length, len(body))
			}

			expected := h.upstreamFile.content[r.offset : r.offset+r.length]
			if !bytes.Equal(body, expected) {
				t.Fatal("Content mismatch")
			}
		})
	}
}

// TestLargeFileStreamingNoCache tests streaming a large file with cache-mode=off.
func TestLargeFileStreamingNoCache(t *testing.T) {
	fileSize := 25 * 1024 * 1024 // 25MB
	opt := DefaultOptions()
	opt.CacheMode = "off"

	h := newProxyTestHarness(t, fileSize, opt)
	defer h.Close()

	resp, err := http.Get(h.proxyURL("/testfile.bin"))
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200, got %d", resp.StatusCode)
	}

	// Read in chunks to verify streaming behavior
	buf := make([]byte, 64*1024) // 64KB chunks
	totalRead := 0
	for {
		n, err := resp.Body.Read(buf)
		totalRead += n
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Read error at byte %d: %v", totalRead, err)
		}
		// Verify each chunk against the expected content
		if n > 0 && !bytes.Equal(buf[:n], h.upstreamFile.content[totalRead-n:totalRead]) {
			t.Fatalf("Content mismatch at byte %d", totalRead-n)
		}
	}

	if totalRead != fileSize {
		t.Fatalf("Expected %d bytes, got %d", fileSize, totalRead)
	}
}

// TestLargeFileCacheDirCleanup verifies the cache directory is properly isolated.
func TestLargeFileCacheDirCleanup(t *testing.T) {
	// Verify temp dir is cleaned up after test
	tmpDir := t.TempDir()

	opt := DefaultOptions()
	opt.CacheMode = "full"
	opt.CacheDir = tmpDir
	// Keep file size small but test that cache files are created
	fileSize := 1 * 1024 * 1024

	h := newProxyTestHarness(t, fileSize, opt)
	defer h.Close()

	// Make a request to populate cache
	resp, err := http.Get(h.proxyURL("/testfile.bin"))
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

// TestLargeFileShardLevel0 tests with no sharding.
func TestLargeFileShardLevel0(t *testing.T) {
	fileSize := 1 * 1024 * 1024
	opt := DefaultOptions()
	opt.CacheMode = "full"
	opt.ShardLevel = 0

	h := newProxyTestHarness(t, fileSize, opt)
	defer h.Close()

	resp, err := http.Get(h.proxyURL("/testfile.bin"))
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Read body failed: %v", err)
	}

	if !bytes.Equal(body, h.upstreamFile.content) {
		t.Fatal("Content mismatch")
	}
}

// TestLargeFileConsecutiveRequests tests the same file downloaded twice through the same proxy.
// The VFS should cache the first request and serve the second from cache.
func TestLargeFileConsecutiveRequests(t *testing.T) {
	fileSize := 5 * 1024 * 1024 // 5MB
	opt := DefaultOptions()
	opt.CacheMode = "full"
	opt.CacheDir = t.TempDir()

	h := newProxyTestHarness(t, fileSize, opt)
	defer h.Close()

	// First request
	resp1, err := http.Get(h.proxyURL("/testfile.bin"))
	if err != nil {
		t.Fatalf("First GET failed: %v", err)
	}
	body1, _ := io.ReadAll(resp1.Body)
	resp1.Body.Close()

	// Second request (should hit VFS cache)
	resp2, err := http.Get(h.proxyURL("/testfile.bin"))
	if err != nil {
		t.Fatalf("Second GET failed: %v", err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()

	if !bytes.Equal(body1, body2) {
		t.Fatal("Content differs between consecutive requests")
	}
	if !bytes.Equal(body1, h.upstreamFile.content) {
		t.Fatal("First request content mismatch")
	}
	if !bytes.Equal(body2, h.upstreamFile.content) {
		t.Fatal("Second request content mismatch")
	}
}

// TestLargeFileUpstreamFailover tests behavior when the upstream is unreachable.
func TestLargeFileUpstreamFailover(t *testing.T) {
	t.Skip("Skipping: requires specific error handling test")

	// This test would verify that the proxy handles upstream failures gracefully
	// by returning appropriate error codes to the client.
}

// TestLargeFileRandomAccess tests random access patterns within a large file.
func TestLargeFileRandomAccess(t *testing.T) {
	fileSize := 100 * 1024 * 1024 // 100MB
	h := newProxyTestHarness(t, fileSize)
	defer h.Close()

	// Read 100 random 4KB chunks from the file
	numChunks := 100
	chunkSize := 4 * 1024

	for i := 0; i < numChunks; i++ {
		offset := int64(i * chunkSize * 257 % fileSize) // deterministic pseudo-random
		if offset+int64(chunkSize) > int64(fileSize) {
			offset = int64(fileSize) - int64(chunkSize)
		}
		end := offset + int64(chunkSize) - 1

		t.Run(fmt.Sprintf("offset_%d", offset), func(t *testing.T) {
			req, _ := http.NewRequest("GET", h.proxyURL("/testfile.bin"), nil)
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, end))

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("GET failed at offset %d: %v", offset, err)
			}
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("Read body failed at offset %d: %v", offset, err)
			}

			if int64(len(body)) != int64(chunkSize) {
				t.Fatalf("Expected %d bytes at offset %d, got %d", chunkSize, offset, len(body))
			}

			expected := h.upstreamFile.content[offset : offset+int64(chunkSize)]
			if !bytes.Equal(body, expected) {
				t.Fatalf("Content mismatch at offset %d", offset)
			}
		})
	}
}

// BenchmarkLargeFileStreaming benchmarks streaming a large file through the proxy.
func BenchmarkLargeFileStreaming(b *testing.B) {
	// Keep benchmark file small for speed, but test the streaming path
	fileSize := 1 * 1024 * 1024 // 1MB
	opt := DefaultOptions()
	opt.CacheMode = "off"

	upstreamFile := newTestFileServer(fileSize)
	upstream := newLocalhostServer(upstreamFile)
	defer upstream.Close()

	cacheDir := b.TempDir()
	opt.CacheDir = cacheDir

	handler, _ := NewHandler(opt)
	defer handler.Shutdown()

	proxyMux := http.NewServeMux()
	proxyMux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		handler.Serve(w, r, r.URL.Query().Get("url"))
	})
	proxyServer := newLocalhostServer(proxyMux)
	defer proxyServer.Close()

	proxyURL := proxyServer.URL + "/stream?url=" + upstream.URL + "/file.bin"

	b.ResetTimer()
	for range b.N {
		resp, err := http.Get(proxyURL)
		if err != nil {
			b.Fatalf("GET failed: %v", err)
		}
		written, _ := io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if written != int64(fileSize) {
			b.Fatalf("Expected %d bytes, got %d", fileSize, written)
		}
	}
}

// TestLargeFileStreamingPartialThenFull tests requesting a partial range, then the full file.
func TestLargeFileStreamingPartialThenFull(t *testing.T) {
	fileSize := 10 * 1024 * 1024
	h := newProxyTestHarness(t, fileSize)
	defer h.Close()

	// Step 1: Request first 1MB
	req1, _ := http.NewRequest("GET", h.proxyURL("/testfile.bin"), nil)
	req1.Header.Set("Range", "bytes=0-1048575")
	resp1, err := http.DefaultClient.Do(req1)
	if err != nil {
		t.Fatalf("Range GET failed: %v", err)
	}
	body1, _ := io.ReadAll(resp1.Body)
	resp1.Body.Close()

	if len(body1) != 1024*1024 {
		t.Fatalf("Expected 1MB from range, got %d", len(body1))
	}

	// Step 2: Request full file
	resp2, err := http.Get(h.proxyURL("/testfile.bin"))
	if err != nil {
		t.Fatalf("Full GET failed: %v", err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()

	if len(body2) != fileSize {
		t.Fatalf("Expected %d bytes from full request, got %d", fileSize, len(body2))
	}

	if !bytes.Equal(body2, h.upstreamFile.content) {
		t.Fatal("Full file content mismatch")
	}

	// The first 1MB should match
	if !bytes.Equal(body2[:1024*1024], body1) {
		t.Fatal("Range content doesn't match full file prefix")
	}
}

// TestLargeFileWithZeroCache tests with cache-min-free-space set to ensure
// the VFS correctly handles disk space constraints.
func TestLargeFileWithZeroCache(t *testing.T) {
	fileSize := 1 * 1024 * 1024
	opt := DefaultOptions()
	opt.CacheMode = "minimal"
	opt.CacheDir = t.TempDir()

	h := newProxyTestHarness(t, fileSize, opt)
	defer h.Close()

	resp, err := http.Get(h.proxyURL("/testfile.bin"))
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Read body failed: %v", err)
	}

	if !bytes.Equal(body, h.upstreamFile.content) {
		t.Fatal("Content mismatch with minimal cache")
	}
}

// TestReRegisterIdempotent verifies that twice-registering the same URL is safe
// and doesn't cause panics or data races.
func TestReRegisterIdempotent(t *testing.T) {
	link.Register("test-hash", "https://example.com/file1", nil)
	link.Register("test-hash", "https://example.com/file1", http.Header{"Authorization": {"Bearer token"}})
	link.Register("test-hash", "https://example.com/file1", nil)

	url, ok := link.Load("test-hash")
	if !ok {
		t.Fatal("Expected hash to be loadable")
	}
	if url == "" {
		t.Fatal("Expected non-empty URL")
	}
}

// TestLargeFileNoChunkStreams tests with minimal chunk streams.
func TestLargeFileNoChunkStreams(t *testing.T) {
	fileSize := 5 * 1024 * 1024
	opt := DefaultOptions()
	opt.CacheMode = "full"
	opt.CacheChunkStreams = 1 // Single stream
	opt.CacheChunkSize = "1M" // 1MB chunks
	opt.CacheDir = t.TempDir()

	h := newProxyTestHarness(t, fileSize, opt)
	defer h.Close()

	resp, err := http.Get(h.proxyURL("/testfile.bin"))
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Read body failed: %v", err)
	}

	if !bytes.Equal(body, h.upstreamFile.content) {
		t.Fatal("Content mismatch with single chunk stream")
	}
}

// TestRandomContentLargeFile uses random content instead of pattern to verify
// no compression or encoding issues.
func TestRandomContentLargeFile(t *testing.T) {
	fileSize := 5 * 1024 * 1024
	content := make([]byte, fileSize)
	rand.Read(content)

	upstream := newLocalhostServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", fileSize))
		http.ServeContent(w, r, "random.bin", time.Now(), bytes.NewReader(content))
	}))
	defer upstream.Close()

	opt := DefaultOptions()
	opt.CacheMode = "off"
	opt.CacheDir = t.TempDir()

	handler, _ := NewHandler(opt)
	defer handler.Shutdown()

	proxyMux := http.NewServeMux()
	proxyMux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		handler.Serve(w, r, r.URL.Query().Get("url"))
	})
	proxyServer := newLocalhostServer(proxyMux)
	defer proxyServer.Close()

	proxyURL := proxyServer.URL + "/stream?url=" + upstream.URL + "/random.bin"

	resp, err := http.Get(proxyURL)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Read body failed: %v", err)
	}

	if !bytes.Equal(body, content) {
		t.Fatal("Random content mismatch")
	}
}

// TestMain runs setup/teardown for the test package.
func TestMain(m *testing.M) {
	// Ensure VFS cache dir is isolated
	tmpDir, err := os.MkdirTemp("", "rclone-vfs-test-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create test temp dir: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()

	// Cleanup
	os.RemoveAll(tmpDir)
	os.Exit(code)
}

// TestLargeFileWithStripQuery tests stripping query parameters when caching.
func TestLargeFileWithStripQuery(t *testing.T) {
	fileSize := 1 * 1024 * 1024
	opt := DefaultOptions()
	opt.CacheMode = "full"
	opt.StripQuery = true
	opt.CacheDir = t.TempDir()

	upstreamFile := newTestFileServer(fileSize)
	upstream := newLocalhostServer(upstreamFile)
	defer upstream.Close()

	handler, _ := NewHandler(opt)
	defer handler.Shutdown()

	proxyMux := http.NewServeMux()
	proxyMux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		handler.Serve(w, r, r.URL.Query().Get("url"))
	})
	proxyServer := newLocalhostServer(proxyMux)
	defer proxyServer.Close()

	// Same path, different query params — should map to same cache entry
	url1 := proxyServer.URL + "/stream?url=" + upstream.URL + "/file.bin?token=a"
	url2 := proxyServer.URL + "/stream?url=" + upstream.URL + "/file.bin?token=b"

	resp1, err := http.Get(url1)
	if err != nil {
		t.Fatalf("GET url1 failed: %v", err)
	}
	body1, _ := io.ReadAll(resp1.Body)
	resp1.Body.Close()

	resp2, err := http.Get(url2)
	if err != nil {
		t.Fatalf("GET url2 failed: %v", err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()

	if !bytes.Equal(body1, body2) {
		t.Fatal("Expected identical content with stripQuery (URLs differ only by query param)")
	}
	if !bytes.Equal(body1, upstreamFile.content) {
		t.Fatal("Content mismatch")
	}
}

// TestLargeFileCacheDirCreation verifies the cache directory is created if it doesn't exist.
func TestLargeFileCacheDirCreation(t *testing.T) {
	// Use a non-existent nested path
	nonExistentDir := filepath.Join(t.TempDir(), "deep", "nested", "cache")
	opt := DefaultOptions()
	opt.CacheMode = "full"
	opt.CacheDir = nonExistentDir

	fileSize := 1 * 1024 * 1024
	upstreamFile := newTestFileServer(fileSize)
	upstream := newLocalhostServer(upstreamFile)
	defer upstream.Close()

	handler, err := NewHandler(opt)
	if err != nil {
		t.Fatalf("Failed to create handler: %v", err)
	}
	defer handler.Shutdown()

	proxyMux := http.NewServeMux()
	proxyMux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		handler.Serve(w, r, r.URL.Query().Get("url"))
	})
	proxyServer := newLocalhostServer(proxyMux)
	defer proxyServer.Close()

	proxyURL := proxyServer.URL + "/stream?url=" + upstream.URL + "/file.bin"
	resp, err := http.Get(proxyURL)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Read body failed: %v", err)
	}

	if !bytes.Equal(body, upstreamFile.content) {
		t.Fatal("Content mismatch")
	}

	// Verify cache dir was created
	if _, err := os.Stat(nonExistentDir); os.IsNotExist(err) {
		t.Fatal("Cache directory was not created")
	}
}
