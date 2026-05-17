package chunkedreader

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tgdrive/rclone-vfs/vfs/vfscommon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testRemote implements vfscommon.RemoteObject backed by a byte slice
type testRemote struct {
	data    []byte
	size    int64
	modTime time.Time
	server  *httptest.Server
}

func newTestRemote(data []byte) *testRemote {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
		w.Header().Set("Accept-Ranges", "bytes")

		rangeHeader := r.Header.Get("Range")
		if rangeHeader == "" {
			w.Write(data)
			return
		}

		var start, end int64
		n, _ := fmt.Sscanf(rangeHeader, "bytes=%d-%d", &start, &end)
		if n == 1 {
			end = int64(len(data)) - 1
		}
		if end >= int64(len(data)) {
			end = int64(len(data)) - 1
		}

		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(data[start : end+1])
	}))

	return &testRemote{
		data:    data,
		size:    int64(len(data)),
		modTime: time.Now(),
		server:  server,
	}
}

func (r *testRemote) String() string { return r.server.URL }

func (r *testRemote) Size() int64 { return r.size }

func (r *testRemote) Open(ctx context.Context, options ...vfscommon.OpenOption) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", r.server.URL, nil)
	if err != nil {
		return nil, err
	}
	for _, opt := range options {
		if opt == nil {
			continue
		}
		k, v := opt.Header()
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		resp.Body.Close()
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}
	return resp.Body, nil
}

func (r *testRemote) Close() {
	r.server.Close()
}

func TestNewSequentialFullRead(t *testing.T) {
	data := []byte("hello world this is a test of the chunked reader")
	remote := newTestRemote(data)
	defer remote.Close()

	cr := New(context.Background(), remote, 10, 50, 1)
	out, err := io.ReadAll(cr)
	require.NoError(t, err)
	assert.Equal(t, data, out)
}

func TestNewParallelFullRead(t *testing.T) {
	data := make([]byte, 100000)
	for i := range data {
		data[i] = byte(i % 256)
	}
	remote := newTestRemote(data)
	defer remote.Close()

	cr := New(context.Background(), remote, 10000, 50000, 4)
	out, err := io.ReadAll(cr)
	require.NoError(t, err)
	assert.Equal(t, data, out)
}

func TestSeekAndRead(t *testing.T) {
	data := []byte("0123456789abcdefghij")
	remote := newTestRemote(data)
	defer remote.Close()

	cr := New(context.Background(), remote, 5, 20, 1)

	// Read from offset 5
	n, err := cr.Seek(5, io.SeekStart)
	require.NoError(t, err)
	assert.Equal(t, int64(5), n)

	buf := make([]byte, 5)
	rn, err := io.ReadFull(cr, buf)
	require.NoError(t, err)
	assert.Equal(t, 5, rn)
	assert.Equal(t, []byte("56789"), buf)
}

func TestReadAtSpecificOffset(t *testing.T) {
	data := []byte("abcdefghijklmnopqrstuvwxyz")
	remote := newTestRemote(data)
	defer remote.Close()

	cr := New(context.Background(), remote, -1, -1, 1)

	// Seek to offset 10 and read 5 bytes
	n, err := cr.Seek(10, io.SeekStart)
	require.NoError(t, err)
	assert.Equal(t, int64(10), n)

	buf := make([]byte, 5)
	rn, err := io.ReadFull(cr, buf)
	require.NoError(t, err)
	assert.Equal(t, 5, rn)
	assert.Equal(t, []byte("klmno"), buf)
}

func TestEOF(t *testing.T) {
	data := []byte("short")
	remote := newTestRemote(data)
	defer remote.Close()

	cr := New(context.Background(), remote, -1, -1, 1)
	out, err := io.ReadAll(cr)
	require.NoError(t, err)
	assert.Equal(t, data, out)
}

func TestRangeSeek(t *testing.T) {
	data := []byte("0123456789")
	remote := newTestRemote(data)
	defer remote.Close()

	cr := New(context.Background(), remote, -1, -1, 1)

	// RangeSeek to offset 3 with length 4
	n, err := cr.RangeSeek(context.Background(), 3, io.SeekStart, 4)
	require.NoError(t, err)
	assert.Equal(t, int64(3), n)

	buf := make([]byte, 4)
	rn, err := io.ReadFull(cr, buf)
	require.NoError(t, err)
	assert.Equal(t, 4, rn)
	assert.Equal(t, []byte("3456"), buf)
}

func TestSequentialChunkGrowth(t *testing.T) {
	data := make([]byte, 1000)
	for i := range data {
		data[i] = byte(i % 256)
	}
	remote := newTestRemote(data)
	defer remote.Close()

	// initial chunk 10, max 100, single stream (sequential)
	cr := New(context.Background(), remote, 10, 100, 1)
	out, err := io.ReadAll(cr)
	require.NoError(t, err)
	assert.Equal(t, data, out)
}
