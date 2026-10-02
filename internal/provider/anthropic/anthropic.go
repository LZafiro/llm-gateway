package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/LZafiro/llm-gateway/internal/provider"
	"github.com/LZafiro/llm-gateway/internal/sse"
)

const (
	Name             = "anthropic"
	apiVersion       = "2023-06-01"
	defaultMaxTokens = 1024
)

type Client struct {
	baseURL string
	header  http.Header
	http    *http.Client
}

func New(baseURL, apiKey string, httpClient *http.Client) *Client {
	header := http.Header{}
	header.Set("x-api-key", apiKey)
	header.Set("anthropic-version", apiVersion)
	return &Client{baseURL: strings.TrimSuffix(baseURL, "/"), header: header, http: httpClient}
}

func (c *Client) Name() string {
	return Name
}

func (c *Client) Complete(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	body, err := c.post(ctx, buildRequest(req, false))
	if err != nil {
		return provider.ChatResponse{}, err
	}
	defer func() { _ = body.Close() }()
	var payload messageResponse
	if err := json.NewDecoder(body).Decode(&payload); err != nil {
		return provider.ChatResponse{}, provider.ReadError(ctx, Name, err)
	}
	var content strings.Builder
	for _, block := range payload.Content {
		if block.Type == "text" {
			content.WriteString(block.Text)
		}
	}
	return provider.ChatResponse{
		Content:      content.String(),
		FinishReason: finishReason(payload.StopReason),
		Usage:        provider.Usage{PromptTokens: payload.Usage.InputTokens, CompletionTokens: payload.Usage.OutputTokens},
	}, nil
}

func (c *Client) Stream(ctx context.Context, req provider.ChatRequest) (provider.ChunkStream, error) {
	body, err := c.post(ctx, buildRequest(req, true))
	if err != nil {
		return nil, err
	}
	return &stream{ctx: ctx, body: body, events: sse.NewReader(body)}, nil
}

func (c *Client) post(ctx context.Context, payload messageRequest) (io.ReadCloser, error) {
	return provider.PostJSON(ctx, c.http, Name, c.baseURL+"/v1/messages", c.header, payload, errorMessage)
}

type stream struct {
	ctx         context.Context
	body        io.ReadCloser
	events      *sse.Reader
	inputTokens int
	done        bool
}

func (s *stream) Next() (provider.Chunk, error) {
	for !s.done {
		event, err := s.events.Next()
		if errors.Is(err, io.EOF) {
			return provider.Chunk{}, provider.MalformedError(Name, io.ErrUnexpectedEOF)
		}
		if err != nil {
			return provider.Chunk{}, provider.ReadError(s.ctx, Name, err)
		}
		var payload streamEvent
		if err := json.Unmarshal(event.Data, &payload); err != nil {
			return provider.Chunk{}, provider.MalformedError(Name, err)
		}
		switch payload.Type {
		case "message_start":
			s.inputTokens = payload.Message.Usage.InputTokens
		case "content_block_delta":
			if payload.Delta.Type == "text_delta" && payload.Delta.Text != "" {
				return provider.Chunk{Content: payload.Delta.Text}, nil
			}
		case "message_delta":
			return provider.Chunk{
				FinishReason: finishReason(payload.Delta.StopReason),
				Usage:        &provider.Usage{PromptTokens: s.inputTokens, CompletionTokens: payload.Usage.OutputTokens},
			}, nil
		case "message_stop":
			s.done = true
		case "error":
			return provider.Chunk{}, &provider.Error{Provider: Name, Kind: errorKind(payload.Error.Type), Message: payload.Error.Message}
		}
	}
	return provider.Chunk{}, io.EOF
}

func (s *stream) Close() error {
	return s.body.Close()
}

func buildRequest(req provider.ChatRequest, stream bool) messageRequest {
	out := messageRequest{
		Model:         req.Model,
		MaxTokens:     defaultMaxTokens,
		Temperature:   clampTemperature(req.Temperature),
		TopP:          req.TopP,
		StopSequences: req.Stop,
		Stream:        stream,
	}
	if req.MaxTokens != nil {
		out.MaxTokens = *req.MaxTokens
	}
	var system []string
	for _, m := range req.Messages {
		if m.Role == provider.RoleSystem {
			system = append(system, m.Content)
			continue
		}
		role := string(m.Role)
		if n := len(out.Messages); n > 0 && out.Messages[n-1].Role == role {
			out.Messages[n-1].Content += "\n\n" + m.Content
			continue
		}
		out.Messages = append(out.Messages, message{Role: role, Content: m.Content})
	}
	out.System = strings.Join(system, "\n\n")
	if req.User != "" {
		out.Metadata = &metadata{UserID: req.User}
	}
	return out
}

func clampTemperature(t *float64) *float64 {
	if t == nil {
		return nil
	}
	v := min(max(*t, 0), 1)
	return &v
}

func finishReason(stopReason string) string {
	switch stopReason {
	case "end_turn", "stop_sequence":
		return "stop"
	case "max_tokens":
		return "length"
	case "":
		return ""
	default:
		return stopReason
	}
}

func errorKind(errorType string) provider.ErrorKind {
	switch errorType {
	case "overloaded_error":
		return provider.KindOverloaded
	case "rate_limit_error":
		return provider.KindRateLimited
	case "api_error", "timeout_error":
		return provider.KindServer
	default:
		return provider.KindClient
	}
}

func errorMessage(body []byte) string {
	var payload struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &payload) != nil || payload.Error.Message == "" {
		return strings.TrimSpace(string(body))
	}
	return fmt.Sprintf("%s: %s", payload.Error.Type, payload.Error.Message)
}
