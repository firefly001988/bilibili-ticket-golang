package scheduler

import (
	"sync"
	"time"
)

// DynamicScheduler manages multiple timed tasks with a global time offset.
type DynamicScheduler struct {
	tasks        map[string]ITask
	globalOffset time.Duration
	mutex        sync.RWMutex
}

// TaskStatus represents the current status of a task for external reporting.
type TaskStatus struct {
	TargetTime   time.Time
	AdjustedTime time.Time
	Remaining    time.Duration
	Stat         RunningStat
	Error        error
}

// NewDynamicScheduler creates a new DynamicScheduler.
func NewDynamicScheduler() *DynamicScheduler {
	return &DynamicScheduler{
		tasks:        make(map[string]ITask),
		globalOffset: 0,
	}
}

// SetGlobalOffset updates the global time offset, rescheduling all waiting tasks.
func (ds *DynamicScheduler) SetGlobalOffset(offset time.Duration) {
	ds.mutex.Lock()
	defer ds.mutex.Unlock()

	oldOffset := ds.globalOffset
	ds.globalOffset = offset

	for _, task := range ds.tasks {
		if task.GetStat() == StatWaiting && task.GetTargetTime().Add(oldOffset).Before(time.Now().Add(10*time.Second)) {
			task.rescheduleWithNewOffset(offset - oldOffset)
		}
	}
}

// GetGlobalOffset returns the current global offset.
func (ds *DynamicScheduler) GetGlobalOffset() time.Duration {
	ds.mutex.RLock()
	defer ds.mutex.RUnlock()
	return ds.globalOffset
}

// AddTask adds a one-shot scheduled task.
func (ds *DynamicScheduler) AddTask(task ITask) {
	ds.mutex.Lock()
	defer ds.mutex.Unlock()
	ds.tasks[task.GetID()] = task
	task.Start(ds.globalOffset)
}

// RemoveTask removes a task by ID.
func (ds *DynamicScheduler) RemoveTask(taskID string) {
	ds.mutex.Lock()
	defer ds.mutex.Unlock()

	if task, exists := ds.tasks[taskID]; exists {
		task.Stop()
		delete(ds.tasks, taskID)
	}
}

// GetTaskStatus returns status for all tasks.
func (ds *DynamicScheduler) GetTaskStatus() map[string]TaskStatus {
	ds.mutex.RLock()
	defer ds.mutex.RUnlock()

	status := make(map[string]TaskStatus)
	for id, task := range ds.tasks {
		adjustedTime := task.GetTargetTime().Add(ds.globalOffset)
		status[id] = TaskStatus{
			TargetTime:   task.GetTargetTime(),
			AdjustedTime: adjustedTime,
			Remaining:    time.Until(adjustedTime),
			Stat:         task.GetStat(),
			Error:        task.GetError(),
		}
	}
	return status
}

// GetTaskCount returns the number of registered tasks.
func (ds *DynamicScheduler) GetTaskCount() int {
	ds.mutex.RLock()
	defer ds.mutex.RUnlock()
	return len(ds.tasks)
}

// ForceStartTask immediately executes a task by ID, skipping its timer.
func (ds *DynamicScheduler) ForceStartTask(id string) {
	ds.mutex.RLock()
	task, exists := ds.tasks[id]
	ds.mutex.RUnlock()
	if exists {
		task.ForceStart()
	}
}

// BroadcastInterval updates the retry interval for all running tasks.
func (ds *DynamicScheduler) BroadcastInterval(newInterval time.Duration) {
	ds.mutex.RLock()
	defer ds.mutex.RUnlock()
	for _, task := range ds.tasks {
		task.UpdateInterval(newInterval)
	}
}

// BroadcastStartDelay updates the start delay (random jitter) for all running tasks.
func (ds *DynamicScheduler) BroadcastStartDelay(newDelay time.Duration) {
	ds.mutex.RLock()
	defer ds.mutex.RUnlock()
	for _, task := range ds.tasks {
		task.UpdateStartDelay(newDelay)
	}
}

// CleanupCompletedTasks removes tasks that have finished (success, failed, or error).
func (ds *DynamicScheduler) CleanupCompletedTasks() {
	ds.mutex.Lock()
	defer ds.mutex.Unlock()

	for id, task := range ds.tasks {
		stat := task.GetStat()
		if stat == StatSuccess || stat == StatFailed || stat == StatError {
			delete(ds.tasks, id)
		}
	}
}
