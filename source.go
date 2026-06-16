package keepcurrent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"sync"
	"time"

	"github.com/mholt/archives"
)

type webSource struct {
	url    string
	etag   string
	mx     sync.RWMutex
	client *http.Client
}

// drainClose discards any remaining body and closes it. net/http only returns a
// connection to the keep-alive pool once its response body has been read to EOF,
// so error/not-modified responses we don't hand to the caller must be drained.
func drainClose(rc io.ReadCloser) {
	_, _ = io.Copy(io.Discard, rc)
	_ = rc.Close()
}

// FromWeb constructs a source from the given URL.
func FromWeb(url string) Source {
	return FromWebWithClient(url, http.DefaultClient)
}

// FromWebWithClient is the same as FromWeb but with a custom http.Client
func FromWebWithClient(url string, client *http.Client) Source {
	return &webSource{url: url, client: client}
}

// Fetch implements the Source interface
func (s *webSource) Fetch(ifNewerThan time.Time) (io.ReadCloser, error) {
	req, err := http.NewRequest(http.MethodGet, s.url, nil)
	if err != nil {
		return nil, err
	}
	if !ifNewerThan.IsZero() {
		req.Header.Add("If-Modified-Since", ifNewerThan.Format(http.TimeFormat))
	}
	if s.getETag() != "" {
		req.Header.Add("If-None-Match", s.etag)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotModified {
		// Drain to EOF then close so net/http can return the connection to the
		// pool for keep-alive reuse (it won't reuse one whose body wasn't fully
		// read). 304 carries no body, so this is effectively just a close here.
		// We hand the body to the caller only on the success path below.
		drainClose(resp.Body)
		return nil, ErrUnmodified
	}
	if resp.StatusCode != http.StatusOK {
		drainClose(resp.Body)
		return nil, fmt.Errorf("unexpected HTTP status %v", resp.StatusCode)
	}
	etag := resp.Header.Get("ETag")
	if etag != "" {
		s.setETag(etag)
	}
	if resp.ContentLength >= 0 {
		// Surface the Content-Length so the Runner can pre-size its read buffer.
		return sizedReadCloser{ReadCloser: resp.Body, n: resp.ContentLength}, nil
	}
	return resp.Body, nil
}

func (s *webSource) getETag() string {
	s.mx.RLock()
	defer s.mx.RUnlock()
	return s.etag
}

func (s *webSource) setETag(etag string) {
	s.mx.Lock()
	s.etag = etag
	s.mx.Unlock()
}

type tarGzSource struct {
	s            Source
	expectedName string
}

// FromTarGz wraps a source to decompress one specific file from the gzipped
// tarball.
func FromTarGz(s Source, expectedName string) Source {
	return &tarGzSource{s, expectedName}
}

var errFound = errors.New("found")

func (s *tarGzSource) Fetch(ifNewerThan time.Time) (io.ReadCloser, error) {
	rc, err := s.s.Fetch(ifNewerThan)
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	// archives.CompressedArchive.Extract() reads ca.Extraction (not ca.Archival),
	// and returns "no extraction format" if it's nil. Setting Archival here was a
	// bug that made every Fetch() fail silently.
	format := archives.CompressedArchive{
		Compression: archives.Gz{},
		Extraction:  archives.Tar{},
	}

	var buf []byte
	err = format.Extract(context.Background(), rc, func(ctx context.Context, info archives.FileInfo) error {
		// NameInArchive is the full stored path (e.g. "GeoLite2-City_20260116/GeoLite2-City.mmdb").
		// Callers pass the basename, so compare on basename — mirrors the behavior of the
		// archiver/v3-based implementation this replaced.
		if path.Base(info.NameInArchive) == s.expectedName {
			f, err := info.Open()
			if err != nil {
				return err
			}
			defer f.Close()
			// Wrap the entry in a size-aware reader so readAll pre-sizes the
			// buffer from the archive entry's uncompressed size — extracting a
			// large file (e.g. a ~75MB mmdb) becomes a single allocation rather
			// than the reallocation churn of io.ReadAll.
			buf, err = readAll(sizedReadCloser{ReadCloser: f, n: info.Size()})
			if err != nil {
				return err
			}
			return errFound
		}
		return nil
	})

	if errors.Is(err, errFound) {
		// Return a size-aware reader so the Runner's read can also be pre-sized.
		return bytesReadCloser(buf), nil
	}
	if err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("file %q not found in archive", s.expectedName)
}

type fileSource struct {
	path         string
	preprocessor func(io.ReadCloser) (io.ReadCloser, error)
}

// FromFile constructs a source from the given file path.
func FromFile(path string) Source {
	return &fileSource{path, nil}
}

// FromFileWithPreprocessor constructs a source from the given file path, while modifying the file data using preprocessor function
func FromFileWithPreprocessor(path string, preprocessor func(io.ReadCloser) (io.ReadCloser, error)) Source {
	return &fileSource{path, preprocessor}
}

func (s *fileSource) Fetch(ifNewerThan time.Time) (io.ReadCloser, error) {
	f, err := os.Open(s.path)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !ifNewerThan.IsZero() && ifNewerThan.Before(fi.ModTime()) {
		f.Close()
		return nil, ErrUnmodified
	}
	if s.preprocessor != nil {
		// A preprocessor may change the byte count, so we can't declare a size
		// up front; readAll falls back to io.ReadAll for the preprocessed stream.
		result, err := s.preprocessor(f)
		if err != nil {
			f.Close()
			return nil, err
		}
		return result, nil
	}
	// Surface the file size so the Runner's readAll pre-sizes its buffer instead
	// of growing by reallocation. The cached payloads read here are exactly the
	// large ones that churn — e.g. geo loads a ~75MB MaxMind mmdb from disk via
	// InitFrom(FromFile(...)) at startup.
	return sizedReadCloser{ReadCloser: f, n: fi.Size()}, nil
}
