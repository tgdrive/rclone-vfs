package link

import (
	"net/url"
	"testing"
)

func TestStripQueryAndDomain(t *testing.T) {
	tests := []struct {
		name        string
		inputURL    string
		stripQuery  bool
		stripDomain bool
		expected    string
	}{
		{
			name:        "strip_query only",
			inputURL:    "https://example.com/file.txt?param1=value1&param2=value2",
			stripQuery:  true,
			stripDomain: false,
			expected:    "https://example.com/file.txt",
		},
		{
			name:        "strip_domain only",
			inputURL:    "https://example.com/file.txt?param1=value1",
			stripQuery:  false,
			stripDomain: true,
			expected:    "/file.txt?param1=value1",
		},
		{
			name:        "strip both query and domain",
			inputURL:    "https://example.com/file.txt?param1=value1",
			stripQuery:  true,
			stripDomain: true,
			expected:    "/file.txt",
		},
		{
			name:        "no stripping",
			inputURL:    "https://example.com/file.txt?param1=value1",
			stripQuery:  false,
			stripDomain: false,
			expected:    "https://example.com/file.txt?param1=value1",
		},
		{
			name:        "URL with port",
			inputURL:    "https://example.com:8080/file.txt?param1=value1",
			stripQuery:  false,
			stripDomain: true,
			expected:    "/file.txt?param1=value1",
		},
		{
			name:        "URL with path and port strip both",
			inputURL:    "https://example.com:8080/path/to/file.txt?param=value",
			stripQuery:  true,
			stripDomain: true,
			expected:    "/path/to/file.txt",
		},
		{
			name:        "relative URL with query",
			inputURL:    "/path/to/file.txt?param=value",
			stripQuery:  true,
			stripDomain: true,
			expected:    "/path/to/file.txt",
		},
		{
			name:        "URL without scheme",
			inputURL:    "example.com/file.txt?param=value",
			stripQuery:  true,
			stripDomain: true,
			expected:    "example.com/file.txt",
		},
		{
			name:        "just path",
			inputURL:    "/file.txt",
			stripQuery:  true,
			stripDomain: true,
			expected:    "/file.txt",
		},
		{
			name:        "URL with user info",
			inputURL:    "https://user:pass@example.com/file.txt",
			stripQuery:  true,
			stripDomain: true,
			expected:    "/file.txt",
		},
		{
			name:        "URL with hash fragment",
			inputURL:    "https://example.com/file.txt#section",
			stripQuery:  true,
			stripDomain: true,
			expected:    "/file.txt",
		},
		{
			name:        "strip_query preserves fragment",
			inputURL:    "https://example.com/file.txt#section",
			stripQuery:  true,
			stripDomain: false,
			expected:    "https://example.com/file.txt#section",
		},
		{
			name:        "URL with user info strip_domain only",
			inputURL:    "https://user:pass@example.com/file.txt",
			stripQuery:  false,
			stripDomain: true,
			expected:    "/file.txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripURL(tt.inputURL, tt.stripQuery, tt.stripDomain)
			if result != tt.expected {
				t.Errorf("stripURL() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func stripURL(u string, stripQuery, stripDomain bool) string {
	keyURL := u
	if stripQuery || stripDomain {
		if parsedURL, err := url.Parse(u); err == nil {
			if stripQuery {
				parsedURL.RawQuery = ""
			}
			if stripDomain {
				parsedURL.Scheme = ""
				parsedURL.Host = ""
				parsedURL.User = nil
				parsedURL.Fragment = ""
			}
			keyURL = parsedURL.String()
		}
	}
	return keyURL
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
