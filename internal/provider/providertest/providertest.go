package providertest

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/LZafiro/llm-gateway/internal/provider"
)

type Reply struct {
	Status  int
	Fixture string
	Header  http.Header
}

type Server struct {
	URL string
	mu  sync.Mutex
	req []Captured
}

type Captured struct {
	Path   string
	Header http.Header
	Body   []byte
}

func NewServer(t *testing.T, reply Reply) *Server {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", reply.Fixture))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	s := &Server{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.req = append(s.req, Captured{Path: r.URL.Path, Header: r.Header.Clone(), Body: raw})
		s.mu.Unlock()
		for k, v := range reply.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(reply.Status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	s.URL = srv.URL
	return s
}

func (s *Server) Requests() []Captured {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Captured(nil), s.req...)
}

func Drain(stream provider.ChunkStream) ([]provider.Chunk, error) {
	defer func() { _ = stream.Close() }()
	var chunks []provider.Chunk
	for {
		chunk, err := stream.Next()
		if errors.Is(err, io.EOF) {
			return chunks, nil
		}
		if err != nil {
			return chunks, err
		}
		chunks = append(chunks, chunk)
	}
}

func Concat(chunks []provider.Chunk) (content, finishReason string, usage *provider.Usage) {
	for _, c := range chunks {
		content += c.Content
		if c.FinishReason != "" {
			finishReason = c.FinishReason
		}
		if c.Usage != nil {
			usage = c.Usage
		}
	}
	return content, finishReason, usage
}

func RequireKind(t *testing.T, err error, want provider.ErrorKind) {
	t.Helper()
	perr, ok := provider.AsError(err)
	if !ok {
		t.Fatalf("err = %v (%T), want *provider.Error", err, err)
	}
	if perr.Kind != want {
		t.Fatalf("kind = %s, want %s (err: %v)", perr.Kind, want, err)
	}
}
