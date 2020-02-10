package keepcurrent

import (
	"crypto/rand"
	"encoding/json"
	"io/ioutil"
	"os"
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
	stop := runner.Start(100 * time.Millisecond)
	time.Sleep(time.Second)
	stop()
	b, err := ioutil.ReadFile(name)
	assert.NoError(t, err)
	assert.Equal(t, b, content)
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
	stop := runner.Start(100 * time.Millisecond)
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
