package api

import (
	"net/http"
	"strings"

	"github.com/LZafiro/llm-gateway/internal/auth"
)

type KeyLookup interface {
	Lookup(plaintext string) (auth.Key, error)
}

func authenticate(keys KeyLookup, r *http.Request) (auth.Key, *apiError) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if ok {
		if key, err := keys.Lookup(strings.TrimSpace(token)); err == nil {
			return key, nil
		}
	}
	return auth.Key{}, &apiError{
		Status:  http.StatusUnauthorized,
		Type:    "authentication_error",
		Code:    "invalid_api_key",
		Message: "missing or invalid API key, pass it as Authorization: Bearer <key>",
	}
}
