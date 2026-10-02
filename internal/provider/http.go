package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

const maxErrorBody = 64 << 10

func NewHTTPClient(responseHeaderTimeout time.Duration) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          64,
			MaxIdleConnsPerHost:   32,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: responseHeaderTimeout,
		},
	}
}

type ErrorMessageFunc func(body []byte) string

func PostJSON(ctx context.Context, client *http.Client, name, url string, header http.Header, payload any, errorMessage ErrorMessageFunc) (io.ReadCloser, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("%s: encode request: %w", name, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: build request: %w", name, err)
	}
	req.Header = header.Clone()
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, TransportError(ctx, name, err)
	}
	if resp.StatusCode == http.StatusOK {
		return resp.Body, nil
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	message := errorMessage(raw)
	if message == "" {
		message = http.StatusText(resp.StatusCode)
	}
	return nil, StatusError(name, resp, message)
}
