package keepcurrent

import (
	"bytes"
	"errors"
	"io"
	"time"
)

var errNotModified = errors.New("unmodified")

// Package keepcurrent periodically poll from the source and if it's changed,
// sync with a set of destinations

type Source interface {
	Fetch() (io.ReadCloser, error)
}
type Sink interface {
	UpdateFrom(io.Reader) error
	String() string
}

type Runner struct {
	source        Source
	sinks         []Sink
	OnSourceError func(error)
	OnSinkError   func(Sink, error)
}

func New(from Source, to ...Sink) *Runner {
	return &Runner{from, to, func(error) {}, func(Sink, error) {}}
}

func (runner *Runner) InitFrom(s Source) {
}

func (runner *Runner) Start(interval time.Duration) func() {
	if len(runner.sinks) == 0 {
		return func() {}
	}
	tk := time.NewTicker(interval)
	ch := make(chan struct{})
	go func() {
		for {
			rc, err := runner.source.Fetch()
			if err != nil {
				runner.OnSourceError(err)
				continue
			}
			defer rc.Close()
			if len(runner.sinks) == 1 {
				s := runner.sinks[0]
				if err := s.UpdateFrom(rc); err != nil {
					runner.OnSinkError(s, err)
				}
			} else {
				var w bytes.Buffer
				var r io.Reader
				for i, s := range runner.sinks {
					if i == 0 {
						r = io.TeeReader(rc, &w)
					}
					if err := s.UpdateFrom(r); err != nil {
						runner.OnSinkError(s, err)
					}
					r = bytes.NewBuffer(w.Bytes())
				}
			}
			select {
			case <-ch:
				tk.Stop()
				return
			case <-tk.C:
			}
		}
	}()
	return func() { close(ch) }
}
