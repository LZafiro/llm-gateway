package provider

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestClassification(t *testing.T) {
	tests := []struct {
		name            string
		err             *Error
		retryable       bool
		failover        bool
		countsAsFailure bool
	}{
		{"connection", &Error{Kind: KindConnection}, true, true, true},
		{"timeout", &Error{Kind: KindTimeout}, true, true, true},
		{"rate limited", &Error{Kind: KindRateLimited}, true, true, true},
		{"overloaded", &Error{Kind: KindOverloaded}, true, true, true},
		{"server", &Error{Kind: KindServer}, true, true, true},
		{"client", &Error{Kind: KindClient}, false, false, false},
		{"malformed", &Error{Kind: KindMalformed}, false, true, true},
		{"canceled", &Error{Kind: KindCanceled}, false, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Retryable(); got != tt.retryable {
				t.Errorf("Retryable = %v, want %v", got, tt.retryable)
			}
			if got := tt.err.AllowsFailover(); got != tt.failover {
				t.Errorf("AllowsFailover = %v, want %v", got, tt.failover)
			}
			if got := tt.err.CountsAsFailure(); got != tt.countsAsFailure {
				t.Errorf("CountsAsFailure = %v, want %v", got, tt.countsAsFailure)
			}
		})
	}
}

func TestKindForStatus(t *testing.T) {
	tests := map[int]ErrorKind{
		http.StatusTooManyRequests:     KindRateLimited,
		529:                            KindOverloaded,
		http.StatusInternalServerError: KindServer,
		http.StatusBadGateway:          KindServer,
		http.StatusServiceUnavailable:  KindServer,
		http.StatusGatewayTimeout:      KindServer,
		http.StatusBadRequest:          KindClient,
		http.StatusUnauthorized:        KindClient,
		http.StatusNotFound:            KindClient,
	}
	for status, want := range tests {
		if got := KindForStatus(status); got != want {
			t.Errorf("KindForStatus(%d) = %s, want %s", status, got, want)
		}
	}
}

func TestTransportError(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	timeoutErr := &net.OpError{Op: "dial", Err: timeoutError{}}
	tests := []struct {
		name string
		ctx  context.Context
		err  error
		want ErrorKind
	}{
		{"canceled by client", canceled, context.Canceled, KindCanceled},
		{"deadline", context.Background(), context.DeadlineExceeded, KindTimeout},
		{"net timeout", context.Background(), timeoutErr, KindTimeout},
		{"refused", context.Background(), errors.New("connection refused"), KindConnection},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TransportError(tt.ctx, "p", tt.err).Kind; got != tt.want {
				t.Errorf("kind = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestParseRetryAfter(t *testing.T) {
	if got := parseRetryAfter("2"); got != 2*time.Second {
		t.Errorf("seconds = %v", got)
	}
	if got := parseRetryAfter("soon"); got != 0 {
		t.Errorf("invalid = %v", got)
	}
	future := time.Now().Add(time.Hour).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(future); got < 59*time.Minute {
		t.Errorf("date = %v", got)
	}
}

type timeoutError struct{}

func (timeoutError) Error() string { return "i/o timeout" }
func (timeoutError) Timeout() bool { return true }
