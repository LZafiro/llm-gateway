package sse

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestReaderParsesEvents(t *testing.T) {
	input := ": comment\n\nevent: message_start\ndata: {\"a\":1}\n\ndata: line1\ndata: line2\n\n\n\nevent: ping\ndata: {}\n"
	r := NewReader(strings.NewReader(input))
	want := []Event{
		{Name: "message_start", Data: []byte(`{"a":1}`)},
		{Data: []byte("line1\nline2")},
		{Name: "ping", Data: []byte("{}")},
	}
	for i, w := range want {
		got, err := r.Next()
		if err != nil {
			t.Fatalf("event %d: %v", i, err)
		}
		if got.Name != w.Name || string(got.Data) != string(w.Data) {
			t.Fatalf("event %d = %q %q, want %q %q", i, got.Name, got.Data, w.Name, w.Data)
		}
	}
	if _, err := r.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("final err = %v, want EOF", err)
	}
}

func TestReaderAcceptsDataWithoutSpace(t *testing.T) {
	r := NewReader(strings.NewReader("data:[DONE]\n\n"))
	got, err := r.Next()
	if err != nil || string(got.Data) != "[DONE]" {
		t.Fatalf("got %q, %v", got.Data, err)
	}
}
