package mockprovider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/LZafiro/llm-gateway/internal/provider"
	"github.com/LZafiro/llm-gateway/internal/provider/mock"
)

type Server struct {
	latency time.Duration
	now     func() time.Time
}

func New(latency time.Duration) *Server {
	return &Server{latency: latency, now: time.Now}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", s.openAI)
	mux.HandleFunc("POST /v1/messages", s.anthropic)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	return mux
}

type inbound struct {
	Model    string `json:"model"`
	System   string `json:"system"`
	Stream   bool   `json:"stream"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

type reply struct {
	model   string
	content string
	pieces  []string
	usage   provider.Usage
}

func (s *Server) read(w http.ResponseWriter, r *http.Request) (inbound, reply, bool) {
	var in inbound
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, `{"error":{"message":"invalid json","type":"invalid_request_error"}}`, http.StatusBadRequest)
		return in, reply{}, false
	}
	messages := make([]provider.Message, 0, len(in.Messages)+1)
	if in.System != "" {
		messages = append(messages, provider.Message{Role: provider.RoleSystem, Content: in.System})
	}
	for _, m := range in.Messages {
		messages = append(messages, provider.Message{Role: provider.Role(m.Role), Content: m.Content})
	}
	content := mock.Reply(messages)
	select {
	case <-time.After(s.latency):
	case <-r.Context().Done():
		return in, reply{}, false
	}
	return in, reply{model: in.Model, content: content, pieces: mock.Split(content), usage: mock.EstimateUsage(messages, content)}, true
}

func (s *Server) openAI(w http.ResponseWriter, r *http.Request) {
	in, rep, ok := s.read(w, r)
	if !ok {
		return
	}
	created := s.now().Unix()
	usage := map[string]int{"prompt_tokens": rep.usage.PromptTokens, "completion_tokens": rep.usage.CompletionTokens, "total_tokens": rep.usage.Total()}
	if !in.Stream {
		writeJSON(w, map[string]any{
			"id": "chatcmpl-mock", "object": "chat.completion", "created": created, "model": rep.model,
			"choices": []any{map[string]any{"index": 0, "message": map[string]string{"role": "assistant", "content": rep.content}, "finish_reason": "stop"}},
			"usage":   usage,
		})
		return
	}
	sse := newEventWriter(w)
	chunk := func(delta map[string]string, finish any) map[string]any {
		return map[string]any{
			"id": "chatcmpl-mock", "object": "chat.completion.chunk", "created": created, "model": rep.model,
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
		}
	}
	sse.data(chunk(map[string]string{"role": "assistant", "content": ""}, nil))
	for _, piece := range rep.pieces {
		sse.data(chunk(map[string]string{"content": piece}, nil))
	}
	sse.data(chunk(map[string]string{}, "stop"))
	sse.data(map[string]any{"id": "chatcmpl-mock", "object": "chat.completion.chunk", "created": created, "model": rep.model, "choices": []any{}, "usage": usage})
	sse.raw("data: [DONE]\n\n")
}

func (s *Server) anthropic(w http.ResponseWriter, r *http.Request) {
	in, rep, ok := s.read(w, r)
	if !ok {
		return
	}
	if !in.Stream {
		writeJSON(w, map[string]any{
			"id": "msg_mock", "type": "message", "role": "assistant", "model": rep.model,
			"content":     []any{map[string]string{"type": "text", "text": rep.content}},
			"stop_reason": "end_turn",
			"usage":       map[string]int{"input_tokens": rep.usage.PromptTokens, "output_tokens": rep.usage.CompletionTokens},
		})
		return
	}
	sse := newEventWriter(w)
	sse.event("message_start", map[string]any{"type": "message_start", "message": map[string]any{
		"id": "msg_mock", "type": "message", "role": "assistant", "model": rep.model, "content": []any{},
		"usage": map[string]int{"input_tokens": rep.usage.PromptTokens, "output_tokens": 1},
	}})
	sse.event("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]string{"type": "text", "text": ""}})
	for _, piece := range rep.pieces {
		sse.event("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]string{"type": "text_delta", "text": piece}})
	}
	sse.event("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
	sse.event("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn"}, "usage": map[string]int{"output_tokens": rep.usage.CompletionTokens}})
	sse.event("message_stop", map[string]string{"type": "message_stop"})
}

func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

type eventWriter struct {
	w  http.ResponseWriter
	rc *http.ResponseController
}

func newEventWriter(w http.ResponseWriter) *eventWriter {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	return &eventWriter{w: w, rc: http.NewResponseController(w)}
}

func (e *eventWriter) event(name string, payload any) {
	data, _ := json.Marshal(payload)
	e.raw(fmt.Sprintf("event: %s\ndata: %s\n\n", name, data))
}

func (e *eventWriter) data(payload any) {
	data, _ := json.Marshal(payload)
	e.raw(fmt.Sprintf("data: %s\n\n", data))
}

func (e *eventWriter) raw(frame string) {
	_, _ = fmt.Fprint(e.w, frame)
	_ = e.rc.Flush()
}
