package cluster_service

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	"bilibili-ticket-golang/cluster/domain"
)

func (s *ClusterService) startTaskGroupWaveController(taskGroup domain.TaskGroup, initialPhase domain.Phase, reflowNow bool) {
	normalizeTaskGroupDefaults(&taskGroup)
	s.cancelTaskGroupWave(taskGroup.ID)

	ctx, cancel := context.WithCancel(context.Background())
	s.waveMu.Lock()
	if s.waveCancels == nil {
		s.waveCancels = make(map[string]context.CancelFunc)
	}
	s.waveCancels[taskGroup.ID] = cancel
	s.waveMu.Unlock()

	go s.runTaskGroupWaves(ctx, taskGroup, initialPhase, reflowNow)
}

func (s *ClusterService) cancelTaskGroupWave(taskGroupID string) {
	s.waveMu.Lock()
	cancel := s.waveCancels[taskGroupID]
	delete(s.waveCancels, taskGroupID)
	s.waveMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *ClusterService) cancelAllTaskGroupWaves() {
	s.waveMu.Lock()
	cancels := make([]context.CancelFunc, 0, len(s.waveCancels))
	for id, cancel := range s.waveCancels {
		cancels = append(cancels, cancel)
		delete(s.waveCancels, id)
	}
	s.waveMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (s *ClusterService) runTaskGroupWaves(ctx context.Context, taskGroup domain.TaskGroup, initialPhase domain.Phase, reflowNow bool) {
	defer func() {
		s.waveMu.Lock()
		delete(s.waveCancels, taskGroup.ID)
		s.waveMu.Unlock()
	}()

	macros, err := s.taskGroupMacros(ctx, taskGroup.ID)
	if err != nil {
		log.Printf("[cluster] waves: task group %s list macros: %v", taskGroup.ID, err)
		return
	}
	if len(macros) == 0 {
		log.Printf("[cluster] waves: task group %s has no macros", taskGroup.ID)
		return
	}
	saleStart, err := taskGroupSaleStart(macros)
	if err != nil {
		log.Printf("[cluster] waves: task group %s sale start: %v", taskGroup.ID, err)
		return
	}

	paymentTimeout := time.Duration(taskGroup.PaymentTimeoutMinutes) * time.Minute
	waveDuration := time.Duration(taskGroup.WaveDurationMinutes) * time.Minute
	maxWaves := taskGroup.MaxWaves
	if paymentTimeout <= 0 {
		paymentTimeout = 10 * time.Minute
	}
	if waveDuration <= 0 {
		waveDuration = 3 * time.Minute
	}
	if maxWaves <= 0 {
		maxWaves = 3
	}

	base := saleStart
	now := time.Now()
	if saleStart.Before(now) || (initialPhase == domain.PhaseReflow && reflowNow) {
		base = now
	}

	for wave := 1; wave <= maxWaves; wave++ {
		phase := initialPhase
		if wave > 1 {
			phase = domain.PhaseReflow
		}
		waveStart := base
		if wave > 1 {
			waveStart = base.Add(time.Duration(wave-1) * paymentTimeout)
		}

		if wave > 1 {
			if !waitUntil(ctx, waveStart) {
				return
			}
			if err := s.planTaskGroupWavePhase(ctx, taskGroup.ID, domain.PhaseReflow); err != nil {
				log.Printf("[cluster] waves: task group %s wave %d plan reflow: %v", taskGroup.ID, wave, err)
			}
		}

		waveEnd := waveStart.Add(waveDuration)
		log.Printf("[cluster] waves: task group %s wave %d/%d phase=%s start=%s end=%s", taskGroup.ID, wave, maxWaves, phase, waveStart.Format(time.RFC3339), waveEnd.Format(time.RFC3339))
		if !waitUntil(ctx, waveEnd) {
			return
		}

		stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if wave == maxWaves {
			if err := s.stopTaskGroupInternal(stopCtx, taskGroup.ID); err != nil {
				log.Printf("[cluster] waves: task group %s final stop: %v", taskGroup.ID, err)
			}
			cancel()
			return
		}
		if err := s.pauseTaskGroupForNextWave(stopCtx, taskGroup.ID); err != nil {
			log.Printf("[cluster] waves: task group %s pause after wave %d: %v", taskGroup.ID, wave, err)
		}
		cancel()
	}
}

func waitUntil(ctx context.Context, at time.Time) bool {
	delay := time.Until(at)
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return false
		default:
			return true
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (s *ClusterService) validateTaskGroupSaleStarted(ctx context.Context, taskGroupID string) error {
	macros, err := s.taskGroupMacros(ctx, taskGroupID)
	if err != nil {
		return err
	}
	start, err := taskGroupSaleStart(macros)
	if err != nil {
		return err
	}
	if time.Now().Before(start) {
		return fmt.Errorf("sale has not started yet: starts at %s", start.Format(time.RFC3339))
	}
	return nil
}

func (s *ClusterService) taskGroupMacros(ctx context.Context, taskGroupID string) ([]domain.MacroTask, error) {
	macros, err := s.repository.ListMacroTasks(ctx)
	if err != nil {
		return nil, err
	}
	selected := make([]domain.MacroTask, 0)
	for _, macro := range macros {
		if macro.TaskGroupID == taskGroupID {
			selected = append(selected, macro)
		}
	}
	sort.SliceStable(selected, func(i, j int) bool {
		if selected[i].Priority == selected[j].Priority {
			return selected[i].ID < selected[j].ID
		}
		return selected[i].Priority > selected[j].Priority
	})
	return selected, nil
}

func taskGroupSaleStart(macros []domain.MacroTask) (time.Time, error) {
	var start time.Time
	for _, macro := range macros {
		if macro.StartAt.IsZero() {
			continue
		}
		if start.IsZero() || macro.StartAt.Before(start) {
			start = macro.StartAt
		}
	}
	if start.IsZero() {
		return time.Time{}, fmt.Errorf("task group has no sale start time")
	}
	return start, nil
}

func (s *ClusterService) planTaskGroupWavePhase(ctx context.Context, taskGroupID string, phase domain.Phase) error {
	macros, err := s.taskGroupMacros(ctx, taskGroupID)
	if err != nil {
		return err
	}
	if len(macros) == 0 {
		return fmt.Errorf("task group has no macro tasks")
	}
	started := 0
	var lastErr error
	for _, macro := range macros {
		if _, err := s.planMacro(ctx, macro.ID, phase); err != nil {
			lastErr = err
			log.Printf("[cluster] waves: task group %s macro %s plan %s skipped: %v", taskGroupID, macro.ID, phase, err)
			continue
		}
		started++
	}
	if started == 0 {
		if lastErr != nil {
			return lastErr
		}
		return fmt.Errorf("no macro task planned")
	}
	return s.dispatcher.Reconcile(ctx)
}

func (s *ClusterService) pauseTaskGroupForNextWave(ctx context.Context, taskGroupID string) error {
	workerList, err := s.repository.ListWorkers(ctx)
	if err != nil {
		return err
	}
	macros, err := s.taskGroupMacros(ctx, taskGroupID)
	if err != nil {
		return err
	}
	workerByID := make(map[string]domain.WorkerNode, len(workerList))
	for _, worker := range workerList {
		workerByID[worker.ID] = worker
	}
	for _, macro := range macros {
		for _, attempt := range s.dispatcher.MacroAttempts(macro.ID) {
			if attempt.State.Terminal() {
				continue
			}
			worker, ok := workerByID[attempt.WorkerID]
			if !ok {
				continue
			}
			_ = s.client.Stop(ctx, worker, attempt.ID)
		}
		s.dispatcher.DisarmMacro(macro.ID)
	}
	return nil
}
