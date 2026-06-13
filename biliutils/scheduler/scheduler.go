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
// SetGlobalOffset updates the global time offset, rescheduling all waiting tasks
// whose remaining time is greater than 10 seconds. Tasks within 10 seconds of
// execution are left untouched to avoid disturbing precise timing.
func (ds *DynamicScheduler) SetGlobalOffset(offset time.Duration) {
	ds.mutex.Lock()
	defer ds.mutex.Unlock()

	oldOffset := ds.globalOffset
	ds.globalOffset = offset

	for _, task := range ds.tasks {
		if task.GetStat() == StatWaiting && time.Until(task.GetTargetTime().Add(oldOffset)) > 10*time.Second {
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
// Returns true if the task was added, false if a task with the same ID is
// already registered.
func (ds *DynamicScheduler) AddTask(task ITask) bool {
	ds.mutex.Lock()
	defer ds.mutex.Unlock()
	if _, exists := ds.tasks[task.GetID()]; exists {
		return false
	}
	ds.tasks[task.GetID()] = task
	task.Start(ds.globalOffset)
	return true
}

// HasTask reports whether a task with the given ID is currently registered.
func (ds *DynamicScheduler) HasTask(id string) bool {
	ds.mutex.RLock()
	defer ds.mutex.RUnlock()
	_, exists := ds.tasks[id]
	return exists
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

// RemoveTaskAndStream removes a task by ID and invokes onRemove after deletion
// while still holding the scheduler lock — useful for closing associated
// resources (log streams, etc.) without racing with a re-add.
func (ds *DynamicScheduler) RemoveTaskAndStream(taskID string, onRemove func()) {
	ds.mutex.Lock()
	defer ds.mutex.Unlock()

	if task, exists := ds.tasks[taskID]; exists {
		task.Stop()
		delete(ds.tasks, taskID)
	}
	if onRemove != nil {
		onRemove()
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
