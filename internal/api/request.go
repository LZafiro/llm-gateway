package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/LZafiro/llm-gateway/internal/gateway"
	"github.com/LZafiro/llm-gateway/internal/provider"
)

const (
	maxBodyBytes = 1 << 20
	maxStops     = 4
)

type chatCall struct {
	Request      gateway.Request
	Stream       bool
	IncludeUsage bool
}

func decodeChat(w http.ResponseWriter, r *http.Request) (chatCall, *apiError) {
	var req chatRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return chatCall{}, &apiError{Status: http.StatusRequestEntityTooLarge, Type: typeInvalidRequest, Code: "request_too_large", Message: "request body exceeds 1 MiB"}
		}
		return chatCall{}, invalidRequest("invalid_json", "", "request body is not valid JSON: "+err.Error())
	}
	if apiErr := rejectUnsupported(req); apiErr != nil {
		return chatCall{}, apiErr
	}
	chat, apiErr := toChatRequest(req)
	if apiErr != nil {
		return chatCall{}, apiErr
	}
	return chatCall{
		Request:      gateway.Request{Model: req.Model, Chat: chat},
		Stream:       req.Stream,
		IncludeUsage: req.StreamOptions != nil && req.StreamOptions.IncludeUsage,
	}, nil
}

func rejectUnsupported(req chatRequest) *apiError {
	raw := []struct {
		param string
		value json.RawMessage
	}{
		{"tools", req.Tools},
		{"tool_choice", req.ToolChoice},
		{"functions", req.Functions},
		{"function_call", req.FunctionCall},
		{"logprobs", req.Logprobs},
		{"top_logprobs", req.TopLogprobs},
		{"audio", req.Audio},
		{"modalities", req.Modalities},
	}
	for _, field := range raw {
		if present(field.value) {
			return unsupported(field.param)
		}
	}
	if req.N != nil && *req.N != 1 {
		return unsupported("n")
	}
	if req.ResponseFormat != nil && req.ResponseFormat.Type != "text" {
		return unsupported("response_format")
	}
	return nil
}

func present(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	switch string(trimmed) {
	case "", "null", "false", "[]", "{}":
		return false
	}
	return true
}

func unsupported(param string) *apiError {
	return invalidRequest("unsupported_parameter", param, fmt.Sprintf("parameter %q is not supported by this gateway", param))
}

func toChatRequest(req chatRequest) (provider.ChatRequest, *apiError) {
	if req.Model == "" {
		return provider.ChatRequest{}, invalidRequest("missing_required_parameter", "model", "model is required")
	}
	if len(req.Messages) == 0 {
		return provider.ChatRequest{}, invalidRequest("missing_required_parameter", "messages", "messages must be a non-empty array")
	}
	chat := provider.ChatRequest{
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		User:        req.User,
	}
	if req.MaxCompletionTokens != nil {
		chat.MaxTokens = req.MaxCompletionTokens
	}
	if chat.MaxTokens != nil && *chat.MaxTokens < 1 {
		return provider.ChatRequest{}, invalidRequest("invalid_value", "max_tokens", "max_tokens must be at least 1")
	}
	if chat.Temperature != nil && (*chat.Temperature < 0 || *chat.Temperature > 2) {
		return provider.ChatRequest{}, invalidRequest("invalid_value", "temperature", "temperature must be between 0 and 2")
	}
	if chat.TopP != nil && (*chat.TopP < 0 || *chat.TopP > 1) {
		return provider.ChatRequest{}, invalidRequest("invalid_value", "top_p", "top_p must be between 0 and 1")
	}
	stop, apiErr := parseStop(req.Stop)
	if apiErr != nil {
		return provider.ChatRequest{}, apiErr
	}
	chat.Stop = stop
	for i, m := range req.Messages {
		msg, apiErr := toMessage(i, m)
		if apiErr != nil {
			return provider.ChatRequest{}, apiErr
		}
		chat.Messages = append(chat.Messages, msg)
	}
	return chat, nil
}

func parseStop(raw json.RawMessage) ([]string, *apiError) {
	if !present(raw) {
		return nil, nil
	}
	var single string
	if json.Unmarshal(raw, &single) == nil {
		return []string{single}, nil
	}
	var many []string
	if json.Unmarshal(raw, &many) != nil || len(many) > maxStops {
		return nil, invalidRequest("invalid_value", "stop", "stop must be a string or an array of up to 4 strings")
	}
	return many, nil
}

func toMessage(index int, m wireMessage) (provider.Message, *apiError) {
	param := fmt.Sprintf("messages[%d]", index)
	role := provider.Role(m.Role)
	switch role {
	case provider.RoleSystem, provider.RoleUser, provider.RoleAssistant:
	case "tool", "function":
		return provider.Message{}, unsupported(param + ".role")
	case "developer":
		role = provider.RoleSystem
	default:
		return provider.Message{}, invalidRequest("invalid_value", param+".role", fmt.Sprintf("unknown role %q", m.Role))
	}
	content, apiErr := parseContent(param+".content", m.Content)
	if apiErr != nil {
		return provider.Message{}, apiErr
	}
	return provider.Message{Role: role, Content: content}, nil
}

func parseContent(param string, raw json.RawMessage) (string, *apiError) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, nil
	}
	var parts []contentPart
	if json.Unmarshal(raw, &parts) != nil {
		return "", invalidRequest("invalid_value", param, "content must be a string or an array of text parts")
	}
	var b strings.Builder
	for _, part := range parts {
		if part.Type != "text" {
			return "", unsupported(param)
		}
		b.WriteString(part.Text)
	}
	return b.String(), nil
}
