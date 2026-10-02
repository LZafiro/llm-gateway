package api

import "net/http"

type Deps struct {
	Readiness map[string]ReadinessCheck
}

func NewRouter(deps Deps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz)
	mux.Handle("GET /readyz", handleReadyz(deps.Readiness))
	return mux
}
