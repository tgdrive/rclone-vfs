package vfsproxy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestHandlerServe(t *testing.T) {
	opt := Options{
		FsName:       "link-test",
		CacheDir:     "",
		CacheMode:    "full",
		DirCacheTime: "0s",
	}

	handler, err := NewHandler(opt)
	if err != nil {
		t.Fatalf("Failed to create handler: %v", err)
	}
	defer handler.Shutdown()

	tests := []struct {
		name       string
		targetURL  string
		wantStatus int
	}{
		{
			name:       "empty URL",
			targetURL:  "",
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			w := httptest.NewRecorder()

			handler.Serve(w, req, tt.targetURL)

			if w.Code != tt.wantStatus {
				t.Errorf("Serve() status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestHandlerServeFileNotFound(t *testing.T) {
	opt := Options{
		FsName:       "link-test",
		CacheDir:     "",
		CacheMode:    "full",
		DirCacheTime: "0s",
	}

	handler, err := NewHandler(opt)
	if err != nil {
		t.Fatalf("Failed to create handler: %v", err)
	}
	defer handler.Shutdown()

	req := httptest.NewRequest("GET", "/nonexistent", nil)
	w := httptest.NewRecorder()

	handler.Serve(w, req, "https://nonexistent.example.com/file.txt")

	if w.Code != http.StatusNotFound {
		t.Errorf("Serve() status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestHashCaching(t *testing.T) {
	opt := Options{
		FsName:       "link-test",
		CacheDir:     "",
		CacheMode:    "full",
		DirCacheTime: "0s",
	}

	handler, err := NewHandler(opt)
	if err != nil {
		t.Fatalf("Failed to create handler: %v", err)
	}
	defer handler.Shutdown()

	testURL := "https://example.com/test/file.txt"

	hash1 := handler.getFileHash(testURL)
	if hash1 == "" {
		t.Error("First getFileHash() returned empty string")
	}

	hash2 := handler.getFileHash(testURL)
	if hash1 != hash2 {
		t.Errorf("getFileHash() returned different hashes: %s != %s", hash1, hash2)
	}

	handler.mu.RLock()
	cachedHash, exists := handler.hashCache[testURL]
	handler.mu.RUnlock()

	if !exists {
		t.Error("Hash not found in cache after getFileHash()")
	}

	if cachedHash != hash1 {
		t.Errorf("Cached hash mismatch: cached=%s, computed=%s", cachedHash, hash1)
	}
}

func TestHashCachingConcurrency(t *testing.T) {
	opt := Options{
		FsName:       "link-test",
		CacheDir:     "",
		CacheMode:    "full",
		DirCacheTime: "0s",
	}

	handler, err := NewHandler(opt)
	if err != nil {
		t.Fatalf("Failed to create handler: %v", err)
	}
	defer handler.Shutdown()

	testURL := "https://example.com/concurrent/test.txt"

	var wg sync.WaitGroup
	numGoroutines := 100
	hashes := make([]string, numGoroutines)
	errors := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			hash := handler.getFileHash(testURL)
			if hash == "" {
				errors <- fmt.Errorf("goroutine %d got empty hash", idx)
				return
			}
			hashes[idx] = hash
		}(i)
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Error(err.Error())
	}

	for i := 1; i < numGoroutines; i++ {
		if hashes[i] != hashes[0] {
			t.Errorf("Hash mismatch at index %d: %s != %s", i, hashes[i], hashes[0])
		}
	}
}

func TestRegisterOptimization(t *testing.T) {
	opt := Options{
		FsName:       "link-test",
		CacheDir:     "",
		CacheMode:    "full",
		DirCacheTime: "0s",
	}

	handler, err := NewHandler(opt)
	if err != nil {
		t.Fatalf("Failed to create handler: %v", err)
	}
	defer handler.Shutdown()

	testURL := "https://example.com/optimization/test.txt"

	req := httptest.NewRequest("GET", "/test1", nil)
	w := httptest.NewRecorder()
	handler.Serve(w, req, testURL)

	req2 := httptest.NewRequest("GET", "/test2", nil)
	w2 := httptest.NewRecorder()
	handler.Serve(w2, req2, testURL)
}
