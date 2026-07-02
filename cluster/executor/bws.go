package executor

import (
	"context"
	"fmt"

	"bilibili-ticket-golang/cluster/domain"
	"bilibili-ticket-golang/lib/biliutils"
)

// BWSBackend implements Backend for BWS (Bilibili World) reservation.
// Each Attempt() call makes a single MakeBWSReservation API request;
// the retry loop, clock calibration, and scheduled-start wait are all
// handled by Engine.Run().
type BWSBackend struct {
	client      *biliutils.BiliClient
	credentials domain.Credentials
	activityID  int
	ticketNo    string
}

// NewBWSBackend creates a new BWS reservation backend from credentials.
func NewBWSBackend(credentials domain.Credentials) (*BWSBackend, error) {
	backend, err := NewBilibiliBackend(credentials)
	if err != nil {
		return nil, fmt.Errorf("bws backend: %w", err)
	}
	client, _ := backend.ClientAndJar()
	return &BWSBackend{
		client:      client,
		credentials: credentials,
	}, nil
}

// Credentials returns the current (potentially refreshed) credentials.
func (b *BWSBackend) Credentials() domain.Credentials {
	return b.credentials
}

// SetReservation configures the BWS reservation target.
func (b *BWSBackend) SetReservation(activityID int, ticketNo string) {
	b.activityID = activityID
	b.ticketNo = ticketNo
}

// Attempt performs a single BWS reservation API call and returns an Outcome.
// Implements Backend.  On success (code 0) it fills OrderID so Engine.Run()
// can recognise the successful result.
func (b *BWSBackend) Attempt(ctx context.Context, spec domain.ExecutionSpec) Outcome {
	code, message, err := b.client.MakeBWSReservation(b.ticketNo, b.activityID)
	if err != nil {
		return Outcome{Code: -999, Message: message, Err: err}
	}
	if code == 0 {
		return Outcome{Code: 0, Message: message, OrderID: fmt.Sprintf("bws-%d", b.activityID)}
	}
	return Outcome{Code: code, Message: message}
}

// ── BWS Outcome Classifier ─────────────────────────────────────

// BWSClassifier classifies BWS reservation outcomes for the retry loop.
type BWSClassifier struct{}

func (BWSClassifier) Classify(o Outcome) Classification {
	if o.Err != nil {
		// Transport errors are always retryable
		return Classification{Reason: domain.FailureNone, Retryable: true}
	}
	switch o.Code {
	case 0:
		// Success
		return Classification{}
	case 75638:
		// Ticket not bound to real-name identity — unrecoverable
		return Classification{Reason: domain.FailureUnrecoverable}
	case -101, -111:
		// Cookie / login expired — unrecoverable for this attempt
		return Classification{Reason: domain.FailureCookieInvalid}
	default:
		// Unknown API code — retry
		return Classification{Reason: domain.FailureNone, Retryable: true}
	}
}
