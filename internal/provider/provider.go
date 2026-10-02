package provider

import "context"

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

type Message struct {
	Role    Role
	Content string
}

type ChatRequest struct {
	Model       string
	Messages    []Message
	MaxTokens   *int
	Temperature *float64
	TopP        *float64
	Stop        []string
	User        string
}

type Usage struct {
	PromptTokens     int
	CompletionTokens int
}

func (u Usage) Total() int {
	return u.PromptTokens + u.CompletionTokens
}

type ChatResponse struct {
	Content      string
	FinishReason string
	Usage        Usage
}

type Chunk struct {
	Content      string
	FinishReason string
	Usage        *Usage
}

type ChunkStream interface {
	Next() (Chunk, error)
	Close() error
}

type Provider interface {
	Name() string
	Complete(ctx context.Context, req ChatRequest) (ChatResponse, error)
	Stream(ctx context.Context, req ChatRequest) (ChunkStream, error)
}
