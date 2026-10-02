package api

import "encoding/json"

type chatRequest struct {
	Model               string          `json:"model"`
	Messages            []wireMessage   `json:"messages"`
	Stream              bool            `json:"stream"`
	StreamOptions       *streamOptions  `json:"stream_options"`
	MaxTokens           *int            `json:"max_tokens"`
	MaxCompletionTokens *int            `json:"max_completion_tokens"`
	Temperature         *float64        `json:"temperature"`
	TopP                *float64        `json:"top_p"`
	Stop                json.RawMessage `json:"stop"`
	User                string          `json:"user"`
	N                   *int            `json:"n"`
	ResponseFormat      *responseFormat `json:"response_format"`
	Tools               json.RawMessage `json:"tools"`
	ToolChoice          json.RawMessage `json:"tool_choice"`
	Functions           json.RawMessage `json:"functions"`
	FunctionCall        json.RawMessage `json:"function_call"`
	Logprobs            json.RawMessage `json:"logprobs"`
	TopLogprobs         json.RawMessage `json:"top_logprobs"`
	Audio               json.RawMessage `json:"audio"`
	Modalities          json.RawMessage `json:"modalities"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type wireMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type contentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type usageJSON struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type completionJSON struct {
	ID      string             `json:"id"`
	Object  string             `json:"object"`
	Created int64              `json:"created"`
	Model   string             `json:"model"`
	Choices []completionChoice `json:"choices"`
	Usage   usageJSON          `json:"usage"`
}

type completionChoice struct {
	Index        int             `json:"index"`
	Message      responseMessage `json:"message"`
	FinishReason string          `json:"finish_reason"`
}

type responseMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chunkJSON struct {
	ID      string        `json:"id"`
	Object  string        `json:"object"`
	Created int64         `json:"created"`
	Model   string        `json:"model"`
	Choices []chunkChoice `json:"choices"`
	Usage   *usageJSON    `json:"usage,omitempty"`
}

type chunkChoice struct {
	Index        int       `json:"index"`
	Delta        deltaJSON `json:"delta"`
	FinishReason *string   `json:"finish_reason"`
}

type deltaJSON struct {
	Role    string  `json:"role,omitempty"`
	Content *string `json:"content,omitempty"`
}

type modelList struct {
	Object string      `json:"object"`
	Data   []modelJSON `json:"data"`
}

type modelJSON struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}
