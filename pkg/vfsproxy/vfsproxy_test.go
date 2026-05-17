package vfsproxy

import (
	"testing"
)

func TestGetFileHashConsistency(t *testing.T) {
	h := &Handler{
		hashCache:  make(map[string]string),
		stripQuery: false,
	}

	// Same URL should produce the same hash
	hash1 := h.getFileHash("https://example.com/file.mp4")
	hash2 := h.getFileHash("https://example.com/file.mp4")

	if hash1 != hash2 {
		t.Errorf("expected identical hashes for same URL, got %q and %q", hash1, hash2)
	}

	if hash1 == "" {
		t.Error("expected non-empty hash")
	}
}

func TestGetFileHashStripQuery(t *testing.T) {
	h := &Handler{
		hashCache:  make(map[string]string),
		stripQuery: true,
	}

	// With stripQuery, URLs differing only in query params should have the same hash
	hash1 := h.getFileHash("https://example.com/file.mp4?token=abc")
	hash2 := h.getFileHash("https://example.com/file.mp4?token=xyz")

	if hash1 != hash2 {
		t.Error("expected same hash when stripQuery is true and URLs differ only by query")
	}
}

func TestGetFileHashStripDomain(t *testing.T) {
	h := &Handler{
		hashCache:   make(map[string]string),
		stripDomain: true,
	}

	// With stripDomain, same path on different domains should have the same hash
	hash1 := h.getFileHash("https://mirror1.example.com/file.mp4")
	hash2 := h.getFileHash("https://mirror2.example.com/file.mp4")

	if hash1 != hash2 {
		t.Error("expected same hash when stripDomain is true and URLs differ only by domain")
	}
}

func TestGetFileHashDifferentURLs(t *testing.T) {
	h := &Handler{
		hashCache:  make(map[string]string),
		stripQuery: false,
	}

	// Different URLs should produce different hashes
	hash1 := h.getFileHash("https://example.com/file1.mp4")
	hash2 := h.getFileHash("https://example.com/file2.mp4")

	if hash1 == hash2 {
		t.Error("expected different hashes for different URLs")
	}
}

func TestGetFileHashCacheEviction(t *testing.T) {
	// Temporarily override the limit for testing
	savedMax := maxHashCacheEntries
	maxHashCacheEntries = 2
	defer func() { maxHashCacheEntries = savedMax }()

	h := &Handler{
		hashCache:  make(map[string]string),
		stripQuery: false,
	}

	// Fill the cache
	h.getFileHash("https://example.com/file1.mp4")
	h.getFileHash("https://example.com/file2.mp4")

	// Third URL should trigger eviction (map is cleared)
	h.getFileHash("https://example.com/file3.mp4")

	// After eviction, the first URL should be recomputed (not cause issues)
	hash := h.getFileHash("https://example.com/file1.mp4")
	if hash == "" {
		t.Error("expected non-empty hash after eviction")
	}
}

func TestGetFileHashCaching(t *testing.T) {
	h := &Handler{
		hashCache:  make(map[string]string),
		stripQuery: false,
	}

	// First call computes
	hash1 := h.getFileHash("https://example.com/file.mp4")

	// Should be in cache now
	if _, exists := h.hashCache["https://example.com/file.mp4"]; !exists {
		t.Error("expected hash to be cached")
	}

	// Second call should return cached value
	hash2 := h.getFileHash("https://example.com/file.mp4")
	if hash1 != hash2 {
		t.Error("expected cached hash to match computed hash")
	}
}
