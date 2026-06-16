package keepcurrent

import (
	"bytes"
	"io"
)

// readAll reads r to EOF into a single buffer. It differs from io.ReadAll only
// in that, when r can report how many bytes remain, it allocates that buffer up
// front. io.ReadAll grows its buffer by repeatedly appending and reallocating,
// so reading an N-byte payload churns through a sequence of ever-larger backing
// arrays (N/2, 3N/4, N, ...) that all become garbage. For the multi-megabyte
// payloads keepcurrent is built to sync (e.g. a ~75MB MaxMind database) that
// transient churn dominates memory on small hosts. Pre-sizing turns the read
// into a single allocation.
func readAll(r io.Reader) ([]byte, error) {
	if n, ok := knownSize(r); ok && n >= 0 {
		// +bytes.MinRead leaves room for the final zero-byte read that signals
		// EOF, so an exactly-sized payload never forces ReadFrom to reallocate.
		buf := bytes.NewBuffer(make([]byte, 0, n+bytes.MinRead))
		_, err := buf.ReadFrom(r)
		return buf.Bytes(), err
	}
	return io.ReadAll(r)
}

// knownSize reports the number of bytes remaining in r when r can tell us. It
// recognises the sized readers keepcurrent constructs internally (sizedReadCloser)
// as well as the standard in-memory readers (*bytes.Reader, *bytes.Buffer,
// *strings.Reader) whose Len() reports the unread remainder.
func knownSize(r io.Reader) (int64, bool) {
	switch v := r.(type) {
	case interface{ size() int64 }:
		return v.size(), true
	case interface{ Len() int }:
		return int64(v.Len()), true
	}
	return 0, false
}

// sizedReadCloser couples a ReadCloser with the total number of bytes it will
// yield, so readAll can pre-size its buffer. keepcurrent wraps HTTP bodies
// (Content-Length) and extracted archive entries in this.
type sizedReadCloser struct {
	io.ReadCloser
	n int64
}

func (s sizedReadCloser) size() int64 { return s.n }

// bytesReadCloser wraps an in-memory payload in a size-aware ReadCloser.
func bytesReadCloser(b []byte) io.ReadCloser {
	return sizedReadCloser{ReadCloser: io.NopCloser(bytes.NewReader(b)), n: int64(len(b))}
}

// bytesConsumer is an optional optimization a Sink may implement to receive the
// payload the Runner has already buffered, instead of being handed an io.Reader
// that it would have to read into a second full copy. The Runner does not retain
// or reuse the slice after the call, so a single sink may take ownership; when a
// caller wires up multiple sinks they share one backing array, so consumers must
// treat the bytes as read-only.
type bytesConsumer interface {
	updateFromBytes(b []byte) error
}
