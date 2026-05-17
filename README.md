# varc — Range-Caching HTTP Proxy

A high-performance HTTP reverse proxy with native Range request caching. Built from the ground up for streaming media, it downloads content from upstream in parallel chunks and caches everything on disk — including byte ranges.

## Features

- **Native Range Caching**: Unlike Varnish (which passthroughs Range requests), varc caches byte ranges on disk and serves them from cache. Concurrent range requests are coalesced into a single upstream fetch.
- **Parallel Chunked Downloading**: Downloads files in parallel streams for maximum throughput on high-latency connections.
- **Disk-Backed Cache**: Sparse file support, metadata persistence, configurable max age/size.
- **Multiple Access Modes**: Standalone HTTP server, Caddy module, or Go library.
- **Flexible Cache Keys**: Optional query parameter stripping, domain stripping, hash sharding.
- **Caddy Ready**: Native Caddy module for easy integration.
- **Docker Ready**: Minimal Alpine-based Docker image.

## Getting Started

### Prerequisites

- Docker or Go 1.25+

### Installation & Run

You can run `varc` directly using Docker:

```bash
docker run -d \
  -p 8080:8080 \
  -v /path/to/host/cache:/tmp/varc_cache \
  ghcr.io/tgdrive/varc --cache-dir /tmp/varc_cache
```

### Usage

```
# Stream a file via query parameter
curl "http://localhost:8080/stream?url=https://example.com/video.mp4"

# Or Base64-encoded path
curl "http://localhost:8080/stream/aHR0cHM6Ly9leGFtcGxlLmNvbS92aWRlby5tcDQ"

# Range requests are cached natively
curl -H "Range: bytes=0-999999" "http://localhost:8080/stream?url=https://example.com/video.mp4"
```

## CLI Flags

| Flag | Default | Description |
|---|---|---|
| `--port` | `8080` | Port to listen on |
| `--cache-dir` | `$TMPDIR/varc_cache` | Cache directory |
| `--cache-mode` | `minimal` | Cache mode: `off`, `minimal`, `writes`, `full` |
| `--chunk-size` | `128M` | Chunk size for parallel downloads |
| `--chunk-streams` | `2` | Number of parallel download streams |
| `--max-age` | `1h` | Maximum cache age |
| `--max-size` | - | Maximum cache size (e.g., `10G`) |
| `--strip-query` | `false` | Strip query params from cache key |
| `--strip-domain` | `false` | Strip domain from cache key |
| `--shard-level` | `1` | Hash shard depth for cache paths |

## Caddy Module

```caddyfile
example.com {
    vfs https://upstream.example.com {
        cache_mode full
        cache_dir /data/cache
        chunk_streams 4
        max_age 24h
        max_size 50G
        strip_query
    }

    reverse_proxy localhost:8080
}
```

## Architecture

1. **Request arrives** → proxy resolves the upstream URL via query param or base64 path.
2. **Cache check** → if the file is already cached on disk, serve directly from cache.
3. **Cache miss** → file is downloaded from upstream in parallel chunks using Range requests, written to disk cache, and streamed to the client.
4. **Range requests** → if the requested range is partially cached, only the missing bytes are fetched from upstream. Fully cached ranges are served without touching the upstream.
5. **Cache cleanup** → background cleaner evicts expired or oversized entries.

## Performance

- Parallel chunked download with configurable stream count
- Sparse file support — no wasted disk for uncached ranges
- Concurrent readers don't block each other
- Metadata is persisted as JSON alongside cached data

## Development

```bash
# Build
go build -o varc .

# Run tests
go test ./...

# Cross-compile
GOOS=linux GOARCH=amd64 go build -o varc-linux-amd64 .
