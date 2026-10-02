package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

type ErrorKind string

const (
	KindTimeout     ErrorKind = "timeout"
	KindConnection  ErrorKind = "connection"
	KindRateLimited ErrorKind = "rate_limited"
	KindOverloaded  ErrorKind = "overloaded"
	KindServer      ErrorKind = "server"
	KindClient      ErrorKind = "client"
	KindMalformed   ErrorKind = "malformed"
	KindCanceled    ErrorKind = "canceled"
)

type Error struct {
	Provider   string
	Status     int
	Kind       ErrorKind
	RetryAfter time.Duration
	Message    string
	Err        error
}

func (e *Error) Error() string {
	msg := e.Message
	if msg == "" && e.Err != nil {
		msg = e.Err.Error()
	}
	if e.Status != 0 {
		return fmt.Sprintf("%s: %s (status %d): %s", e.Provider, e.Kind, e.Status, msg)
	}
	return fmt.Sprintf("%s: %s: %s", e.Provider, e.Kind, msg)
}

func (e *Error) Unwrap() error {
	return e.Err
}

func (e *Error) Retryable() bool {
	switch e.Kind {
	case KindTimeout, KindConnection, KindRateLimited, KindOverloaded, KindServer:
		return true
	case KindClient, KindMalformed, KindCanceled:
		return false
	}
	return false
}

func (e *Error) AllowsFailover() bool {
	return e.Retryable() || e.Kind == KindMalformed
}

func (e *Error) CountsAsFailure() bool {
	return e.Kind != KindClient && e.Kind != KindCanceled
}

func AsError(err error) (*Error, bool) {
	var perr *Error
	ok := errors.As(err, &perr)
	return perr, ok
}

func KindForStatus(status int) ErrorKind {
	switch {
	case status == http.StatusTooManyRequests:
		return KindRateLimited
	case status == 529:
		return KindOverloaded
	case status >= 500:
		return KindServer
	default:
		return KindClient
	}
}

func StatusError(name string, resp *http.Response, message string) *Error {
	return &Error{
		Provider:   name,
		Status:     resp.StatusCode,
		Kind:       KindForStatus(resp.StatusCode),
		RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
		Message:    message,
	}
}

func TransportError(ctx context.Context, name string, err error) *Error {
	kind := KindConnection
	switch {
	case ctx.Err() != nil && errors.Is(context.Cause(ctx), context.Canceled):
		kind = KindCanceled
	case errors.Is(err, context.DeadlineExceeded), isTimeout(err):
		kind = KindTimeout
	}
	return &Error{Provider: name, Kind: kind, Err: err}
}

func MalformedError(name string, err error) *Error {
	return &Error{Provider: name, Kind: KindMalformed, Err: err}
}

func ReadError(ctx context.Context, name string, err error) *Error {
	if ctx.Err() != nil {
		return TransportError(ctx, name, err)
	}
	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &syntaxErr) || errors.As(err, &typeErr) || errors.Is(err, io.ErrUnexpectedEOF) {
		return MalformedError(name, err)
	}
	return TransportError(ctx, name, err)
}

func isTimeout(err error) bool {
	var timeout interface{ Timeout() bool }
	return errors.As(err, &timeout) && timeout.Timeout()
}

func parseRetryAfter(value string) time.Duration {
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		if d := time.Until(at); d > 0 {
			return d
		}
	}
	return 0
}
