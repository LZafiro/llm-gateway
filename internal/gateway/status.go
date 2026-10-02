package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/LZafiro/llm-gateway/internal/provider"
	"github.com/LZafiro/llm-gateway/internal/ratelimit"
	"github.com/LZafiro/llm-gateway/internal/router"
)

const StatusClientClosed = 499

type RateLimitError struct {
	Decision ratelimit.Decision
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("rate limit exceeded, retry in %s", e.Decision.RetryAfter)
}

func Classify(err error) (status int, code string) {
	var exhausted *router.ExhaustedError
	var limited *RateLimitError
	perr, isProvider := provider.AsError(err)
	switch {
	case err == nil:
		return http.StatusOK, ""
	case errors.As(err, &limited):
		return http.StatusTooManyRequests, "rate_limit_exceeded"
	case errors.Is(err, router.ErrModelNotFound):
		return http.StatusNotFound, "model_not_found"
	case errors.As(err, &exhausted):
		return http.StatusServiceUnavailable, "all_providers_unavailable"
	case errors.Is(err, router.ErrDeadline):
		return http.StatusGatewayTimeout, "deadline_exceeded"
	case errors.Is(err, context.Canceled), isProvider && perr.Kind == provider.KindCanceled:
		return StatusClientClosed, "client_closed_request"
	case isProvider && perr.Kind == provider.KindClient:
		if perr.Status >= 400 && perr.Status < 500 {
			return perr.Status, "upstream_client_error"
		}
		return http.StatusBadRequest, "upstream_client_error"
	case isProvider:
		return http.StatusBadGateway, "upstream_error"
	default:
		return http.StatusInternalServerError, "internal_error"
	}
}
