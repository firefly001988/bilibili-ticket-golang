package cluster_service

import (
	"context"
	"log"
	"sync"
	"time"

	"bilibili-ticket-golang/cluster/domain"
)

// refreshResources reloads accounts and workers from the repository,
// checks health for remote workers, and pushes the latest state into
// the dispatcher.
func (s *ClusterService) refreshResources(ctx context.Context) error {
	accountList, err := s.repository.ListAccounts(ctx)
	if err != nil {
		return err
	}
	workers, err := s.repository.ListWorkers(ctx)
	if err != nil {
		return err
	}
	dispatchWorkers := make([]domain.WorkerNode, len(workers))
	copy(dispatchWorkers, workers)
	var wg sync.WaitGroup
	for i := range dispatchWorkers {
		i := i
		node := dispatchWorkers[i]
		if !node.Enabled {
			continue
		}
		if node.Type == domain.WorkerTypeLocal {
			// Local workers are managed in-process; skip health checks.
			continue
		}
		if s.client.IsHealthy(node.ID) {
			s.dispatcher.MarkWorkerHealthy(node.ID)
			continue
		}
		if s.client.IsDisconnected(node.ID) {
			dispatchWorkers[i].Enabled = false
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			healthCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			info, err := s.client.Health(healthCtx, node)
			cancel()
			if err != nil {
				log.Printf("[cluster] health check failed for worker %s (%s): %v", node.ID, node.Address, err)
				dispatchWorkers[i].Enabled = false
				return
			}
			log.Printf("[cluster] worker %s connected (version=%s, plugin=%s)", node.ID, info["version"], info["pluginVersion"])
			if s.client.IsHealthy(node.ID) {
				s.dispatcher.MarkWorkerHealthy(node.ID)
			}
		}()
	}
	wg.Wait()
	for i := range dispatchWorkers {
		if dispatchWorkers[i].Enabled && !s.client.IsHealthy(dispatchWorkers[i].ID) {
			dispatchWorkers[i].Enabled = false
		}
	}
	s.dispatcher.SetResources(accountList, dispatchWorkers)

	return nil
}
