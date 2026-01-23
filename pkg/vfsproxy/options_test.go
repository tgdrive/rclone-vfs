package vfsproxy

import (
	"testing"

	"github.com/spf13/pflag"
)

func TestDefaultOptions(t *testing.T) {
	opt := DefaultOptions()

	// Test default values from tags
	if opt.FsName != "rclone-vfs" {
		t.Errorf("expected FsName 'rclone-vfs', got '%s'", opt.FsName)
	}
	if opt.ShardLevel != 1 {
		t.Errorf("expected ShardLevel 1, got %d", opt.ShardLevel)
	}

	// Test a value that should come from rclone defaults (vfscommon.Opt)
	// rclone default for vfs_read_chunk_size is usually "128Mi" or similar
	if opt.CacheChunkSize == "" {
		t.Error("expected CacheChunkSize to be populated from rclone defaults, but it's empty")
	}
}

func TestToConfigMap(t *testing.T) {
	opt := Options{
		CacheMode:    "full",
		CacheMaxAge:  "24h",
		ReadOnly:     true,
		ShardLevel:   5, // Has no vfs tag, should be ignored
		StripQuery:   true, // Has no vfs tag, should be ignored
	}

	m := opt.ToConfigMap()

	if m["vfs_cache_mode"] != "full" {
		t.Errorf("expected vfs_cache_mode 'full', got '%s'", m["vfs_cache_mode"])
	}
	if m["vfs_cache_max_age"] != "24h" {
		t.Errorf("expected vfs_cache_max_age '24h', got '%s'", m["vfs_cache_max_age"])
	}
	if m["read_only"] != "true" {
		t.Errorf("expected read_only 'true', got '%s'", m["read_only"])
	}

	// Check that fields with vfs:"-" are NOT in the map
	if _, exists := m["shard_level"]; exists {
		t.Error("shard_level should not be in the config map")
	}
}

func TestAddFlags(t *testing.T) {
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	opt := Options{
		FsName: "test-fs",
	}

	opt.AddFlags(fs)

	// Verify flags were registered
	f := fs.Lookup("fs-name")
	if f == nil {
		t.Fatal("flag 'fs-name' not found")
	}
	if f.DefValue != "test-fs" {
		t.Errorf("expected default value 'test-fs', got '%s'", f.DefValue)
	}

	// Test parsing a flag
	err := fs.Parse([]string{"--fs-name", "overridden", "--shard-level", "3"})
	if err != nil {
		t.Fatalf("failed to parse flags: %v", err)
	}

	if opt.FsName != "overridden" {
		t.Errorf("expected FsName 'overridden', got '%s'", opt.FsName)
	}
	if opt.ShardLevel != 3 {
		t.Errorf("expected ShardLevel 3, got %d", opt.ShardLevel)
	}
}
