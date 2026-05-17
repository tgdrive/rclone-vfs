package types

import (
	"os"
	"runtime"
	"time"
)

const mebi = 1048576 // 1 MiB in bytes

// Options is options for creating the vfs
type Options struct {
	NoSeek             bool          `config:"no_seek"`        // don't allow seeking if set
	NoChecksum         bool          `config:"no_checksum"`    // don't check checksums if set
	ReadOnly           bool          `config:"read_only"`      // if set VFS is read only
	Links              bool          `config:"vfs_links"`      // if set interpret link files
	NoModTime          bool          `config:"no_modtime"`     // don't read mod times for files
	DirCacheTime       time.Duration `config:"dir_cache_time"` // how long to consider directory listing cache valid
	Refresh            bool          `config:"vfs_refresh"`    // refreshes the directory listing recursively on start
	PollInterval       time.Duration `config:"poll_interval"`
	Umask              FileMode      `config:"umask"`
	UID                uint32        `config:"uid"`
	GID                uint32        `config:"gid"`
	DirPerms           FileMode      `config:"dir_perms"`
	FilePerms          FileMode      `config:"file_perms"`
	LinkPerms          FileMode      `config:"link_perms"`
	ChunkSize          int64         `config:"vfs_read_chunk_size"`       // if > 0 read files in chunks
	ChunkSizeLimit     int64         `config:"vfs_read_chunk_size_limit"` // if > ChunkSize double the chunk size after each chunk until reached
	ChunkStreams       int           `config:"vfs_read_chunk_streams"`    // Number of download streams to use
	CacheMode          CacheMode     `config:"vfs_cache_mode"`
	CacheMaxAge        time.Duration `config:"vfs_cache_max_age"`
	CacheMaxSize       int64         `config:"vfs_cache_max_size"`
	CacheMinFreeSpace  int64         `config:"vfs_cache_min_free_space"`
	CachePollInterval  time.Duration `config:"vfs_cache_poll_interval"`
	CaseInsensitive    bool          `config:"vfs_case_insensitive"`
	BlockNormDupes     bool          `config:"vfs_block_norm_dupes"`
	WriteWait          time.Duration `config:"vfs_write_wait"`       // time to wait for in-sequence write
	ReadWait           time.Duration `config:"vfs_read_wait"`        // time to wait for in-sequence read
	WriteBack          time.Duration `config:"vfs_write_back"`       // time to wait before writing back dirty files
	ReadAhead          int64         `config:"vfs_read_ahead"`       // bytes to read ahead in cache mode "full"
	UsedIsSize         bool          `config:"vfs_used_is_size"`     // if true, use the `rclone size` algorithm for Used size
	FastFingerprint    bool          `config:"vfs_fast_fingerprint"` // if set use fast fingerprints
	DiskSpaceTotalSize int64         `config:"vfs_disk_space_total_size"`
	HandleCaching      time.Duration `config:"vfs_handle_caching"`     // time to keep handle alive after last close
	CacheDir           string        `config:"cache_dir"`              // path to the cache directory on local disk
	MetadataExtension  string        `config:"vfs_metadata_extension"` // if set respond to files with this extension with metadata

	// Logger is the logging backend. If nil, all log output is suppressed.
	Logger Logger
}

// Opt is the default options modified by the environment variables and command line flags
var Opt = Options{
	DirCacheTime:       5 * 60 * time.Second,
	PollInterval:       time.Minute,
	CachePollInterval:  60 * time.Second,
	CacheMaxAge:        3600 * time.Second,
	CacheMaxSize:       -1,
	CacheMinFreeSpace:  -1,
	ChunkSize:          128 * mebi,
	ChunkSizeLimit:     -1,
	WriteWait:          1000 * time.Millisecond,
	ReadWait:           20 * time.Millisecond,
	WriteBack:          5 * time.Second,
	ReadAhead:          0,
	DiskSpaceTotalSize: -1,
	HandleCaching:      5 * time.Second,
	DirPerms:           FileMode(0777),
	FilePerms:          FileMode(0666),
	LinkPerms:          FileMode(0666),
	CaseInsensitive:    runtime.GOOS == "windows" || runtime.GOOS == "darwin",
}

// Init the options, making sure everything is within range
func (opt *Options) Init() {
	// Set default logger if none provided
	if opt.Logger == nil {
		opt.Logger = NopLogger()
	}

	// Mask the permissions with the umask
	opt.DirPerms &= ^opt.Umask
	opt.FilePerms &= ^opt.Umask
	opt.LinkPerms &= ^opt.Umask

	// Make sure directories are returned as directories
	opt.DirPerms |= FileMode(os.ModeDir)

	// Make sure links are returned as links
	opt.LinkPerms |= FileMode(os.ModeSymlink)
}
