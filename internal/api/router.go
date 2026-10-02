package api

import (
	"log/slog"
	"net/http"
	"time"
)

type Service interface {
	Gateway
	ModelLister
}

type Deps struct {
	Gateway    Service
	Chaos      ChaosAdmin
	AdminToken string
	Logger     *slog.Logger
	Readiness  map[string]ReadinessCheck
	Now        func() time.Time
}

func NewRouter(deps Deps) http.Handler {
	if deps.Logger == nil {
		deps.Logger = slog.New(slog.DiscardHandler)
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz)
	mux.Handle("GET /readyz", handleReadyz(deps.Readiness))
	if deps.Gateway != nil {
		mux.Handle("POST /v1/chat/completions", &chatHandler{gateway: deps.Gateway, logger: deps.Logger, now: deps.Now})
		mux.Handle("GET /v1/models", handleModels(deps.Gateway))
	}
	if deps.Chaos != nil {
		mux.Handle("PUT /admin/chaos/{provider}", requireAdmin(deps.AdminToken, handlePutChaos(deps.Chaos, deps.Now)))
		mux.Handle("DELETE /admin/chaos/{provider}", requireAdmin(deps.AdminToken, handleDeleteChaos(deps.Chaos)))
	}
	return mux
}
