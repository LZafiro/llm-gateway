package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/LZafiro/llm-gateway/internal/provider"
	"github.com/LZafiro/llm-gateway/internal/sse"
)

const Name = "openai"

type Client struct {
	name    string
	baseURL string
	header  http.Header
	http    *http.Client
}

func New(baseURL, apiKey string, httpClient *http.Client) *Client {
	header := http.Header{}
	header.Set("Authorization", "Bearer "+apiKey)
	return &Client{name: Name, baseURL: strings.TrimSuffix(baseURL, "/"), header: header, http: httpClient}
}

func (c *Client) Name() string {
	return c.name
}

func (c *Client) Complete(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	body, err := c.post(ctx, buildRequest(req, false))
	if err != nil {
		return provider.ChatResponse{}, err
	}
	defer func() { _ = body.Close() }()
	var payload chatResponse
	if err := json.NewDecoder(body).Decode(&payload); err != nil {
		return provider.ChatResponse{}, provider.ReadError(ctx, c.name, err)
	}
	if len(payload.Choices) == 0 {
		return provider.ChatResponse{}, provider.MalformedError(c.name, errors.New("response has no choices"))
	}
	return provider.ChatResponse{
		Content:      payload.Choices[0].Message.Content,
		FinishReason: payload.Choices[0].FinishReason,
		Usage:        provider.Usage{PromptTokens: payload.Usage.PromptTokens, CompletionTokens: payload.Usage.CompletionTokens},
	}, nil
}

func (c *Client) Stream(ctx context.Context, req provider.ChatRequest) (provider.ChunkStream, error) {
	body, err := c.post(ctx, buildRequest(req, true))
	if err != nil {
		return nil, err
	}
	return &stream{client: c, ctx: ctx, body: body, events: sse.NewReader(body)}, nil
}

func (c *Client) post(ctx context.Context, payload chatRequest) (io.ReadCloser, error) {
	return provider.PostJSON(ctx, c.http, c.name, c.baseURL+"/v1/chat/completions", c.header, payload, errorMessage)
}

type stream struct {
	client *Client
	ctx    context.Context
	body   io.ReadCloser
	events *sse.Reader
}

func (s *stream) Next() (provider.Chunk, error) {
	for {
		event, err := s.events.Next()
		if errors.Is(err, io.EOF) {
			return provider.Chunk{}, provider.MalformedError(s.client.name, io.ErrUnexpectedEOF)
		}
		if err != nil {
			return provider.Chunk{}, provider.ReadError(s.ctx, s.client.name, err)
		}
		if string(event.Data) == "[DONE]" {
			return provider.Chunk{}, io.EOF
		}
		var payload chunk
		if err := json.Unmarshal(event.Data, &payload); err != nil {
			return provider.Chunk{}, provider.MalformedError(s.client.name, err)
		}
		if payload.Error != nil {
			return provider.Chunk{}, &provider.Error{Provider: s.client.name, Kind: errorKind(payload.Error.Type), Message: payload.Error.Message}
		}
		out := provider.Chunk{}
		if len(payload.Choices) > 0 {
			out.Content = payload.Choices[0].Delta.Content
			if payload.Choices[0].FinishReason != nil {
				out.FinishReason = *payload.Choices[0].FinishReason
			}
		}
		if payload.Usage != nil {
			out.Usage = &provider.Usage{PromptTokens: payload.Usage.PromptTokens, CompletionTokens: payload.Usage.CompletionTokens}
		}
		if out != (provider.Chunk{}) {
			return out, nil
		}
	}
}

func (s *stream) Close() error {
	return s.body.Close()
}

func buildRequest(req provider.ChatRequest, stream bool) chatRequest {
	out := chatRequest{
		Model:       req.Model,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stop:        req.Stop,
		User:        req.User,
		Stream:      stream,
	}
	if stream {
		out.StreamOptions = &streamOptions{IncludeUsage: true}
	}
	for _, m := range req.Messages {
		out.Messages = append(out.Messages, message{Role: string(m.Role), Content: m.Content})
	}
	return out
}

func errorKind(errorType string) provider.ErrorKind {
	switch errorType {
	case "server_error":
		return provider.KindServer
	case "rate_limit_exceeded", "requests", "tokens":
		return provider.KindRateLimited
	default:
		return provider.KindClient
	}
}

func errorMessage(body []byte) string {
	var payload struct {
		Error apiError `json:"error"`
	}
	if json.Unmarshal(body, &payload) != nil || payload.Error.Message == "" {
		return strings.TrimSpace(string(body))
	}
	return payload.Error.Message
}
