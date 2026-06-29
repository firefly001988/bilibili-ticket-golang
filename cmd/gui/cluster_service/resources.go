package cluster_service

import (
	"context"
	"log"
	"sync"
	"time"

	"bilibili-ticket-golang/cluster/domain"
	"bilibili-ticket-golang/lib/global"
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
				s.RecordWorkerDisconnected(node.ID, err.Error())
				dispatchWorkers[i].Enabled = false
				return
			}
			// If this worker previously had SkipVersionCheck set and now
			// Health passed with a real version match, the versions have
			// converged — clear the flag so the next divergence requires
			// manual acknowledgment again.
			if node.SkipVersionCheck && info != nil {
				if ok, _ := info["protocolVersionOk"].(bool); ok && global.GitCommit != "Development" {
					log.Printf("[cluster] versions converged for worker %s — clearing SkipVersionCheck", node.ID)
					node.SkipVersionCheck = false
					_ = s.repository.PutWorker(ctx, node)
				}
			}
			ver, _ := info["version"].(string)
			plugin, _ := info["pluginVersion"].(string)
			log.Printf("[cluster] worker %s connected (version=%s, plugin=%s)", node.ID, ver, plugin)
			s.RecordWorkerConnected(node.ID, node.Address, ver)
			// Push the current global config to the newly connected worker.
			s.pushGlobalConfigToWorker(ctx, node)
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

	// Cache worker list for provisioner use.
	s.mu.Lock()
	s.workers = dispatchWorkers
	s.mu.Unlock()

	return nil
}
