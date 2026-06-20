package executor

import (
	"context"
	"testing"
	"time"

	"bilibili-ticket-golang/cluster/domain"
)

type fakeBackend struct {
	outcomes []Outcome
	calls    int
	creds    domain.Credentials
}

func (f *fakeBackend) Attempt(context.Context, domain.ExecutionSpec) Outcome {
	o := f.outcomes[f.calls]
	f.calls++
	return o
}
func (f *fakeBackend) Credentials() domain.Credentials { return f.creds }

func validSpec() domain.ExecutionSpec {
	return domain.ExecutionSpec{AttemptID: "a", IntentID: "i", ProjectID: 1, ScreenID: 2, SKUID: 3, Buyers: []domain.Buyer{{LogicalID: "b"}}, StartMode: domain.StartImmediate, Deadline: time.Now().Add(time.Minute), IntervalMS: 1}
}

func TestUnknownErrorRetriesAndReturnsCredentials(t *testing.T) {
	b := &fakeBackend{outcomes: []Outcome{{Code: 7654}, {Code: 0, OrderID: "order"}}, creds: domain.Credentials{Version: 2}}
	r := (Engine{Backend: b}).Run(context.Background(), validSpec())
	if !r.Success || b.calls != 2 || r.Credentials.Version != 2 {
		t.Fatalf("unexpected result: %#v calls=%d", r, b.calls)
	}
}

func TestUnrecoverableAndCancellation(t *testing.T) {
	b := &fakeBackend{outcomes: []Outcome{{Code: 100016}}}
	r := (Engine{Backend: b}).Run(context.Background(), validSpec())
	if r.Reason != domain.FailureUnrecoverable || r.Retryable {
		t.Fatalf("unexpected result: %#v", r)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r = (Engine{Backend: b}).Run(ctx, validSpec())
	if r.State != domain.AttemptStopped {
		t.Fatalf("unexpected cancelled result: %#v", r)
	}
}

func TestExpiredDeadlineDoesNotCallBackend(t *testing.T) {
	b := &fakeBackend{}
	s := validSpec()
	s.Deadline = time.Now().Add(-time.Second)
	r := (Engine{Backend: b}).Run(context.Background(), s)
	if r.Reason != domain.FailureDeadline || b.calls != 0 {
		t.Fatalf("unexpected result: %#v", r)
	}
}
