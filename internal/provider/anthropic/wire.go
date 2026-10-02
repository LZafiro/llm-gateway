package anthropic

type messageRequest struct {
	Model         string    `json:"model"`
	System        string    `json:"system,omitempty"`
	Messages      []message `json:"messages"`
	MaxTokens     int       `json:"max_tokens"`
	Temperature   *float64  `json:"temperature,omitempty"`
	TopP          *float64  `json:"top_p,omitempty"`
	StopSequences []string  `json:"stop_sequences,omitempty"`
	Stream        bool      `json:"stream,omitempty"`
	Metadata      *metadata `json:"metadata,omitempty"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type metadata struct {
	UserID string `json:"user_id"`
}

type usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type messageResponse struct {
	Content    []contentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
	Usage      usage          `json:"usage"`
}

type streamEvent struct {
	Type    string          `json:"type"`
	Message messageResponse `json:"message"`
	Delta   struct {
		Type       string `json:"type"`
		Text       string `json:"text"`
		StopReason string `json:"stop_reason"`
	} `json:"delta"`
	Usage usage `json:"usage"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}
