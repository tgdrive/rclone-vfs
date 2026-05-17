package link

import (
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestStripURL(t *testing.T) {
	tests := []struct {
		name        string
		url         string
		stripQuery  bool
		stripDomain bool
		want        string
	}{
		{
			name:        "no stripping",
			url:         "https://example.com/path/file.mp4?token=abc123",
			stripQuery:  false,
			stripDomain: false,
			want:        "https://example.com/path/file.mp4?token=abc123",
		},
		{
			name:        "strip query only",
			url:         "https://example.com/path/file.mp4?token=abc123",
			stripQuery:  true,
			stripDomain: false,
			want:        "https://example.com/path/file.mp4",
		},
		{
			name:        "strip domain only",
			url:         "https://example.com/path/file.mp4?token=abc123",
			stripQuery:  false,
			stripDomain: true,
			want:        "/path/file.mp4?token=abc123",
		},
		{
			name:        "strip both",
			url:         "https://example.com/path/file.mp4?token=abc123",
			stripQuery:  true,
			stripDomain: true,
			want:        "/path/file.mp4",
		},
		{
			name:        "invalid URL returns original",
			url:         "://invalid",
			stripQuery:  true,
			stripDomain: true,
			want:        "://invalid",
		},
		{
			name:        "no query string with strip query",
			url:         "https://example.com/file.mp4",
			stripQuery:  true,
			stripDomain: false,
			want:        "https://example.com/file.mp4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripURL(tt.url, tt.stripQuery, tt.stripDomain)
			if got != tt.want {
				t.Errorf("StripURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestShardedPath(t *testing.T) {
	tests := []struct {
		name      string
		fileHash  string
		level     int
		want      string
	}{
		{
			name:      "level 0",
			fileHash:  "abcdef123456",
			level:     0,
			want:      "abcdef123456",
		},
		{
			name:      "level 1",
			fileHash:  "abcdef123456",
			level:     1,
			want:      "ab/abcdef123456",
		},
		{
			name:      "level 2",
			fileHash:  "abcdef123456",
			level:     2,
			want:      "ab/cd/abcdef123456",
		},
		{
			name:      "level 3",
			fileHash:  "abcdef123456",
			level:     3,
			want:      "ab/cd/ef/abcdef123456",
		},
		{
			name:      "short hash with level 1",
			fileHash:  "a",
			level:     1,
			want:      "a",
		},
		{
			name:      "negative level",
			fileHash:  "abc123",
			level:     -1,
			want:      "abc123",
		},
		{
			name:      "level exceeds hash length",
			fileHash:  "abc",
			level:     5,
			want:      "ab/abc",
		},
		{
			name:      "empty hash",
			fileHash:  "",
			level:     1,
			want:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShardedPath(tt.fileHash, tt.level)
			if got != tt.want {
				t.Errorf("ShardedPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRegisterAndLoad(t *testing.T) {
	// Clear any existing entries
	urlMap = sync.Map{}

	Register("hash1", "https://example.com/file1.mp4", nil)
	Register("hash2", "https://example.com/file2.mp4", http.Header{"Authorization": {"Bearer token"}})

	t.Run("load existing entry without headers", func(t *testing.T) {
		url, ok := Load("hash1")
		if !ok {
			t.Fatal("expected entry to exist")
		}
		if url != "https://example.com/file1.mp4" {
			t.Errorf("got url %q, want %q", url, "https://example.com/file1.mp4")
		}
	})

	t.Run("load existing entry with headers", func(t *testing.T) {
		val, ok := urlMap.Load("hash2")
		if !ok {
			t.Fatal("expected entry to exist")
		}
		e := val.(*entry)
		if e.url != "https://example.com/file2.mp4" {
			t.Errorf("got url %q, want %q", e.url, "https://example.com/file2.mp4")
		}
		if e.header.Get("Authorization") != "Bearer token" {
			t.Errorf("expected Authorization header, got %q", e.header.Get("Authorization"))
		}
	})

	t.Run("load non-existent entry", func(t *testing.T) {
		_, ok := Load("nonexistent")
		if ok {
			t.Error("expected entry to not exist")
		}
	})

	t.Run("createdAt is set on register", func(t *testing.T) {
		val, ok := urlMap.Load("hash1")
		if !ok {
			t.Fatal("expected entry to exist")
		}
		e := val.(*entry)
		if e.createdAt.IsZero() {
			t.Error("expected createdAt to be set")
		}
	})

	t.Run("overwrite updates createdAt", func(t *testing.T) {
		val, _ := urlMap.Load("hash1")
		oldCreated := val.(*entry).createdAt

		time.Sleep(time.Millisecond)
		Register("hash1", "https://example.com/new-file.mp4", nil)

		val, _ = urlMap.Load("hash1")
		e := val.(*entry)
		if e.createdAt.Equal(oldCreated) {
			t.Error("expected createdAt to be updated after re-register")
		}
		if e.url != "https://example.com/new-file.mp4" {
			t.Errorf("got url %q, want %q", e.url, "https://example.com/new-file.mp4")
		}
	})
}

func TestObjectMethods(t *testing.T) {
	now := time.Now()
	o := &Object{
		fs:       &Fs{name: "test-fs"},
		remote:   "ab/abcdef",
		url:      "https://example.com/file.mp4",
		size:     1024,
		modTime:  now,
		mimeType: "video/mp4",
	}

	t.Run("Fs", func(t *testing.T) {
		if o.Fs() != o.fs {
			t.Error("Fs() should return the fs field")
		}
	})

	t.Run("Remote", func(t *testing.T) {
		if o.Remote() != "ab/abcdef" {
			t.Errorf("got %q, want %q", o.Remote(), "ab/abcdef")
		}
	})

	t.Run("String", func(t *testing.T) {
		if o.String() != "ab/abcdef" {
			t.Errorf("got %q, want %q", o.String(), "ab/abcdef")
		}
	})

	t.Run("Size", func(t *testing.T) {
		if o.Size() != 1024 {
			t.Errorf("got %d, want %d", o.Size(), 1024)
		}
	})

	t.Run("ModTime", func(t *testing.T) {
		if !o.ModTime(nil).Equal(now) {
			t.Errorf("got %v, want %v", o.ModTime(nil), now)
		}
	})

	t.Run("MimeType", func(t *testing.T) {
		if o.MimeType(nil) != "video/mp4" {
			t.Errorf("got %q, want %q", o.MimeType(nil), "video/mp4")
		}
	})

	t.Run("Storable", func(t *testing.T) {
		if !o.Storable() {
			t.Error("expected Storable to be true")
		}
	})
}

func TestFsBasicMethods(t *testing.T) {
	f := &Fs{
		name:       "test-fs",
		root:       "",
		stripQuery: false,
	}

	t.Run("Name", func(t *testing.T) {
		if f.Name() != "test-fs" {
			t.Errorf("got %q, want %q", f.Name(), "test-fs")
		}
	})

	t.Run("Root", func(t *testing.T) {
		if f.Root() != "" {
			t.Errorf("got %q, want %q", f.Root(), "")
		}
	})

	t.Run("String", func(t *testing.T) {
		if f.String() != "link:" {
			t.Errorf("got %q, want %q", f.String(), "link:")
		}
	})

	t.Run("Hashes", func(t *testing.T) {
		// Verify Hashes() returns without panicking
		_ = f.Hashes()
	})

	t.Run("Precision", func(t *testing.T) {
		if f.Precision() != time.Second {
			t.Errorf("got %v, want %v", f.Precision(), time.Second)
		}
	})
}

func TestCleanupStaleEntries(t *testing.T) {
	urlMap = sync.Map{}

	// Register an entry
	Register("stale-hash", "https://example.com/stale.mp4", nil)

	// Manually set createdAt far in the past
	val, _ := urlMap.Load("stale-hash")
	e := val.(*entry)
	e.mu.Lock()
	e.createdAt = time.Now().Add(-2 * time.Hour)
	e.mu.Unlock()

	// Register a fresh entry
	Register("fresh-hash", "https://example.com/fresh.mp4", nil)

	// Run cleanup with a short TTL
	f := &Fs{cacheTTL: 30 * time.Minute}
	f.cleanupStaleEntries()

	// Stale entry should be removed
	if _, ok := urlMap.Load("stale-hash"); ok {
		t.Error("expected stale entry to be cleaned up")
	}

	// Fresh entry should remain
	if _, ok := urlMap.Load("fresh-hash"); !ok {
		t.Error("expected fresh entry to remain")
	}
}

func TestErrorReadOnly(t *testing.T) {
	o := &Object{}

	if err := o.SetModTime(nil, time.Now()); err != errorReadOnly {
		t.Errorf("SetModTime: got %v, want %v", err, errorReadOnly)
	}
	if err := o.Remove(nil); err != errorReadOnly {
		t.Errorf("Remove: got %v, want %v", err, errorReadOnly)
	}
	if err := o.Update(nil, nil, nil); err != errorReadOnly {
		t.Errorf("Update: got %v, want %v", err, errorReadOnly)
	}
}
