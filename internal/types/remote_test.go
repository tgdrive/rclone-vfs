package types

import (
	"context"
	"io"
	"testing"
)

// ---------------------------------------------------------------------------
// Compile-time interface checks
// ---------------------------------------------------------------------------

// compileCheckOpenOption verifies that *RangeOption satisfies OpenOption.
var compileCheckOpenOption OpenOption = (*RangeOption)(nil)

// compileCheckRemoteObject is a compile-time check that our test struct
// satisfies RemoteObject, proving the interface is properly defined.
type testRemoteObject struct{}

func (t *testRemoteObject) Open(_ context.Context, _ ...OpenOption) (io.ReadCloser, error) {
	return nil, nil
}
func (t *testRemoteObject) Size() int64    { return 0 }
func (t *testRemoteObject) String() string { return "" }

var compileCheckRemoteObject RemoteObject = (*testRemoteObject)(nil)

// ---------------------------------------------------------------------------
// RangeOption.Header
// ---------------------------------------------------------------------------

func TestRangeOptionHeaderStartOnly(t *testing.T) {
	o := &RangeOption{Start: 100, End: -1}
	key, value := o.Header()
	if key != "Range" {
		t.Fatalf("expected key %q, got %q", "Range", key)
	}
	want := "bytes=100-"
	if value != want {
		t.Fatalf("expected value %q, got %q", want, value)
	}
}

func TestRangeOptionHeaderStartEnd(t *testing.T) {
	o := &RangeOption{Start: 100, End: 199}
	key, value := o.Header()
	if key != "Range" {
		t.Fatalf("expected key %q, got %q", "Range", key)
	}
	want := "bytes=100-199"
	if value != want {
		t.Fatalf("expected value %q, got %q", want, value)
	}
}

func TestRangeOptionHeaderZeroStart(t *testing.T) {
	o := &RangeOption{Start: 0, End: 1023}
	key, value := o.Header()
	if key != "Range" {
		t.Fatalf("expected key %q, got %q", "Range", key)
	}
	want := "bytes=0-1023"
	if value != want {
		t.Fatalf("expected value %q, got %q", want, value)
	}
}

func TestRangeOptionHeaderLargeValues(t *testing.T) {
	o := &RangeOption{Start: 0, End: -1}
	key, value := o.Header()
	if key != "Range" {
		t.Fatalf("expected key %q, got %q", "Range", key)
	}
	want := "bytes=0-"
	if value != want {
		t.Fatalf("expected value %q, got %q", want, value)
	}
}
