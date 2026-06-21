package executor

import (
	"context"
	"errors"
	"time"

	"bilibili-ticket-golang/cluster/domain"
)

// Backend owns the Bilibili prepare/confirm/createV2/confirmation transaction.
// A call must preserve the complete buyer list: partial orders are forbidden.
type Backend interface {
	Attempt(context.Context, domain.ExecutionSpec) Outcome
	Credentials() domain.Credentials
}

type Outcome struct {
	OrderID string
	Code    int
	Message string
	Err     error
}

type Classification struct {
	Reason    domain.FailureReason
	Retryable bool
	Backoff   time.Duration
}

type Classifier interface{ Classify(Outcome) Classification }

type DefaultClassifier struct{}

func (DefaultClassifier) Classify(o Outcome) Classification {
	switch o.Code {
	case 0, 100048, 100079:
		if o.OrderID != "" {
			return Classification{}
		}
	case 100016, 100017:
		return Classification{Reason: domain.FailureUnrecoverable}
	case 412:
		return Classification{Reason: domain.FailureHTTP412}
	case -101, -111:
		return Classification{Reason: domain.FailureCookieInvalid}
	case -352:
		return Classification{Reason: domain.FailureCaptcha}
	}
	// Unknown API and transport failures intentionally remain retryable.
	return Classification{Reason: domain.FailureNone, Retryable: true}
}

type Clock interface {
	Now() time.Time
	Sleep(context.Context, time.Duration) error
}

type realClock struct{}

type OffsetClock struct{ Offset time.Duration }

func (c OffsetClock) Now() time.Time { return time.Now().Add(c.Offset) }
func (c OffsetClock) Sleep(ctx context.Context, duration time.Duration) error {
	return realClock{}.Sleep(ctx, duration)
}

func (realClock) Now() time.Time { return time.Now() }
func (realClock) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type Engine struct {
	Backend    Backend
	Classifier Classifier
	Clock      Clock
	Observe    func(Event)
}

type Event struct {
	Time      time.Time
	Stage     string
	Message   string
	Code      int
	Retryable bool
}

func (e Engine) Run(ctx context.Context, spec domain.ExecutionSpec) domain.ExecutionResult {
	now := time.Now
	if e.Clock == nil {
		e.Clock = realClock{}
	} else {
		now = e.Clock.Now
	}
	if e.Classifier == nil {
		e.Classifier = DefaultClassifier{}
	}
	emit := func(stage, message string, code int, retryable bool) {
		if e.Observe != nil {
			e.Observe(Event{Time: now(), Stage: stage, Message: message, Code: code, Retryable: retryable})
		}
	}
	result := domain.ExecutionResult{AttemptID: spec.AttemptID, IntentID: spec.IntentID, SpecHash: spec.Hash(), State: domain.AttemptRunning, StartedAt: now()}
	finish := func(state domain.AttemptState, reason domain.FailureReason, message string, retryable bool) domain.ExecutionResult {
		result.State, result.Reason, result.Message, result.Retryable = state, reason, message, retryable
		result.FinishedAt = now()
		if e.Backend != nil {
			result.Credentials = e.Backend.Credentials()
		}
		return result
	}
	if err := spec.Validate(); err != nil {
		return finish(domain.AttemptFailed, domain.FailureInternal, err.Error(), false)
	}
	if e.Backend == nil {
		return finish(domain.AttemptFailed, domain.FailureInternal, "executor backend is required", false)
	}
	if !spec.Deadline.After(now()) {
		return finish(domain.AttemptFailed, domain.FailureDeadline, "deadline elapsed", false)
	}
	if spec.StartMode == domain.StartScheduled && spec.StartAt.After(now()) {
		emit("scheduled", "waiting until scheduled start", 0, false)
		if err := e.Clock.Sleep(ctx, spec.StartAt.Sub(now())); err != nil {
			return finish(domain.AttemptStopped, domain.FailureStopped, err.Error(), false)
		}
	}
	interval := time.Duration(spec.IntervalMS) * time.Millisecond
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	for {
		if err := ctx.Err(); err != nil {
			return finish(domain.AttemptStopped, domain.FailureStopped, err.Error(), false)
		}
		if !spec.Deadline.After(now()) {
			return finish(domain.AttemptFailed, domain.FailureDeadline, "deadline elapsed", false)
		}
		emit("request", "starting purchase API transaction", 0, false)
		outcome := e.Backend.Attempt(ctx, spec)
		classification := e.Classifier.Classify(outcome)
		message := outcome.Message
		if outcome.Err != nil {
			if message != "" {
				message += ": "
			}
			message += outcome.Err.Error()
		}
		if message == "" {
			message = "purchase API returned no order"
		}
		emit("response", message, outcome.Code, classification.Retryable)
		if outcome.OrderID != "" && classification.Reason == domain.FailureNone && !classification.Retryable {
			result.Success, result.OrderID = true, outcome.OrderID
			return finish(domain.AttemptSucceeded, domain.FailureNone, outcome.Message, false)
		}
		if !classification.Retryable {
			message := outcome.Message
			if message == "" && outcome.Err != nil {
				message = outcome.Err.Error()
			}
			return finish(domain.AttemptFailed, classification.Reason, message, false)
		}
		wait := interval
		if classification.Backoff > wait {
			wait = classification.Backoff
		}
		if remaining := spec.Deadline.Sub(now()); wait > remaining {
			wait = remaining
		}
		emit("retry", "retrying after "+wait.String(), outcome.Code, true)
		if err := e.Clock.Sleep(ctx, wait); err != nil && !errors.Is(err, context.DeadlineExceeded) {
			return finish(domain.AttemptStopped, domain.FailureStopped, err.Error(), false)
		}
	}
}
