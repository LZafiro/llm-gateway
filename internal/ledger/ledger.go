package ledger

import (
	"time"

	"github.com/LZafiro/llm-gateway/internal/config"
	"github.com/LZafiro/llm-gateway/internal/provider"
)

type Source string

const (
	SourceAPI  Source = "api"
	SourceDemo Source = "demo"
)

type Attempt struct {
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	Status    int    `json:"status,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Injected  bool   `json:"injected,omitempty"`
	LatencyMS int64  `json:"latency_ms"`
}

type Entry struct {
	RequestID        string
	CreatedAt        time.Time
	TenantID         int64
	TenantName       string
	Source           Source
	EndUser          string
	RequestedModel   string
	Provider         string
	Model            string
	Stream           bool
	Cache            string
	Similarity       *float64
	Status           int
	ErrorCode        string
	Attempts         []Attempt
	Usage            provider.Usage
	UsageEstimated   bool
	CostUSD          float64
	EmbeddingCostUSD float64
	SavedUSD         float64
	Latency          time.Duration
	TTFB             *time.Duration
	Overhead         *time.Duration
}

type Pricing config.Pricing

func (p Pricing) Cost(model string, usage provider.Usage) float64 {
	price := p[model]
	return (float64(usage.PromptTokens)*price.InputPerMTok + float64(usage.CompletionTokens)*price.OutputPerMTok) / 1e6
}
