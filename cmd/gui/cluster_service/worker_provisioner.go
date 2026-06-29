package cluster_service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"bilibili-ticket-golang/cluster/domain"
	"bilibili-ticket-golang/cluster/employer"
)

// ErrNoWorkerAvailable is returned when every healthy worker is already
// bound to a different account.  Callers should retry after a short wait.
var ErrNoWorkerAvailable = errors.New("no worker available — all workers busy, retry later")

// WorkerProvisioner implements accounts.Provisioner by delegating to a
// worker (local or remote) via the WorkerClient gRPC transport.
type WorkerProvisioner struct {
	client     *employer.WorkerClient
	pickWorker func(accountID string) (domain.WorkerNode, error)
	release    func(accountID string)
}

// NewWorkerProvisioner creates a provisioner that routes buyer CRUD
// operations through workers.  The pickWorker callback acquires an
// exclusive account→worker binding (or returns ErrNoWorkerAvailable when
// all workers are busy).  The release callback frees the binding so the
// worker can serve another account.
func NewWorkerProvisioner(client *employer.WorkerClient) *WorkerProvisioner {
	return &WorkerProvisioner{
		client: client,
		pickWorker: func(accountID string) (domain.WorkerNode, error) {
			return domain.WorkerNode{
				ID:      "local",
				Address: "127.0.0.1:37900",
				Type:    domain.WorkerTypeLocal,
				Enabled: true,
			}, nil
		},
		release: func(accountID string) {},
	}
}

// SetPickWorker replaces the worker selection strategy.  The function
// receives an account ID and must return the WorkerNode to use for that
// account.  The release callback frees the binding so the worker can
// serve another account.
func (p *WorkerProvisioner) SetPickWorker(
	pick func(accountID string) (domain.WorkerNode, error),
	release func(accountID string),
) {
	p.pickWorker = pick
	p.release = release
}

// ListBuyers fetches all real-name buyers from a Bilibili account by
// proxying through a worker.  If all workers are busy the call retries
// for up to 30 seconds with 1-second backoff.
func (p *WorkerProvisioner) ListBuyers(ctx context.Context, account domain.Account) ([]domain.Buyer, domain.Credentials, error) {
	node, err := p.acquireWorker(ctx, account.ID)
	if err != nil {
		return nil, account.Credentials, err
	}
	defer p.release(account.ID)

	buyers, creds, err := p.client.ListBuyers(ctx, node, account.Credentials)
	if err != nil {
		return nil, account.Credentials, fmt.Errorf("worker %s list buyers for account %s: %w", node.ID, account.ID, err)
	}
	log.Printf("[worker-provisioner] listed %d buyers on account %s via worker %s", len(buyers), account.ID, node.ID)
	return buyers, creds, nil
}

// CreateBuyer creates a new real-name buyer on a Bilibili account via a
// worker.  If all workers are busy the call retries for up to 30 seconds
// with 1-second backoff.
func (p *WorkerProvisioner) CreateBuyer(ctx context.Context, account domain.Account, buyer domain.Buyer) (domain.Buyer, domain.Credentials, error) {
	node, err := p.acquireWorker(ctx, account.ID)
	if err != nil {
		return domain.Buyer{}, account.Credentials, err
	}
	defer p.release(account.ID)

	created, creds, err := p.client.CreateBuyer(ctx, node, account.Credentials, buyer)
	if err != nil {
		return domain.Buyer{}, account.Credentials, fmt.Errorf("worker %s create buyer for account %s: %w", node.ID, account.ID, err)
	}
	log.Printf("[worker-provisioner] created buyer %s on account %s via worker %s", created.LogicalID, account.ID, node.ID)
	return created, creds, nil
}

// acquireWorker calls pickWorker with a retry loop.  When
// ErrNoWorkerAvailable is returned, it waits 1 second and retries for up
// to 30 seconds.  Context cancellation terminates the retry early.
func (p *WorkerProvisioner) acquireWorker(ctx context.Context, accountID string) (domain.WorkerNode, error) {
	deadline := time.Now().Add(30 * time.Second)
	for {
		node, err := p.pickWorker(accountID)
		if err == nil {
			return node, nil
		}
		if !errors.Is(err, ErrNoWorkerAvailable) {
			return domain.WorkerNode{}, fmt.Errorf("no worker available for account %s: %w", accountID, err)
		}
		if time.Now().After(deadline) {
			return domain.WorkerNode{}, fmt.Errorf("timed out waiting for a free worker for account %s", accountID)
		}
		select {
		case <-ctx.Done():
			return domain.WorkerNode{}, ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
}

// ── Account → Worker binding (mutual exclusion) ─────────────────

// pickWorkerForAccount ensures each account uses at most one worker at a
// time ("一号一Worker").  If the account already has an active binding,
// that worker is returned immediately (idempotent).  Otherwise any free
// healthy worker is picked and bound to the account.  When no worker is
// free, ErrNoWorkerAvailable is returned and the caller should retry.
func (s *ClusterService) pickWorkerForAccount(accountID string) (domain.WorkerNode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Lazy init.
	if s.accountBindings == nil {
		s.accountBindings = make(map[string]string)
	}

	// Already bound — reuse the same worker.
	if workerID, ok := s.accountBindings[accountID]; ok {
		for _, w := range s.workers {
			if w.ID == workerID && w.Enabled {
				return w, nil
			}
		}
		// Worker disappeared (e.g. removed while bound) — clear stale
		// binding and fall through to pick a new one.
		delete(s.accountBindings, accountID)
	}

	// Build a set of busy worker IDs.
	busy := make(map[string]bool, len(s.accountBindings))
	for _, wid := range s.accountBindings {
		busy[wid] = true
	}

	// Separate free healthy workers: local first, then remotes.
	var localNode *domain.WorkerNode
	var freeRemotes []domain.WorkerNode
	for _, w := range s.workers {
		if !w.Enabled || busy[w.ID] {
			continue
		}
		if w.Type == domain.WorkerTypeLocal {
			n := w
			localNode = &n
			continue
		}
		if s.client.IsHealthy(w.ID) {
			freeRemotes = append(freeRemotes, w)
		}
	}

	// No remote workers: local worker can serve unlimited accounts.
	if len(freeRemotes) == 0 {
		if localNode != nil {
			s.accountBindings[accountID] = localNode.ID
			return *localNode, nil
		}
		return domain.WorkerNode{}, fmt.Errorf("no healthy workers available")
	}

	// Pick any free remote worker, preferring the one with the fewest
	// current bindings (spread the load evenly).
	best := freeRemotes[0]
	bestLoad := s.bindingCount(best.ID)
	for _, w := range freeRemotes[1:] {
		if n := s.bindingCount(w.ID); n < bestLoad {
			best, bestLoad = w, n
		}
	}

	s.accountBindings[accountID] = best.ID
	return best, nil
}

// bindingCount returns how many accounts are currently bound to a worker.
// Must be called under s.mu.
func (s *ClusterService) bindingCount(workerID string) int {
	n := 0
	for _, wid := range s.accountBindings {
		if wid == workerID {
			n++
		}
	}
	return n
}

// releaseAccount removes the account→worker binding so the worker can be
// used for another account.
func (s *ClusterService) releaseAccount(accountID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.accountBindings, accountID)
}
