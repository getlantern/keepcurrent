package keepcurrent

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"io"
	"io/ioutil"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestReadWriteSameFile(t *testing.T) {
	b := make([]byte, 1024*1024)
	_, err := rand.Read(b)
	assert.NoError(t, err)

	name, content := writeTempFile(t, b)
	defer os.Remove(name)
	runner := New(FromFile(name), ToFile(name))
	runner.OnSourceError = func(err error, tries int) time.Duration {
		assert.Equal(t, 1, tries)
		assert.NoError(t, err)
		return 0
	}
	runner.OnSinkError = func(s Sink, err error) {
		assert.NoError(t, err)
	}
	stop := runner.Start(10 * time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	stop()
	b, err = ioutil.ReadFile(name)
	assert.NoError(t, err)
	assert.Equal(t, b, content)
}

func TestReadWriteWithPreprocessor(t *testing.T) {
	rot13 := func(r rune) rune {
		if r >= 'a' && r <= 'z' {
			// Rotate lowercase letters 13 places.
			if r >= 'm' {
				return r - 13
			} else {
				return r + 13
			}
		} else if r >= 'A' && r <= 'Z' {
			// Rotate uppercase letters 13 places.
			if r >= 'M' {
				return r - 13
			} else {
				return r + 13
			}
		}
		// Do nothing.
		return r
	}
	name, content := writeTempFile(t, []byte(strings.Map(rot13, "test input")))
	defer os.Remove(name)
	preWrite := func(r io.Reader) (io.Reader, error) {
		b, _ := ioutil.ReadAll(r)
		s := strings.Map(rot13, string(b))
		return strings.NewReader(s), nil
	}

	postRead := func(r io.ReadCloser) (io.ReadCloser, error) {
		b, _ := ioutil.ReadAll(r)
		s := strings.Map(rot13, string(b))
		return ioutil.NopCloser(strings.NewReader(s)), nil
	}

	runner := New(FromFileWithPreprocessor(name, postRead), ToFileWithPreprocessor(name, preWrite))
	runner.OnSourceError = func(err error, tries int) time.Duration {
		assert.Equal(t, 1, tries)
		assert.NoError(t, err)
		return 0
	}
	runner.OnSinkError = func(s Sink, err error) {
		assert.NoError(t, err)
	}
	stop := runner.Start(10 * time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	stop()
	b, err := ioutil.ReadFile(name)
	assert.NoError(t, err)
	assert.Equal(t, b, content)
}

func writeTempFile(t *testing.T, b []byte) (string, []byte) {
	f, err := ioutil.TempFile(os.TempDir(), "keep_current_test")
	assert.NoError(t, err)
	_, err = f.Write(b)
	assert.NoError(t, err)
	f.Close()
	return f.Name(), b
}

func TestUpdateFromWeb(t *testing.T) {
	ch := make(chan []byte)
	url := "https://httpbin.org/get"
	runner := New(FromWeb(url), ToChannel(ch))
	runner.OnSourceError = func(err error, tries int) time.Duration {
		assert.Fail(t, "unexpected source error "+err.Error())
		return 0
	}
	runner.OnSinkError = func(s Sink, err error) {
		assert.Fail(t, "unexpected sink error "+err.Error())
	}
	stop := runner.Start(10 * time.Second)
	got := make(chan bool, 1)
	go func() {
		for b := range ch {
			data := make(map[string]interface{})
			if assert.NoError(t, json.Unmarshal(b, &data)) {
				assert.EqualValues(t, data["url"], url)
				got <- true
			}
		}
	}()
	<-got
	stop()
}

type byteSource struct {
	remainingFailures int32
	calls             int32
	lastModified      time.Time
}

func (s *byteSource) Fetch(ifNewerThan time.Time) (io.ReadCloser, error) {
	if ifNewerThan.After(s.lastModified) {
		return nil, ErrUnmodified
	}
	atomic.AddInt32(&s.calls, 1)
	if atomic.AddInt32(&s.remainingFailures, -1) > 0 {
		// keeps failing with io.ErrUnexpectedEOF
		return ioutil.NopCloser(s), nil
	}
	return ioutil.NopCloser(bytes.NewBuffer([]byte("abcde"))), nil
}

func (s *byteSource) Read(p []byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func TestIfNewerThan(t *testing.T) {
	ch := make(chan []byte)
	var updates int32
	go func() {
		for range ch {
			atomic.AddInt32(&updates, 1)
		}
	}()

	s := byteSource{lastModified: time.Now()}
	runner := New(&s, ToChannel(ch))
	stop := runner.Start(10 * time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	stop()
	assert.EqualValues(t, 1, atomic.LoadInt32(&s.calls))
	assert.EqualValues(t, 1, atomic.LoadInt32(&updates))

	// Now make sure it does not fetch the source if it's not newer than what
	// got from InitFrom.
	runner = New(&s, ToChannel(ch))
	runner.InitFrom(&s)
	assert.EqualValues(t, 2, atomic.LoadInt32(&s.calls))
	stop = runner.Start(10 * time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	stop()
	assert.EqualValues(t, 2, atomic.LoadInt32(&s.calls))
}

func TestBackoffOnFail(t *testing.T) {
	ch := make(chan []byte)
	var updates int32
	go func() {
		for range ch {
			atomic.AddInt32(&updates, 1)
		}
	}()

	s := byteSource{lastModified: time.Now(), remainingFailures: 5}
	runner := New(&s, ToChannel(ch))
	var finalFailures int32
	runner.OnSourceError = ExpBackoffThenFail(time.Millisecond, 3, func(err error) {
		atomic.AddInt32(&finalFailures, 1)
	})
	stop := runner.Start(100 * time.Millisecond)
	defer stop()
	time.Sleep(50 * time.Millisecond)
	// The first synchronization should have failed
	assert.EqualValues(t, 3, atomic.LoadInt32(&s.calls))
	assert.EqualValues(t, 0, atomic.LoadInt32(&updates))
	assert.EqualValues(t, 1, atomic.LoadInt32(&finalFailures))
	time.Sleep(150 * time.Millisecond)
	// The second round of synchronization should have completed
	assert.EqualValues(t, 5, atomic.LoadInt32(&s.calls))
	assert.EqualValues(t, 1, atomic.LoadInt32(&updates))
	assert.EqualValues(t, 1, atomic.LoadInt32(&finalFailures))
}
