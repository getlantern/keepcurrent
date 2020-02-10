// Package keepcurrent periodically poll from the source and if it's changed,
// sync with a set of destinations

package keepcurrent

import (
	"bytes"
	"errors"
	"io"
	"time"
)

// ErrUnmodified is the error to return when a source is not modified after
// the given time.
var ErrUnmodified = errors.New("unmodified")

// Source represents somewhere any data can be fetched from
type Source interface {
	// Fetch fetches the data from the source if modified after the given time.
	Fetch(ifNewerThan time.Time) (io.ReadCloser, error)
}

// Sink represents somewhere the data can be written to
type Sink interface {
	UpdateFrom(io.Reader) error
	String() string
}

// Runner runs the logic to synchronizes data from the source to the sinks
type Runner struct {
	// If given, OnSourceError is called for any error fetching from the source
	OnSourceError func(error)
	// If given, OnSinkError is called for any error writing to any of the sinks
	OnSinkError func(Sink, error)

	source      Source
	sinks       []Sink
	lastUpdated time.Time
}

// New construct a runner which synchronizes data from one source to one or more sinks
func New(from Source, to ...Sink) *Runner {
	return &Runner{func(error) {}, func(Sink, error) {}, from, to, time.Time{}}
}

// InitFrom synchronizes data from the given source to configured sinks.
func (runner *Runner) InitFrom(s Source) {
	if len(runner.sinks) == 0 {
		return
	}
	runner.syncOnce(s)
}

// Start starts a loop to actually synchronizes data with given interval.  It
// returns a function to stop the loop.
func (runner *Runner) Start(interval time.Duration) func() {
	if len(runner.sinks) == 0 {
		return func() {}
	}
	tk := time.NewTicker(interval)
	ch := make(chan struct{})
	go func() {
		for {
			runner.syncOnce(runner.source)
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

func (runner *Runner) syncOnce(from Source) {
	start := time.Now()
	rc, err := from.Fetch(runner.lastUpdated)
	if err == ErrUnmodified {
		return
	}
	if err != nil {
		runner.OnSourceError(err)
		return
	}
	runner.lastUpdated = start
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
}
