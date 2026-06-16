package keepcurrent

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
)

type fileSink struct {
	path         string
	preprocessor func(io.Reader) (io.Reader, error)
}

// ToFile constructs a sink from the given file path. Writing to the file while
// reading from it (via FromFile) won't corrupt the file.
func ToFile(path string) Sink {
	return &fileSink{path, nil}
}

// ToFileWithPreprocessor constructs a sink from the given file path while modifying the data before writing to disk.
func ToFileWithPreprocessor(path string, preprocessor func(io.Reader) (io.Reader, error)) Sink {
	return &fileSink{path, preprocessor}
}

func (s *fileSink) UpdateFrom(r io.Reader) error {
	tmpFile, err := ioutil.TempFile("", "")
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			tmpFile.Close()
		}
	}()
	defer os.Remove(tmpFile.Name())

	err = os.Chmod(tmpFile.Name(), 0666)
	if err != nil {
		return err
	}

	if s.preprocessor != nil {
		r, err = s.preprocessor(r)
		if err != nil {
			return err
		}
	}
	_, err = io.Copy(tmpFile, r)
	if err != nil {
		return err
	}

	err = tmpFile.Close()
	if err != nil {
		return err
	}

	return os.Rename(tmpFile.Name(), s.path)
}

func (s *fileSink) String() string {
	return "file sink to " + s.path
}

type byteChannel struct {
	ch chan []byte
}

// ToChannel constructs a sink which sends all data to the given channel.
func ToChannel(ch chan []byte) Sink {
	return &byteChannel{ch}
}

func (s *byteChannel) UpdateFrom(r io.Reader) (err error) {
	// The channel is owned by the caller (see ToChannel) and may be closed
	// concurrently — e.g. on shutdown or config reload — while a Runner is
	// mid-sync. A send on a closed channel panics, which would crash the whole
	// process; recover *only that specific panic* and surface it as a sink
	// error instead (Runner reports sink errors via OnSinkError rather than
	// dying). Any other panic is re-raised so genuine bugs still surface.
	defer func() {
		if rec := recover(); rec != nil {
			if e, ok := rec.(error); ok && e.Error() == "send on closed channel" {
				err = fmt.Errorf("byte channel sink: %w", e)
				return
			}
			panic(rec)
		}
	}()
	// readAll pre-sizes from the reader's length (the Runner hands us a
	// *bytes.Reader), so this copy is a single allocation. We deliberately copy
	// rather than forward the Runner's buffer: ToChannel's contract is that each
	// delivered slice is independently owned, so consumers may retain or mutate
	// it freely.
	b, err := readAll(r)
	if err != nil {
		return err
	}
	s.ch <- b
	return nil
}

func (s *byteChannel) String() string {
	return "byte channel"
}
