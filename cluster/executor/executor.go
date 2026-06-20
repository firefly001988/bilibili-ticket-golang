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
	result := domain.ExecutionResult{AttemptID: spec.AttemptID, IntentID: spec.IntentID, State: domain.AttemptRunning, StartedAt: now()}
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
		outcome := e.Backend.Attempt(ctx, spec)
		classification := e.Classifier.Classify(outcome)
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
		if err := e.Clock.Sleep(ctx, wait); err != nil && !errors.Is(err, context.DeadlineExceeded) {
			return finish(domain.AttemptStopped, domain.FailureStopped, err.Error(), false)
		}
	}
}
