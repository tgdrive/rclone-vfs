# rclone-vfs

A high-performance streaming proxy server built on top of [Rclone's VFS](https://rclone.org/commands/rclone_mount/#vfs-file-caching) layer. This service allows you to stream files from any HTTP/HTTPS URL with the benefits of Rclone's smart caching, buffering, and read-ahead capabilities.

## Features

- **Rclone VFS Integration**: Leverages Rclone's robust VFS for disk caching, sparse file support, and efficient streaming.
- **Flexible Input**: Supports passing target URLs via query parameters or Base64-encoded paths.
- **Deduplication**: Optional query parameter and domain stripping to maximize cache hits for mirrored content.
- **Caddy Ready**: Includes a native Caddy module for easy integration into your web server.
- **Docker Ready**: Minimal Alpine-based Docker image.

## Getting Started

### Prerequisites

- Docker or Go 1.25+

### Installation & Run

You can run `rclone-vfs` directly using Docker:

```bash
docker run -d \
  -p 8080:8080 \
  -v /path/to/host/cache:/tmp/rclone_vfs_cache \
  ghcr.io/tgdrive/rclone-vfs --cache-dir /tmp/rclone_vfs_cache
```

## CLI Flags

All Rclone VFS settings are supported. Below are the most common ones:

| Flag | Default | Description |
|------|---------|-------------|
| `--port` | `8080` | Port to listen on. |
| `--cache-dir` | System Temp | Directory to store the VFS disk cache. |
| `--cache-mode` | `off` | VFS cache mode (`off`, `minimal`, `writes`, `full`). |
| `--chunk-size` | `64M` | The chunk size for read requests. |
| `--chunk-streams` | `2` | Number of parallel streams to read at once. |
| `--max-age` | `1h` | Max age of files in the VFS cache. |
| `--max-size` | `off` | Max total size of objects in the cache. |
| `--strip-query` | `false` | If true, strips query parameters from the URL when generating the cache key. |
| `--strip-domain` | `false` | If true, strips domain and protocol from the URL. |
| `--shard-level` | `1` | Number of directory levels for sharding the cache. |

*Run `rclone-vfs --help` to see all available flags, including advanced VFS permissions and timing settings.*

## API Endpoints

### 1. Stream via Query Parameter
```http
GET /stream?url=https://example.com/video.mp4
```

### 2. Stream via Base64 Path
Useful for players that struggle with query parameters.
1. Base64 encode your URL: `https://example.com/video.mp4` -> `aHR0cHM6Ly9leGFtcGxlLmNvbS92aWRlby5tcDQ`
2. Request: `GET /stream/aHR0cHM6Ly9leGFtcGxlLmNvbS92aWRlby5tcDQ`

### 3. Providing File Size (Optimization)
By default, the proxy makes a request to the origin server to fetch the file size (metadata). You can skip this by providing the size upfront:

**Via Query Parameter:**
```http
GET /stream?url=https://example.com/video.mp4&size=1073741824
```

**Via HTTP Header:**
```http
GET /stream?url=https://example.com/video.mp4
X-File-Size: 1073741824
```

**Benefits:**
- Eliminates the metadata fetch request to the origin
- Faster time-to-first-byte
- Reduced load on the origin server

## Caddy Plugin

Build Caddy with the module:
```bash
xcaddy build --with github.com/tgdrive/rclone-vfs/caddy
```

### Caddyfile Usage
```caddyfile
:8080 {
    vfs https://upstream.com {
        cache_dir /var/cache/vfs
        cache_mode full
        max_age 24h
        strip_query
    }
}
```

### Caddyfile Directives
- `upstream` (argument): The base URL of the source server.
- `cache_dir`: Path to disk cache.
- `cache_mode`: `off`, `minimal`, `writes`, or `full`.
- `max_age`, `max_size`, `chunk_size`, `chunk_streams`.
- `strip_query`, `strip_domain`, `shard-level`.
- `read_only`, `no_seek`, `no_checksum`, etc.

## How it Works

1. **VFS Mapping**: The requested URL is mapped to a unique deterministic path in a virtual rclone file system.
2. **Streaming**: Rclone's VFS layer handles the heavy lifting—on-demand downloading, parallel chunk streaming, and local disk persistence.
3. **Efficiency**: Range requests are fully supported, allowing clients to seek through large files without downloading the entire file.
