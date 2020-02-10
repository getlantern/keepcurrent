package keepcurrent

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"io"
	"io/ioutil"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestReadWriteSameFile(t *testing.T) {
	name, content := writeTempFile(t)
	defer os.Remove(name)
	runner := New(FromFile(name), ToFile(name))
	runner.OnSourceError = func(err error) {
		assert.NoError(t, err)
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

func writeTempFile(t *testing.T) (string, []byte) {
	b := make([]byte, 1024*1024)
	_, err := rand.Read(b)
	assert.NoError(t, err)
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
	runner.OnSourceError = func(err error) {
		assert.Fail(t, "unexpected source error "+err.Error())
	}
	runner.OnSinkError = func(s Sink, err error) {
		assert.Fail(t, "unexpected sink error "+err.Error())
	}
	stop := runner.Start(10 * time.Millisecond)
	got := make(chan bool)
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
	calls        int32
	lastModified time.Time
}

func (s *byteSource) Fetch(ifNewerThan time.Time) (io.ReadCloser, error) {
	if ifNewerThan.After(s.lastModified) {
		return nil, ErrUnmodified
	}
	atomic.AddInt32(&s.calls, 1)
	return ioutil.NopCloser(bytes.NewBuffer([]byte("abcde"))), nil
}

func TestIfNewerThan(t *testing.T) {
	ch := make(chan []byte)
	s := byteSource{lastModified: time.Now()}
	runner := New(&s, ToChannel(ch))
	stop := runner.Start(10 * time.Millisecond)
	var updates int32
	go func() {
		for range ch {
			atomic.AddInt32(&updates, 1)
		}
	}()
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
