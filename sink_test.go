package keepcurrent

import (
	"bytes"
	"testing"
)

// Regression test for the "send on closed channel" crash: the channel is owned
// by the caller and may be closed (shutdown / config reload) while a Runner is
// mid-sync. UpdateFrom must surface that as an error rather than panicking and
// crashing the whole process.
func TestByteChannelClosedDoesNotPanic(t *testing.T) {
	ch := make(chan []byte)
	close(ch)
	s := ToChannel(ch)
	err := s.UpdateFrom(bytes.NewReader([]byte("hello")))
	if err == nil {
		t.Fatal("expected an error sending to a closed channel, got nil")
	}
}

// A healthy send still delivers the data unchanged.
func TestByteChannelDelivers(t *testing.T) {
	ch := make(chan []byte, 1)
	s := ToChannel(ch)
	if err := s.UpdateFrom(bytes.NewReader([]byte("hello"))); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := <-ch; string(got) != "hello" {
		t.Fatalf("got %q, want %q", got, "hello")
	}
}
