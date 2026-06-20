package employer

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"bilibili-ticket-golang/cluster/domain"
	"bilibili-ticket-golang/cluster/executor"
	"bilibili-ticket-golang/cluster/worker"
)

type employerBackend struct{}

func (employerBackend) Attempt(context.Context, domain.ExecutionSpec) executor.Outcome {
	return executor.Outcome{OrderID: "order"}
}
func (employerBackend) Credentials() domain.Credentials { return domain.Credentials{Version: 2} }

func TestWorkerClientUsesSameProtocolForRemoteWorkers(t *testing.T) {
	server, err := worker.NewServer(worker.Config{BearerKey: "key", DataDir: t.TempDir(), PollInterval: 10 * time.Second}, func(domain.ExecutionSpec) (executor.Backend, error) { return employerBackend{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	node := domain.WorkerNode{ID: "remote", BaseURL: httpServer.URL}
	client := NewWorkerClient()
	client.SetKey(node.ID, "key")
	spec := domain.ExecutionSpec{AttemptID: "a", IntentID: "i", ProjectID: 1, ScreenID: 2, SKUID: 3, Buyers: []domain.Buyer{{LogicalID: "b"}}, StartMode: domain.StartImmediate, Deadline: time.Now().Add(time.Minute)}
	if err := client.Submit(context.Background(), node, spec); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		status, err := client.Status(context.Background(), node, "a")
		if err != nil {
			t.Fatal(err)
		}
		if status.State == domain.AttemptSucceeded {
			if status.Result.Credentials.Version != 2 {
				t.Fatalf("credentials missing: %#v", status)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("attempt did not complete")
}
