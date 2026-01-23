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
			chunk_streams 4
			strip_query
			shard-level 3
			read_only
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
	if !v.ReadOnly {
		t.Error("expected ReadOnly to be true")
	}

	// Test defaults for things not in the Caddyfile
	if v.FsName != "rclone-vfs" {
		t.Errorf("expected default FsName 'rclone-vfs', got '%s'", v.FsName)
	}
}
