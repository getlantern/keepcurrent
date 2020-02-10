package keepcurrent

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/mholt/archiver"
)

var errNotFoundInArchive = errors.New("file not found in archive")

type webSource struct {
	url          string
	etag         string
	lastModified time.Time
	mx           sync.RWMutex
	client       *http.Client
}

func FromWeb(url string) Source {
	return FromWebWithClient(url, http.DefaultClient)
}

func FromWebWithClient(url string, client *http.Client) Source {
	return &webSource{url: url, client: client}
}

func (s *webSource) Fetch() (io.ReadCloser, error) {
	req, err := http.NewRequest(http.MethodGet, s.url, nil)
	if err != nil {
		return nil, err
	}
	if !s.getLastModified().IsZero() {
		req.Header.Add("If-Modified-Since", s.lastModified.Format(http.TimeFormat))
	}
	if s.getETag() != "" {
		req.Header.Add("Etag", s.etag)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotModified {
		return nil, errNotModified
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Unexpected HTTP status %v", resp.StatusCode)
	}
	etag := resp.Header.Get("ETag")
	if etag != "" {
		s.SetETag(etag)
	}
	return resp.Body, nil
}

func (s *webSource) getETag() string {
	s.mx.RLock()
	defer s.mx.RUnlock()
	return s.etag
}

func (s *webSource) getLastModified() time.Time {
	s.mx.RLock()
	defer s.mx.RUnlock()
	return s.lastModified
}

func (s *webSource) SetETag(etag string) {
	s.mx.Lock()
	s.etag = etag
	s.mx.Unlock()
}

func (s *webSource) SetLastModified(t time.Time) {
	s.mx.Lock()
	s.lastModified = t
	s.mx.Unlock()
}

type tarGzSource struct {
	s            Source
	expectedName string
}

func TarGz(s Source, expectedName string) Source {
	return &tarGzSource{s, expectedName}
}

func (s *tarGzSource) Fetch() (io.ReadCloser, error) {
	rc, err := s.s.Fetch()
	if err != nil {
		return nil, err
	}
	unzipper := archiver.NewTarGz()
	if err := unzipper.Open(rc, 0); err != nil {
		return nil, err
	}
	for {
		f, err := unzipper.Read()
		if err != nil {
			return nil, err
		}
		if f.Name() == s.expectedName {
			return chainedCloser{f, rc}, nil
		}
	}
	return nil, errNotFoundInArchive
}

type chainedCloser []io.ReadCloser

func (cc chainedCloser) Read(p []byte) (n int, err error) {
	return cc[0].Read(p)
}

func (cc chainedCloser) Close() error {
	var lastError error
	for _, c := range cc {
		if err := c.Close(); err != nil {
			lastError = err
		}
	}
	return lastError
}

type fileSource struct {
	path string
}

func FromFile(path string) Source {
	return &fileSource{path}
}

func (s *fileSource) Fetch() (io.ReadCloser, error) {
	f, err := os.Open(s.path)
	if err != nil {
		return nil, err
	}
	return f, nil
}
