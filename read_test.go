package keepcurrent

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// unsizedReader hides any size its wrapped reader might otherwise expose (it
// implements only Read), forcing readAll down the io.ReadAll fallback path.
type unsizedReader struct{ r io.Reader }

func (u *unsizedReader) Read(p []byte) (int, error) { return u.r.Read(p) }

func TestReadAllPreSizesKnownReaders(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 1<<20) // 1 MiB

	// *bytes.Reader reports Len(), so readAll should allocate exactly once and
	// not overshoot the way io.ReadAll's doubling does.
	got, err := readAll(bytes.NewReader(payload))
	require.NoError(t, err)
	assert.Equal(t, payload, got)
	assert.Equalf(t, len(payload)+bytes.MinRead, cap(got),
		"buffer for a sized reader should be pre-allocated to the payload size, not grown")

	// sizedReadCloser (what the web/tar.gz sources return) is also recognised.
	got, err = readAll(bytesReadCloser(payload))
	require.NoError(t, err)
	assert.Equal(t, payload, got)
	assert.Equal(t, len(payload)+bytes.MinRead, cap(got))
}

func TestReadAllFallsBackForUnsizedReaders(t *testing.T) {
	payload := bytes.Repeat([]byte("y"), 4096)
	// An opaque reader exposes no size; readAll must still return the full data.
	got, err := readAll(&unsizedReader{bytes.NewReader(payload)})
	require.NoError(t, err)
	assert.Equal(t, payload, got)
}

func TestReadAllFallsBackWhenSizeExceedsCap(t *testing.T) {
	// A reader that reports a huge size (e.g. a bogus/hostile Content-Length)
	// but only delivers a small payload. readAll must not attempt the giant
	// pre-allocation; it should fall back to io.ReadAll and still return the
	// real bytes.
	payload := bytes.Repeat([]byte("z"), 1024)
	r := sizedReadCloser{ReadCloser: io.NopCloser(bytes.NewReader(payload)), n: maxPreAlloc + 1}

	n, ok := knownSize(r)
	require.True(t, ok)
	require.Greater(t, n, int64(maxPreAlloc))

	got, err := readAll(r)
	require.NoError(t, err)
	assert.Equal(t, payload, got)
	assert.LessOrEqual(t, cap(got), maxPreAlloc, "must not pre-allocate the reported (bogus) size")
}

func TestKnownSize(t *testing.T) {
	n, ok := knownSize(bytes.NewReader(make([]byte, 42)))
	assert.True(t, ok)
	assert.EqualValues(t, 42, n)

	n, ok = knownSize(bytesReadCloser(make([]byte, 7)))
	assert.True(t, ok)
	assert.EqualValues(t, 7, n)

	_, ok = knownSize(&unsizedReader{})
	assert.False(t, ok)
}
