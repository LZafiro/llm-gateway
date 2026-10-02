package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/LZafiro/llm-gateway/internal/gateway"
	"github.com/LZafiro/llm-gateway/internal/provider"
	"github.com/LZafiro/llm-gateway/internal/requestid"
)

const (
	HeaderRequestID = "X-Gateway-Request-Id"
	HeaderProvider  = "X-Gateway-Provider"
	HeaderModel     = "X-Gateway-Model"
	HeaderCache     = "X-Gateway-Cache"
	HeaderAttempts  = "X-Gateway-Attempts"
)

type Gateway interface {
	Complete(ctx context.Context, req gateway.Request) (gateway.Completion, error)
	Stream(ctx context.Context, req gateway.Request) (gateway.Streaming, error)
}

type chatHandler struct {
	gateway Gateway
	logger  *slog.Logger
	now     func() time.Time
}

func (h *chatHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := requestid.New()
	w.Header().Set(HeaderRequestID, id)
	call, apiErr := decodeChat(w, r)
	if apiErr != nil {
		writeError(w, apiErr)
		return
	}
	if call.Stream {
		h.stream(w, r, id, call)
		return
	}
	completion, err := h.gateway.Complete(r.Context(), call.Request)
	setMeta(w, completion.Meta)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	resp := completion.Response
	writeJSON(w, http.StatusOK, completionJSON{
		ID:      "chatcmpl-" + id,
		Object:  "chat.completion",
		Created: h.now().Unix(),
		Model:   completion.Meta.Model,
		Choices: []completionChoice{{
			Message:      responseMessage{Role: string(provider.RoleAssistant), Content: resp.Content},
			FinishReason: resp.FinishReason,
		}},
		Usage: toUsage(resp.Usage),
	})
}

func (h *chatHandler) stream(w http.ResponseWriter, r *http.Request, id string, call chatCall) {
	streaming, err := h.gateway.Stream(r.Context(), call.Request)
	setMeta(w, streaming.Meta)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	defer func() { _ = streaming.Rest.Close() }()
	sw := newStreamWriter(w, "chatcmpl-"+id, h.now().Unix(), streaming.Meta.Model)
	var usage *provider.Usage
	err = sw.role()
	chunk := streaming.First
	for err == nil {
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
		if err = sw.chunk(chunk); err != nil {
			break
		}
		chunk, err = streaming.Rest.Next()
	}
	switch {
	case errors.Is(err, io.EOF):
		if err = h.finishStream(sw, call, usage); err != nil {
			h.logger.DebugContext(r.Context(), "stream write failed", "request_id", id, "error", err)
		}
	case isCanceled(err) || r.Context().Err() != nil:
		h.logger.InfoContext(r.Context(), "client canceled stream", "request_id", id)
	default:
		h.logger.WarnContext(r.Context(), "stream interrupted", "request_id", id, "error", err)
		_ = sw.interrupted()
	}
}

func (h *chatHandler) finishStream(sw *streamWriter, call chatCall, usage *provider.Usage) error {
	if call.IncludeUsage && usage != nil {
		if err := sw.usage(*usage); err != nil {
			return err
		}
	}
	return sw.done()
}

func (h *chatHandler) fail(w http.ResponseWriter, r *http.Request, err error) {
	if isCanceled(err) || r.Context().Err() != nil {
		h.logger.InfoContext(r.Context(), "client canceled request", "request_id", w.Header().Get(HeaderRequestID))
		return
	}
	apiErr := errorFor(err)
	if apiErr.Status >= http.StatusInternalServerError {
		h.logger.WarnContext(r.Context(), "request failed", "request_id", w.Header().Get(HeaderRequestID), "status", apiErr.Status, "error", err)
	}
	writeError(w, apiErr)
}

func setMeta(w http.ResponseWriter, meta gateway.Meta) {
	h := w.Header()
	if meta.Provider != "" {
		h.Set(HeaderProvider, meta.Provider)
		h.Set(HeaderModel, meta.Model)
	}
	if meta.Cache != "" {
		h.Set(HeaderCache, string(meta.Cache))
	}
	h.Set(HeaderAttempts, strconv.Itoa(meta.Attempts))
}

func toUsage(u provider.Usage) usageJSON {
	return usageJSON{PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens, TotalTokens: u.Total()}
}
