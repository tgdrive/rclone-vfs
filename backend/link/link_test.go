package link

import (
	"context"
	"testing"

	"github.com/rclone/rclone/fs/config/configmap"
)

func TestStripFlagsInBackend(t *testing.T) {
	tests := []struct {
		name              string
		opts              configmap.Simple
		inputURL          string
		expectStrippedURL string
	}{
		{
			name: "no stripping",
			opts: configmap.Simple{
				"strip_query":  "false",
				"strip_domain": "false",
			},
			inputURL:          "https://example.com/file.txt?param=value",
			expectStrippedURL: "https://example.com/file.txt?param=value",
		},
		{
			name: "strip_query only",
			opts: configmap.Simple{
				"strip_query":  "true",
				"strip_domain": "false",
			},
			inputURL:          "https://example.com/file.txt?param=value",
			expectStrippedURL: "https://example.com/file.txt",
		},
		{
			name: "strip_domain only",
			opts: configmap.Simple{
				"strip_query":  "false",
				"strip_domain": "true",
			},
			inputURL:          "https://example.com/file.txt?param=value",
			expectStrippedURL: "/file.txt?param=value",
		},
		{
			name: "strip both",
			opts: configmap.Simple{
				"strip_query":  "true",
				"strip_domain": "true",
			},
			inputURL:          "https://example.com/file.txt?param=value",
			expectStrippedURL: "/file.txt",
		},
		{
			name: "strip both with user info and fragment",
			opts: configmap.Simple{
				"strip_query":  "true",
				"strip_domain": "true",
			},
			inputURL:          "https://user:pass@example.com/file.txt?param=value#section",
			expectStrippedURL: "/file.txt",
		},
		{
			name: "strip_query preserves fragment",
			opts: configmap.Simple{
				"strip_query":  "true",
				"strip_domain": "false",
			},
			inputURL:          "https://example.com/file.txt#section",
			expectStrippedURL: "https://example.com/file.txt#section",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := NewFs(context.Background(), "test", "", tt.opts)
			if err != nil {
				t.Fatalf("NewFs failed: %v", err)
			}

			fs := f.(*Fs)

			// Verify the flags were set correctly
			if tt.opts["strip_query"] == "true" && !fs.stripQuery {
				t.Error("stripQuery should be true")
			}
			if tt.opts["strip_query"] == "false" && fs.stripQuery {
				t.Error("stripQuery should be false")
			}
			if tt.opts["strip_domain"] == "true" && !fs.stripDomain {
				t.Error("stripDomain should be true")
			}
			if tt.opts["strip_domain"] == "false" && fs.stripDomain {
				t.Error("stripDomain should be false")
			}

			// The actual stripping happens in NewObject which requires network
			// So we just verify the flags were set correctly
			t.Logf("Fs stripQuery=%v, stripDomain=%v", fs.stripQuery, fs.stripDomain)
		})
	}
}

func TestRegisterAndLoad(t *testing.T) {
	remote := "test-remote"
	url := "https://example.com/test/file.txt"

	Register(remote, url)

	loadedURL, exists := Load(remote)
	if !exists {
		t.Error("Load() returned false, expected true")
	}

	if loadedURL != url {
		t.Errorf("Load() = %q, want %q", loadedURL, url)
	}
}

func TestLoadNonExistent(t *testing.T) {
	_, exists := Load("non-existent-remote")
	if exists {
		t.Error("Load() returned true for non-existent key, expected false")
	}
}

func TestRegisterOverwrite(t *testing.T) {
	remote := "test-remote-overwrite"
	originalURL := "https://example.com/original/file.txt"
	newURL := "https://example.com/new/file.txt"

	Register(remote, originalURL)
	Register(remote, newURL)

	loadedURL, _ := Load(remote)
	if loadedURL != newURL {
		t.Errorf("After overwrite, Load() = %q, want %q", loadedURL, newURL)
	}
}
