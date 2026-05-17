package vfs

import (
	"testing"

	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/tgdrive/rclone-vfs/pkg/vfsproxy"
)

func TestUnmarshalCaddyfile(t *testing.T) {
	d := caddyfile.NewTestDispenser(`
		vfs https://example.com {
			cache_dir /tmp/cache
			cache_mode full
			max_age 24h
			chunk_size 4M
			chunk_streams 4
			strip_query
			shard_level 3
		}
	`)

	v := &VFS{
		Options: vfsproxy.DefaultOptions(),
	}

	err := v.UnmarshalCaddyfile(d)
	if err != nil {
		t.Fatalf("failed to unmarshal caddyfile: %v", err)
	}

	if v.Upstream != "https://example.com" {
		t.Errorf("expected Upstream 'https://example.com', got '%s'", v.Upstream)
	}

	// Test reflection-mapped string options
	if v.CacheDir != "/tmp/cache" {
		t.Errorf("expected CacheDir '/tmp/cache', got '%s'", v.CacheDir)
	}
	if v.CacheMode != "full" {
		t.Errorf("expected CacheMode 'full', got '%s'", v.CacheMode)
	}
	if v.CacheMaxAge != "24h" {
		t.Errorf("expected CacheMaxAge '24h', got '%s'", v.CacheMaxAge)
	}
	if v.CacheChunkSize != "4M" {
		t.Errorf("expected CacheChunkSize '4M', got '%s'", v.CacheChunkSize)
	}

	// Test reflection-mapped integer options
	if v.CacheChunkStreams != 4 {
		t.Errorf("expected CacheChunkStreams 4, got %d", v.CacheChunkStreams)
	}
	if v.ShardLevel != 3 {
		t.Errorf("expected ShardLevel 3, got %d", v.ShardLevel)
	}

	// Test reflection-mapped boolean flags
	if !v.StripQuery {
		t.Error("expected StripQuery to be true")
	}
}
