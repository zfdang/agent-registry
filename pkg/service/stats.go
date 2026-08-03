package service

import (
	"context"
	"sync"
	"time"

	"github.com/zfdang/agent-registry/pkg/model"
)

// statsCache is a simple 60-second in-process cache for GET /v1/stats (03).
type statsCache struct {
	mu        sync.Mutex
	ttl       time.Duration
	value     model.StatsResponse
	fetchedAt time.Time
	valid     bool
}

func newStatsCache(ttl time.Duration) *statsCache {
	return &statsCache{ttl: ttl}
}

// Ping checks database connectivity, for GET /health?ready=1.
func (s *Service) Ping(ctx context.Context) error {
	return s.store.Ping(ctx)
}

// Stats returns the cached stats, refreshing them if the cache is empty or
// expired.
func (s *Service) Stats(ctx context.Context) (model.StatsResponse, error) {
	c := s.statsCache
	c.mu.Lock()
	if c.valid && time.Since(c.fetchedAt) < c.ttl {
		v := c.value
		c.mu.Unlock()
		return v, nil
	}
	c.mu.Unlock()

	raw, err := s.store.Stats(ctx)
	if err != nil {
		return model.StatsResponse{}, err
	}
	resp := model.StatsResponse{
		TotalAgents:    raw.TotalAgents,
		VerifiedAgents: raw.VerifiedAgents,
		NewThisWeek:    raw.NewThisWeek,
		DistinctOwners: raw.DistinctOwners,
	}

	c.mu.Lock()
	c.value = resp
	c.fetchedAt = time.Now()
	c.valid = true
	c.mu.Unlock()

	return resp, nil
}
