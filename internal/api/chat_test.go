package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LZafiro/llm-gateway/internal/config"
	"github.com/LZafiro/llm-gateway/internal/gateway"
	"github.com/LZafiro/llm-gateway/internal/provider"
	"github.com/LZafiro/llm-gateway/internal/provider/providertest"
	"github.com/LZafiro/llm-gateway/internal/router"
)

var fixedNow = func() time.Time { return time.Unix(1767225600, 0) }

func newHandler(t *testing.T, primary, fallback *providertest.Fake) http.Handler {
	t.Helper()
	r, err := router.New(config.Routes{
		Aliases: map[string][]string{"fast": {"primary/p-1", "fallback/f-1"}},
		Direct:  []string{"primary/p-1", "fallback/f-1"},
	}, map[string]provider.Provider{"primary": primary, "fallback": fallback}, router.Options{
		Resilience: config.Default().Resilience,
		Sleep:      func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return NewRouter(Deps{Gateway: gateway.New(r), Now: fixedNow})
}

func healthy(name string) *providertest.Fake {
	return &providertest.Fake{
		ProviderName: name,
		Response:     provider.ChatResponse{Content: "hello from " + name, FinishReason: "stop", Usage: provider.Usage{PromptTokens: 7, CompletionTokens: 3}},
		Chunks: []provider.Chunk{
			{Content: "hello "},
			{Content: "from " + name},
			{FinishReason: "stop", Usage: &provider.Usage{PromptTokens: 7, CompletionTokens: 3}},
		},
	}
}

func down(name string) *providertest.Fake {
	return &providertest.Fake{ProviderName: name, Err: &provider.Error{Provider: name, Kind: provider.KindConnection}}
}

func post(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, raw string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	return v
}

func sseData(t *testing.T, body string) []string {
	t.Helper()
	var frames []string
	for _, frame := range strings.Split(strings.TrimSpace(body), "\n\n") {
		data, ok := strings.CutPrefix(frame, "data: ")
		if !ok {
			t.Fatalf("frame without data prefix: %q", frame)
		}
		frames = append(frames, data)
	}
	return frames
}

func TestCompletionNonStream(t *testing.T) {
	rec := post(t, newHandler(t, down("primary"), healthy("fallback")), `{"model":"fast","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	h := rec.Header()
	if h.Get(HeaderProvider) != "fallback" || h.Get(HeaderModel) != "f-1" || h.Get(HeaderAttempts) != "3" || h.Get(HeaderCache) != "miss" {
		t.Errorf("headers = %v", h)
	}
	id := h.Get(HeaderRequestID)
	got := decode[completionJSON](t, rec.Body.String())
	want := completionJSON{
		ID: "chatcmpl-" + id, Object: "chat.completion", Created: 1767225600, Model: "f-1",
		Choices: []completionChoice{{Message: responseMessage{Role: "assistant", Content: "hello from fallback"}, FinishReason: "stop"}},
		Usage:   usageJSON{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10},
	}
	if len(id) != 26 || got.ID != want.ID || got.Choices[0] != want.Choices[0] || got.Usage != want.Usage || got.Model != want.Model || got.Created != want.Created {
		t.Errorf("body = %+v, want %+v", got, want)
	}
}

func TestCompletionStreamWithUsage(t *testing.T) {
	body := `{"model":"fast","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`
	rec := post(t, newHandler(t, healthy("primary"), healthy("fallback")), body)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status = %d, content-type = %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	frames := sseData(t, rec.Body.String())
	if len(frames) != 6 || frames[5] != "[DONE]" {
		t.Fatalf("frames = %q", frames)
	}
	first := decode[chunkJSON](t, frames[0])
	if first.Object != "chat.completion.chunk" || first.Model != "p-1" || first.Choices[0].Delta.Role != "assistant" || first.Choices[0].FinishReason != nil {
		t.Errorf("first = %s", frames[0])
	}
	var content string
	for _, f := range frames[1:3] {
		content += *decode[chunkJSON](t, f).Choices[0].Delta.Content
	}
	if content != "hello from primary" {
		t.Errorf("content = %q", content)
	}
	if finish := decode[chunkJSON](t, frames[3]).Choices[0].FinishReason; finish == nil || *finish != "stop" {
		t.Errorf("finish frame = %s", frames[3])
	}
	usage := decode[chunkJSON](t, frames[4])
	if len(usage.Choices) != 0 || usage.Usage == nil || usage.Usage.TotalTokens != 10 {
		t.Errorf("usage frame = %s", frames[4])
	}
}

func TestCompletionStreamWithoutUsage(t *testing.T) {
	rec := post(t, newHandler(t, healthy("primary"), healthy("fallback")), `{"model":"fast","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	frames := sseData(t, rec.Body.String())
	if len(frames) != 5 || strings.Contains(rec.Body.String(), `"usage"`) {
		t.Errorf("frames = %q", frames)
	}
}

func TestCompletionStreamInterrupted(t *testing.T) {
	primary := &providertest.Fake{ProviderName: "primary", Chunks: []provider.Chunk{{Content: "par"}}, StreamErr: &provider.Error{Kind: provider.KindServer}}
	rec := post(t, newHandler(t, primary, healthy("fallback")), `{"model":"fast","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	frames := sseData(t, rec.Body.String())
	if len(frames) != 4 || frames[3] != "[DONE]" {
		t.Fatalf("frames = %q", frames)
	}
	envelope := decode[errorEnvelope](t, frames[2])
	if envelope.Error.Code != "stream_interrupted" || rec.Header().Get(HeaderProvider) != "primary" {
		t.Errorf("error frame = %s", frames[2])
	}
}

func TestCompletionErrors(t *testing.T) {
	clientErr := &providertest.Fake{ProviderName: "primary", Err: &provider.Error{Kind: provider.KindClient, Status: 413, Message: "prompt is too long"}}
	tests := []struct {
		name     string
		primary  *providertest.Fake
		fallback *providertest.Fake
		body     string
		status   int
		code     string
		param    string
	}{
		{"invalid json", nil, nil, `{"model":`, 400, "invalid_json", ""},
		{"missing model", nil, nil, `{"messages":[{"role":"user","content":"hi"}]}`, 400, "missing_required_parameter", "model"},
		{"empty messages", nil, nil, `{"model":"fast","messages":[]}`, 400, "missing_required_parameter", "messages"},
		{"tools", nil, nil, `{"model":"fast","tools":[{"type":"function"}],"messages":[{"role":"user","content":"hi"}]}`, 400, "unsupported_parameter", "tools"},
		{"n", nil, nil, `{"model":"fast","n":2,"messages":[{"role":"user","content":"hi"}]}`, 400, "unsupported_parameter", "n"},
		{"logprobs", nil, nil, `{"model":"fast","logprobs":true,"messages":[{"role":"user","content":"hi"}]}`, 400, "unsupported_parameter", "logprobs"},
		{"json format", nil, nil, `{"model":"fast","response_format":{"type":"json_object"},"messages":[{"role":"user","content":"hi"}]}`, 400, "unsupported_parameter", "response_format"},
		{"tool role", nil, nil, `{"model":"fast","messages":[{"role":"tool","content":"x"}]}`, 400, "unsupported_parameter", "messages[0].role"},
		{"image part", nil, nil, `{"model":"fast","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"x"}}]}]}`, 400, "unsupported_parameter", "messages[0].content"},
		{"bad role", nil, nil, `{"model":"fast","messages":[{"role":"robot","content":"x"}]}`, 400, "invalid_value", "messages[0].role"},
		{"temperature", nil, nil, `{"model":"fast","temperature":3,"messages":[{"role":"user","content":"x"}]}`, 400, "invalid_value", "temperature"},
		{"too many stops", nil, nil, `{"model":"fast","stop":["a","b","c","d","e"],"messages":[{"role":"user","content":"x"}]}`, 400, "invalid_value", "stop"},
		{"unknown model", nil, nil, `{"model":"gpt-9","messages":[{"role":"user","content":"x"}]}`, 404, "model_not_found", "model"},
		{"exhausted", down("primary"), down("fallback"), `{"model":"fast","messages":[{"role":"user","content":"x"}]}`, 503, "all_providers_unavailable", ""},
		{"upstream client error", clientErr, healthy("fallback"), `{"model":"fast","messages":[{"role":"user","content":"x"}]}`, 413, "upstream_client_error", ""},
		{"too large", nil, nil, `{"model":"fast","messages":[{"role":"user","content":"` + strings.Repeat("a", maxBodyBytes) + `"}]}`, 413, "request_too_large", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			primary, fallback := tt.primary, tt.fallback
			if primary == nil {
				primary, fallback = healthy("primary"), healthy("fallback")
			}
			rec := post(t, newHandler(t, primary, fallback), tt.body)
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, tt.status, rec.Body)
			}
			envelope := decode[errorEnvelope](t, rec.Body.String())
			param := ""
			if envelope.Error.Param != nil {
				param = *envelope.Error.Param
			}
			if envelope.Error.Code != tt.code || param != tt.param || envelope.Error.Message == "" {
				t.Errorf("error = %+v, param = %q", envelope.Error, param)
			}
			if tt.status == http.StatusServiceUnavailable && rec.Header().Get("Retry-After") == "" {
				t.Errorf("missing Retry-After on 503")
			}
		})
	}
}

func TestDeveloperRoleIsTreatedAsSystem(t *testing.T) {
	rec := post(t, newHandler(t, healthy("primary"), healthy("fallback")), `{"model":"p-1","messages":[{"role":"developer","content":"be terse"},{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
}

func TestModels(t *testing.T) {
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	newHandler(t, healthy("primary"), healthy("fallback")).ServeHTTP(rec, req)
	list := decode[modelList](t, rec.Body.String())
	if list.Object != "list" || len(list.Data) != 3 || list.Data[0].ID != "fast" || list.Data[1].OwnedBy != "primary" {
		t.Errorf("models = %+v", list)
	}
}
