package maintenance

import (
	"context"
	"log/slog"
	"time"
)

const (
	jobName  = "daily"
	runHour  = 3
	interval = 24 * time.Hour
)

type Task struct {
	Name string
	Run  func(ctx context.Context, now time.Time) (int64, error)
}

type Store interface {
	LastRun(ctx context.Context, job string) (time.Time, bool, error)
	MarkRun(ctx context.Context, job string, at time.Time) error
}

type Scheduler struct {
	store  Store
	tasks  []Task
	logger *slog.Logger
	now    func() time.Time
}

func New(store Store, logger *slog.Logger, now func() time.Time, tasks ...Task) *Scheduler {
	return &Scheduler{store: store, tasks: tasks, logger: logger, now: now}
}

func (s *Scheduler) Run(ctx context.Context) {
	if last, ok, err := s.store.LastRun(ctx, jobName); err != nil {
		s.logger.WarnContext(ctx, "maintenance: read last run failed", "error", err)
	} else if !ok || s.now().Sub(last) > interval {
		s.RunOnce(ctx)
	}
	for {
		timer := time.NewTimer(NextRun(s.now()).Sub(s.now()))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			s.RunOnce(ctx)
		}
	}
}

func (s *Scheduler) RunOnce(ctx context.Context) {
	now := s.now()
	for _, task := range s.tasks {
		affected, err := task.Run(ctx, now)
		if err != nil {
			s.logger.WarnContext(ctx, "maintenance task failed", "task", task.Name, "error", err)
			continue
		}
		s.logger.InfoContext(ctx, "maintenance task done", "task", task.Name, "rows", affected)
	}
	if err := s.store.MarkRun(ctx, jobName, now); err != nil {
		s.logger.WarnContext(ctx, "maintenance: mark run failed", "error", err)
	}
}

func NextRun(now time.Time) time.Time {
	now = now.UTC()
	next := time.Date(now.Year(), now.Month(), now.Day(), runHour, 0, 0, 0, time.UTC)
	if !next.After(now) {
		next = next.Add(interval)
	}
	return next
}
