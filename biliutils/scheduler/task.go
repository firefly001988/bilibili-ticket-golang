package scheduler

import "time"

// RunningStat represents the current state of a scheduled task.
type RunningStat int

const (
	StatWaiting RunningStat = iota
	StatPending
	StatSuccess
	StatFailed
	StatError
)

// ITask defines the interface for a schedulable task.
type ITask interface {
	GetID() string
	GetTargetTime() time.Time
	Start(globalOffset time.Duration)
	ForceStart()
	Stop()
	GetStat() RunningStat
	GetError() error
	rescheduleWithNewOffset(offsetDelta time.Duration)
}
