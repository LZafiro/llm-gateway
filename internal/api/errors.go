package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/LZafiro/llm-gateway/internal/provider"
	"github.com/LZafiro/llm-gateway/internal/router"
)

const (
	typeInvalidRequest = "invalid_request_error"
	typeUpstream       = "upstream_error"
	typeServer         = "server_error"
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
	var exhausted *router.ExhaustedError
	perr, isProvider := provider.AsError(err)
	switch {
	case errors.Is(err, router.ErrModelNotFound):
		return &apiError{Status: http.StatusNotFound, Type: typeInvalidRequest, Code: "model_not_found", Param: "model", Message: err.Error()}
	case errors.As(err, &exhausted):
		return &apiError{Status: http.StatusServiceUnavailable, Type: typeUpstream, Code: "all_providers_unavailable", Message: err.Error(), RetryAfter: "1"}
	case errors.Is(err, context.DeadlineExceeded):
		return &apiError{Status: http.StatusGatewayTimeout, Type: typeUpstream, Code: "deadline_exceeded", Message: "request deadline exceeded"}
	case isProvider && perr.Kind == provider.KindClient:
		status := perr.Status
		if status == 0 {
			status = http.StatusBadRequest
		}
		return &apiError{Status: status, Type: typeInvalidRequest, Code: "upstream_client_error", Message: perr.Message}
	case isProvider:
		return &apiError{Status: http.StatusBadGateway, Type: typeUpstream, Code: "upstream_error", Message: perr.Error()}
	default:
		return &apiError{Status: http.StatusInternalServerError, Type: typeServer, Code: "internal_error", Message: "internal error"}
	}
}

func isCanceled(err error) bool {
	perr, ok := provider.AsError(err)
	return errors.Is(err, context.Canceled) || ok && perr.Kind == provider.KindCanceled
}
