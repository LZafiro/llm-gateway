package gateway

import (
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/LZafiro/llm-gateway/internal/ledger"
	"github.com/LZafiro/llm-gateway/internal/provider"
	"github.com/LZafiro/llm-gateway/internal/router"
)

type tracker struct {
	service *Service
	start   time.Time
	entry   ledger.Entry
	once    sync.Once
}

func (s *Service) begin(req Request) *tracker {
	start := s.now()
	source := req.Source
	if source == "" {
		source = ledger.SourceAPI
	}
	return &tracker{
		service: s,
		start:   start,
		entry: ledger.Entry{
			RequestID:      req.ID,
			CreatedAt:      start,
			TenantID:       req.Tenant.ID,
			TenantName:     req.Tenant.Name,
			Source:         source,
			EndUser:        req.Chat.User,
			RequestedModel: req.Model,
			Stream:         req.Stream,
		},
	}
}

func (t *tracker) finish(meta Meta, outcome router.Outcome, usage provider.Usage, err error) {
	status, code := Classify(err)
	t.record(meta, outcome, usage, status, code)
}

func (t *tracker) finishStream(meta Meta, outcome router.Outcome, usage provider.Usage, err error) {
	status, code := streamStatus(err)
	t.record(meta, outcome, usage, status, code)
}

func (t *tracker) record(meta Meta, outcome router.Outcome, usage provider.Usage, status int, code string) {
	t.once.Do(func() {
		e := t.entry
		e.Provider, e.Model, e.Cache = meta.Provider, meta.Model, string(meta.Cache)
		e.Status, e.ErrorCode = status, code
		e.Attempts = attempts(outcome)
		e.Usage = usage
		if meta.Provider != "" {
			e.CostUSD = t.service.pricing.Cost(meta.Provider+"/"+meta.Model, usage)
		}
		e.Latency = t.service.now().Sub(t.start)
		t.service.recorder.Record(e)
	})
}

func attempts(outcome router.Outcome) []ledger.Attempt {
	out := make([]ledger.Attempt, 0, len(outcome.Attempts))
	for _, a := range outcome.Attempts {
		out = append(out, ledger.Attempt{
			Provider:  a.Target.Provider,
			Model:     a.Target.Model,
			Status:    a.Status,
			Kind:      string(a.Kind),
			Injected:  a.Injected,
			LatencyMS: a.Latency.Milliseconds(),
		})
	}
	return out
}

var errStreamAbandoned = errors.New("stream closed before completion")

type recordingStream struct {
	provider.ChunkStream
	tracker *tracker
	meta    Meta
	outcome router.Outcome
	usage   *provider.Usage
	err     error
	ended   bool
}

func (s *recordingStream) observe(c provider.Chunk) {
	if c.Usage != nil {
		usage := *c.Usage
		s.usage = &usage
	}
}

func (s *recordingStream) Next() (provider.Chunk, error) {
	c, err := s.ChunkStream.Next()
	switch {
	case err == nil:
		s.observe(c)
	case errors.Is(err, io.EOF):
		s.ended = true
	default:
		s.err = err
	}
	return c, err
}

func (s *recordingStream) Close() error {
	err := s.ChunkStream.Close()
	var final error
	switch {
	case s.err != nil:
		final = s.err
	case !s.ended:
		final = errStreamAbandoned
	}
	usage := provider.Usage{}
	if s.usage != nil {
		usage = *s.usage
	}
	s.tracker.entry.UsageEstimated = s.usage == nil
	s.tracker.finishStream(s.meta, s.outcome, usage, final)
	return err
}

func streamStatus(err error) (int, string) {
	if err == nil {
		return http.StatusOK, ""
	}
	if errors.Is(err, errStreamAbandoned) {
		return StatusClientClosed, "client_closed_request"
	}
	if status, code := Classify(err); status == StatusClientClosed {
		return status, code
	}
	return http.StatusBadGateway, "stream_interrupted"
}
