package keepcurrent

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lastModifiedServer serves body with Last-Modified mod, answering 304 to an
// If-Modified-Since at or after it, as a CDN or object store does.
func lastModifiedServer(t *testing.T, mod time.Time, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ims, err := http.ParseTime(r.Header.Get("If-Modified-Since")); err == nil && !mod.After(ims) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Last-Modified", mod.UTC().Format(http.TimeFormat))
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A runner started from a cached file older than the web copy fetches the web
// copy, even when the process starts after the web copy was published. Before,
// InitFrom recorded the start time as the data's age, so the first web check
// asked only for data newer than the start, got 304, and kept the old file
// until the web copy changed again.
func TestInitFromOlderFileFetchesNewerWebCopy(t *testing.T) {
	published := time.Now().Add(-time.Hour).Truncate(time.Second)
	srv := lastModifiedServer(t, published, "new")

	path := filepath.Join(t.TempDir(), "db")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0644))
	cached := published.Add(-24 * time.Hour)
	require.NoError(t, os.Chtimes(path, cached, cached))

	ch := make(chan []byte, 4)
	runner := New(FromWeb(srv.URL), ToFile(path), ToChannel(ch))
	runner.InitFrom(FromFile(path))
	assert.Equal(t, "old", string(<-ch), "InitFrom loads the cached file")
	stop := runner.Start(time.Hour)
	defer stop()

	select {
	case got := <-ch:
		assert.Equal(t, "new", string(got), "the first web check fetches the newer copy")
	case <-time.After(5 * time.Second):
		t.Fatal("the runner never fetched the web copy published after its cached file")
	}
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "new", string(b), "and saves it")
}

// A runner started from a cached file at least as new as the web copy doesn't
// fetch it again.
func TestInitFromCurrentFileSkipsTheWebCopy(t *testing.T) {
	published := time.Now().Add(-time.Hour).Truncate(time.Second)
	srv := lastModifiedServer(t, published, "new")

	path := filepath.Join(t.TempDir(), "db")
	require.NoError(t, os.WriteFile(path, []byte("cached"), 0644))
	cached := published.Add(time.Minute)
	require.NoError(t, os.Chtimes(path, cached, cached))

	ch := make(chan []byte, 4)
	runner := New(FromWeb(srv.URL), ToFile(path), ToChannel(ch))
	runner.InitFrom(FromFile(path))
	assert.Equal(t, "cached", string(<-ch))
	stop := runner.Start(time.Hour)
	time.Sleep(200 * time.Millisecond)
	stop()
	select {
	case got := <-ch:
		t.Fatalf("fetched %q although the cached file is current", got)
	default:
	}
}

// InitFrom doesn't write the file it read back to itself, so the file keeps
// the age that tells the next runner whether it is current.
func TestInitFromKeepsTheCachedFilesAge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	require.NoError(t, os.WriteFile(path, []byte("cached"), 0644))
	cached := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	require.NoError(t, os.Chtimes(path, cached, cached))

	ch := make(chan []byte, 1)
	runner := New(FromWeb("http://127.0.0.1:1/unused"), ToFile(path), ToChannel(ch))
	runner.InitFrom(FromFile(path))
	assert.Equal(t, "cached", string(<-ch))

	fi, err := os.Stat(path)
	require.NoError(t, err)
	assert.True(t, fi.ModTime().Equal(cached), "modtime %v, want %v", fi.ModTime(), cached)
}

// If-Modified-Since is the cutoff in GMT, whatever the host's time zone.
func TestWebSourceSendsIfModifiedSinceInGMT(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("If-Modified-Since")
		w.WriteHeader(http.StatusNotModified)
	}))
	t.Cleanup(srv.Close)
	cutoff := time.Date(2026, 10, 8, 17, 15, 0, 0, time.FixedZone("UTC-6", -6*3600))
	_, err := FromWeb(srv.URL).Fetch(cutoff)
	assert.ErrorIs(t, err, ErrUnmodified)
	assert.Equal(t, "Thu, 08 Oct 2026 23:15:00 GMT", got)
}

// A sink that preprocesses still writes to the file the source read, since
// its output can differ from what was read.
func TestInitFromStillWritesAPreprocessedCopyInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	require.NoError(t, os.WriteFile(path, []byte("cached"), 0644))
	upper := func(r io.Reader) (io.Reader, error) {
		b, err := io.ReadAll(r)
		return strings.NewReader(strings.ToUpper(string(b))), err
	}
	runner := New(FromWeb("http://127.0.0.1:1/unused"), ToFileWithPreprocessor(path, upper))
	runner.InitFrom(FromFile(path))
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "CACHED", string(b))
}

// A cached file dated in the future says nothing about its age, so the first
// web check is unconditional and fetches a copy published before that date.
func TestInitFromFutureDatedFileFetchesTheWebCopy(t *testing.T) {
	published := time.Now().Add(-time.Hour).Truncate(time.Second)
	srv := lastModifiedServer(t, published, "new")

	path := filepath.Join(t.TempDir(), "db")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0644))
	future := time.Now().Add(48 * time.Hour)
	require.NoError(t, os.Chtimes(path, future, future))

	ch := make(chan []byte, 4)
	runner := New(FromWeb(srv.URL), ToFile(path), ToChannel(ch))
	runner.InitFrom(FromFile(path))
	assert.Equal(t, "old", string(<-ch))
	stop := runner.Start(time.Hour)
	defer stop()
	select {
	case got := <-ch:
		assert.Equal(t, "new", string(got))
	case <-time.After(5 * time.Second):
		t.Fatal("a future-dated cached file kept the runner from fetching the web copy")
	}
}

// A sink that reaches the source's file through a symlink is the same file, so
// InitFrom doesn't rewrite it either.
func TestInitFromKeepsTheAgeThroughASymlink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "db")
	link := filepath.Join(dir, "db-link")
	require.NoError(t, os.WriteFile(path, []byte("cached"), 0644))
	require.NoError(t, os.Symlink(path, link))
	cached := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	require.NoError(t, os.Chtimes(path, cached, cached))

	ch := make(chan []byte, 1)
	runner := New(FromWeb("http://127.0.0.1:1/unused"), ToFile(link), ToChannel(ch))
	runner.InitFrom(FromFile(path))
	assert.Equal(t, "cached", string(<-ch))

	fi, err := os.Lstat(link)
	require.NoError(t, err)
	assert.NotZero(t, fi.Mode()&os.ModeSymlink, "the link is still a link")
	fi, err = os.Stat(path)
	require.NoError(t, err)
	assert.True(t, fi.ModTime().Equal(cached), "modtime %v, want %v", fi.ModTime(), cached)
}
