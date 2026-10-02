package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/LZafiro/llm-gateway/internal/provider"
)

type streamWriter struct {
	w           http.ResponseWriter
	rc          *http.ResponseController
	id          string
	created     int64
	model       string
	wroteHeader bool
}

func newStreamWriter(w http.ResponseWriter, id string, created int64, model string) *streamWriter {
	return &streamWriter{w: w, rc: http.NewResponseController(w), id: id, created: created, model: model}
}

func (s *streamWriter) role() error {
	empty := ""
	return s.send(s.envelope(chunkChoice{Delta: deltaJSON{Role: string(provider.RoleAssistant), Content: &empty}}))
}

func (s *streamWriter) chunk(c provider.Chunk) error {
	if c.Content != "" {
		content := c.Content
		if err := s.send(s.envelope(chunkChoice{Delta: deltaJSON{Content: &content}})); err != nil {
			return err
		}
	}
	if c.FinishReason != "" {
		reason := c.FinishReason
		return s.send(s.envelope(chunkChoice{FinishReason: &reason}))
	}
	return nil
}

func (s *streamWriter) usage(u provider.Usage) error {
	usage := toUsage(u)
	return s.send(chunkJSON{ID: s.id, Object: "chat.completion.chunk", Created: s.created, Model: s.model, Choices: []chunkChoice{}, Usage: &usage})
}

func (s *streamWriter) done() error {
	return s.write([]byte("data: [DONE]\n\n"))
}

func (s *streamWriter) interrupted() error {
	payload := errorEnvelope{Error: errorBody{Message: "upstream stream interrupted", Type: typeUpstream, Code: "stream_interrupted"}}
	if err := s.send(payload); err != nil {
		return err
	}
	return s.done()
}

func (s *streamWriter) envelope(choice chunkChoice) chunkJSON {
	return chunkJSON{ID: s.id, Object: "chat.completion.chunk", Created: s.created, Model: s.model, Choices: []chunkChoice{choice}}
}

func (s *streamWriter) send(payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode chunk: %w", err)
	}
	return s.write(fmt.Appendf(nil, "data: %s\n\n", data))
}

func (s *streamWriter) write(frame []byte) error {
	if !s.wroteHeader {
		h := s.w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache")
		h.Set("Connection", "keep-alive")
		h.Set("X-Accel-Buffering", "no")
		s.w.WriteHeader(http.StatusOK)
		s.wroteHeader = true
	}
	if _, err := s.w.Write(frame); err != nil {
		return err
	}
	return s.rc.Flush()
}
