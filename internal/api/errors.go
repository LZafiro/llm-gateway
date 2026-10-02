package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/LZafiro/llm-gateway/internal/gateway"
	"github.com/LZafiro/llm-gateway/internal/provider"
	"github.com/LZafiro/llm-gateway/internal/router"
)

const (
	typeInvalidRequest = "invalid_request_error"
	typeUpstream       = "upstream_error"
	typeServer         = "server_error"
	typeRateLimit      = "rate_limit_error"
)

type apiError struct {
	Status     int
	Type       string
	Code       string
	Param      string
	Message    string
	RetryAfter string
}

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Param   *string `json:"param"`
	Code    string  `json:"code"`
}

func (e *apiError) envelope() errorEnvelope {
	body := errorBody{Message: e.Message, Type: e.Type, Code: e.Code}
	if e.Param != "" {
		body.Param = &e.Param
	}
	return errorEnvelope{Error: body}
}

func invalidRequest(code, param, message string) *apiError {
	return &apiError{Status: http.StatusBadRequest, Type: typeInvalidRequest, Code: code, Param: param, Message: message}
}

func writeError(w http.ResponseWriter, e *apiError) {
	if e.RetryAfter != "" {
		w.Header().Set("Retry-After", e.RetryAfter)
	}
	writeJSON(w, e.Status, e.envelope())
}

func errorFor(err error) *apiError {
	status, code := gateway.Classify(err)
	e := &apiError{Status: status, Code: code}
	var exhausted *router.ExhaustedError
	var limited *gateway.RateLimitError
	switch {
	case errors.As(err, &limited):
		e.Type, e.Message = typeRateLimit, "rate limit exceeded for this API key"
		e.RetryAfter = strconv.Itoa(int(limited.Decision.RetryAfter.Seconds()))
	case status == http.StatusNotFound:
		e.Type, e.Param, e.Message = typeInvalidRequest, "model", err.Error()
	case errors.As(err, &exhausted):
		e.Type, e.RetryAfter = typeUpstream, "1"
		e.Message = fmt.Sprintf("all providers unavailable for model %q", exhausted.Route.Requested)
	case status == http.StatusGatewayTimeout:
		e.Type, e.Message = typeUpstream, "request deadline exceeded"
	case code == "upstream_client_error":
		perr, _ := provider.AsError(err)
		e.Type, e.Message = typeInvalidRequest, perr.Message
	case code == "upstream_error":
		e.Type, e.Message = typeUpstream, "upstream provider error"
	default:
		e.Type, e.Message = typeServer, "internal error"
	}
	return e
}
