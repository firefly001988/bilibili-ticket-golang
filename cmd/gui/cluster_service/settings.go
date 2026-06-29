package cluster_service

import (
	"context"
	"log"
	"time"

	"bilibili-ticket-golang/cluster/domain"
)

// ── Global configuration ──────────────────────────────────────────────

// globalConfig holds the employer-side runtime configuration that is
// pushed to all connected workers. The zero value means "use defaults".
type globalConfig struct {
	RetryIntervalMs int64 `json:"retryIntervalMs"`
	StartDelayMs    int64 `json:"startDelayMs"`
}

// EffectiveRetryInterval returns the retry interval to use, falling back
// to the provided per-intent value when the global setting is zero.
func (cfg globalConfig) EffectiveRetryInterval(fallback int64) int64 {
	if cfg.RetryIntervalMs > 0 {
		return cfg.RetryIntervalMs
	}
	return fallback
}

// EffectiveStartDelay returns the start delay to use, falling back to the
// provided default when the global setting is zero.
func (cfg globalConfig) EffectiveStartDelay(fallback int64) int64 {
	if cfg.StartDelayMs > 0 {
		return cfg.StartDelayMs
	}
	return fallback
}

// LoadGlobalConfig reads the persisted global configuration from the
// repository and applies it to the dispatcher. Call this after creating
// the dispatcher to restore saved settings from disk.
func (s *ClusterService) LoadGlobalConfig(ctx context.Context) {
	if s.repository == nil {
		return
	}
	retryMs, startMs, err := s.repository.GlobalConfig(ctx)
	if err != nil {
		log.Printf("[cluster] load global config failed: %v", err)
		return
	}

	s.mu.Lock()
	s.globalCfg.RetryIntervalMs = retryMs
	s.globalCfg.StartDelayMs = startMs
	s.mu.Unlock()

	if s.dispatcher != nil {
		s.dispatcher.SetGlobalConfig(retryMs, startMs)
	}

	log.Printf("[cluster] loaded global config: retryInterval=%dms startDelay=%dms", retryMs, startMs)
}

// GetRetryInterval returns the global retry interval in milliseconds.
// When the value is 0 the dispatcher uses its built-in default (500ms).
func (s *ClusterService) GetRetryInterval() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return int(s.globalCfg.RetryIntervalMs)
}

// SetRetryInterval updates the global retry interval (ms), persists it,
// broadcasts the change to all running tasks, and pushes it to all
// connected workers so they use the new value for future submissions.
func (s *ClusterService) SetRetryInterval(ms int) {
	if ms < 50 {
		ms = 50
	}

	s.mu.Lock()
	s.globalCfg.RetryIntervalMs = int64(ms)
	s.mu.Unlock()

	// Persist to disk.
	if s.repository != nil {
		if err := s.repository.PutGlobalConfig(context.Background(), int64(ms), s.globalCfg.StartDelayMs); err != nil {
			log.Printf("[cluster] persist retry interval failed: %v", err)
		}
	}

	// Update the dispatcher so new attempts use the new interval.
	if s.dispatcher != nil {
		s.dispatcher.SetGlobalConfig(s.globalCfg.RetryIntervalMs, s.globalCfg.StartDelayMs)
	}

	log.Printf("[cluster] global retry interval set to %dms (will push to all workers)", ms)

	// Push to all connected healthy workers asynchronously.
	go s.pushGlobalConfigToAll(context.Background())
}

// GetStartDelay returns the global start delay in milliseconds.
// When the value is 0 no early start is applied.
func (s *ClusterService) GetStartDelay() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return int(s.globalCfg.StartDelayMs)
}

// SetStartDelay updates the global start delay (ms, 0-500), persists it,
// and pushes it to all connected workers. A positive value tells workers
// to start their scheduled tasks this many milliseconds early to account
// for clock drift and network latency.
func (s *ClusterService) SetStartDelay(ms int) {
	if ms < 0 {
		ms = 0
	}
	if ms > 500 {
		ms = 500
	}

	s.mu.Lock()
	s.globalCfg.StartDelayMs = int64(ms)
	s.mu.Unlock()

	// Persist to disk.
	if s.repository != nil {
		if err := s.repository.PutGlobalConfig(context.Background(), s.globalCfg.RetryIntervalMs, int64(ms)); err != nil {
			log.Printf("[cluster] persist start delay failed: %v", err)
		}
	}

	// Update the dispatcher so new attempts use the new start delay.
	if s.dispatcher != nil {
		s.dispatcher.SetGlobalConfig(s.globalCfg.RetryIntervalMs, s.globalCfg.StartDelayMs)
	}

	log.Printf("[cluster] global start delay set to %dms (will push to all workers)", ms)

	// Push to all connected healthy workers asynchronously.
	go s.pushGlobalConfigToAll(context.Background())
}

// pushGlobalConfigToAll sends the current global config to every enabled
// worker (both local and remote) that has a healthy connection.
func (s *ClusterService) pushGlobalConfigToAll(ctx context.Context) {
	s.mu.RLock()
	cfg := s.globalCfg
	s.mu.RUnlock()

	workers, err := s.repository.ListWorkers(ctx)
	if err != nil {
		log.Printf("[cluster] pushGlobalConfig: list workers failed: %v", err)
		return
	}

	for _, w := range workers {
		if !w.Enabled {
			continue
		}
		if !s.client.IsHealthy(w.ID) {
			continue
		}
		w := w
		go func() {
			cfgCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := s.client.Configure(cfgCtx, w, cfg.RetryIntervalMs, cfg.StartDelayMs); err != nil {
				log.Printf("[cluster] push config to worker %s failed: %v", w.ID, err)
			} else {
				log.Printf("[cluster] pushed config to worker %s: retryInterval=%dms startDelay=%dms", w.ID, cfg.RetryIntervalMs, cfg.StartDelayMs)
			}
		}()
	}
}

// pushGlobalConfigToWorker sends the current global config to a specific
// worker. Called during the connection handshake in refreshResources.
func (s *ClusterService) pushGlobalConfigToWorker(ctx context.Context, node domain.WorkerNode) {
	s.mu.RLock()
	cfg := s.globalCfg
	s.mu.RUnlock()

	cfgCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := s.client.Configure(cfgCtx, node, cfg.RetryIntervalMs, cfg.StartDelayMs); err != nil {
		log.Printf("[cluster] push config to new worker %s failed: %v", node.ID, err)
	}
}
