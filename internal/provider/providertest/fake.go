package providertest

import (
	"context"
	"io"
	"sync/atomic"

	"github.com/LZafiro/llm-gateway/internal/provider"
)

type Fake struct {
	ProviderName string
	Err          error
	StreamErr    error
	Response     provider.ChatResponse
	Chunks       []provider.Chunk
	calls        atomic.Int64
	lastModel    atomic.Value
}

func (f *Fake) Name() string {
	return f.ProviderName
}

func (f *Fake) Calls() int {
	return int(f.calls.Load())
}

func (f *Fake) LastModel() string {
	model, _ := f.lastModel.Load().(string)
	return model
}

func (f *Fake) Complete(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	f.calls.Add(1)
	f.lastModel.Store(req.Model)
	if f.Err != nil {
		return provider.ChatResponse{}, f.Err
	}
	return f.Response, nil
}

func (f *Fake) Stream(_ context.Context, req provider.ChatRequest) (provider.ChunkStream, error) {
	f.calls.Add(1)
	f.lastModel.Store(req.Model)
	if f.Err != nil {
		return nil, f.Err
	}
	return &fakeStream{chunks: f.Chunks, err: f.StreamErr}, nil
}

type fakeStream struct {
	chunks []provider.Chunk
	err    error
	next   int
}

func (s *fakeStream) Next() (provider.Chunk, error) {
	if s.next < len(s.chunks) {
		s.next++
		return s.chunks[s.next-1], nil
	}
	if s.err != nil {
		return provider.Chunk{}, s.err
	}
	return provider.Chunk{}, io.EOF
}

func (s *fakeStream) Close() error {
	return nil
}
