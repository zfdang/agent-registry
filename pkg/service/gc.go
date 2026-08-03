package service

import (
	"context"
	"time"
)

// ValidationGCAge is how long past its expiry a validation row (and its
// checks) is kept before being swept (03).
const ValidationGCAge = 24 * time.Hour

// TaskGCAge is how long a terminal a2a_task row is retained before being
// swept (03).
const TaskGCAge = 7 * 24 * time.Hour

// RunGC sweeps expired validations and old terminal A2A tasks. Callers run
// this once at boot and again on a daily ticker (05, 03).
func (s *Service) RunGC(ctx context.Context) {
	now := time.Now().UTC()

	if n, err := s.store.SweepExpiredValidations(ctx, now.Add(-ValidationGCAge)); err != nil {
		s.log.Errorw("gc: sweep expired validations failed", "error", err)
	} else if n > 0 {
		s.log.Infow("gc: swept expired validations", "count", n)
	}

	if n, err := s.store.SweepTerminalTasks(ctx, now.Add(-TaskGCAge)); err != nil {
		s.log.Errorw("gc: sweep terminal tasks failed", "error", err)
	} else if n > 0 {
		s.log.Infow("gc: swept terminal a2a tasks", "count", n)
	}
}

// StartGCTicker runs RunGC once immediately and then once per day until ctx
// is canceled.
func (s *Service) StartGCTicker(ctx context.Context) {
	s.RunGC(ctx)
	ticker := time.NewTicker(24 * time.Hour)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.RunGC(ctx)
			}
		}
	}()
}
