package keepcurrent

import (
	"bytes"
	"io"
)

// maxPreAlloc caps how much readAll will allocate up front from a reader's
// self-reported size. It guards against a bogus or hostile size (e.g. a wildly
// inflated HTTP Content-Length) turning into a giant make() that panics or
// OOMs; anything larger falls back to io.ReadAll, which grows against the bytes
// actually delivered. The bound sits comfortably above the payloads keepcurrent
// syncs in practice (a MaxMind database is well under 100MB).
const maxPreAlloc = 256 << 20 // 256 MiB

// readAll reads r to EOF into a single buffer. It differs from io.ReadAll only
// in that, when r can report how many bytes remain, it allocates that buffer up
// front. io.ReadAll grows its buffer by repeatedly appending and reallocating,
// so reading an N-byte payload churns through a sequence of ever-larger backing
// arrays (N/2, 3N/4, N, ...) that all become garbage. For the multi-megabyte
// payloads keepcurrent is built to sync (e.g. a ~75MB MaxMind database) that
// transient churn dominates memory on small hosts. Pre-sizing turns the read
// into a single allocation.
func readAll(r io.Reader) ([]byte, error) {
	if n, ok := knownSize(r); ok && n >= 0 && n <= maxPreAlloc {
		// +bytes.MinRead leaves room for the final zero-byte read that signals
		// EOF, so an exactly-sized payload never forces ReadFrom to reallocate.
		buf := bytes.NewBuffer(make([]byte, 0, int(n)+bytes.MinRead))
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
