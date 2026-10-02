package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/LZafiro/llm-gateway/internal/chaos"
)

type ChaosAdmin interface {
	Set(provider string, rule chaos.Rule) (chaos.Rule, error)
	Clear(provider string) error
}

type chaosRuleRequest struct {
	Down       bool    `json:"down"`
	ErrorRate  float64 `json:"error_rate"`
	LatencyMS  int     `json:"latency_ms"`
	JitterMS   int     `json:"jitter_ms"`
	TTLSeconds *int    `json:"ttl_seconds"`
}

type chaosRuleJSON struct {
	Provider  string     `json:"provider"`
	Down      bool       `json:"down"`
	ErrorRate float64    `json:"error_rate"`
	LatencyMS int64      `json:"latency_ms"`
	JitterMS  int64      `json:"jitter_ms"`
	ExpiresAt *time.Time `json:"expires_at"`
	Source    string     `json:"source"`
}

func toChaosRuleJSON(provider string, rule chaos.Rule) chaosRuleJSON {
	out := chaosRuleJSON{
		Provider:  provider,
		Down:      rule.Down,
		ErrorRate: rule.ErrorRate,
		LatencyMS: rule.Latency.Milliseconds(),
		JitterMS:  rule.Jitter.Milliseconds(),
		Source:    string(rule.Source),
	}
	if !rule.ExpiresAt.IsZero() {
		expires := rule.ExpiresAt.UTC()
		out.ExpiresAt = &expires
	}
	return out
}

func requireAdmin(token string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		presented, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" || !ok || subtle.ConstantTimeCompare([]byte(presented), []byte(token)) != 1 {
			writeError(w, &apiError{Status: http.StatusUnauthorized, Type: "authentication_error", Code: "invalid_admin_token", Message: "invalid admin token"})
			return
		}
		next(w, r)
	}
}

func handlePutChaos(store ChaosAdmin, now func() time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req chaosRuleRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
			writeError(w, invalidRequest("invalid_json", "", "request body is not valid JSON"))
			return
		}
		rule := chaos.Rule{
			Down:      req.Down,
			ErrorRate: req.ErrorRate,
			Latency:   time.Duration(req.LatencyMS) * time.Millisecond,
			Jitter:    time.Duration(req.JitterMS) * time.Millisecond,
		}
		if req.TTLSeconds != nil {
			if *req.TTLSeconds < 1 {
				writeError(w, invalidRequest("invalid_value", "ttl_seconds", "ttl_seconds must be positive"))
				return
			}
			rule.ExpiresAt = now().Add(time.Duration(*req.TTLSeconds) * time.Second)
		}
		provider := r.PathValue("provider")
		stored, err := store.Set(provider, rule)
		if err != nil {
			writeError(w, chaosError(err))
			return
		}
		writeJSON(w, http.StatusOK, toChaosRuleJSON(provider, stored))
	}
}

func handleDeleteChaos(store ChaosAdmin) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := store.Clear(r.PathValue("provider")); err != nil {
			writeError(w, chaosError(err))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func chaosError(err error) *apiError {
	if errors.Is(err, chaos.ErrUnknownProvider) {
		return &apiError{Status: http.StatusNotFound, Type: typeInvalidRequest, Code: "provider_not_found", Param: "provider", Message: err.Error()}
	}
	return invalidRequest("invalid_value", "", err.Error())
}
