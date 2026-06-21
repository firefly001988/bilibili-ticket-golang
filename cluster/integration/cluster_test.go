package integration

import (
	"context"
	"crypto/tls"
	"net"
	"path/filepath"
	"testing"
	"time"

	"bilibili-ticket-golang/cluster/dispatcher"
	"bilibili-ticket-golang/cluster/domain"
	"bilibili-ticket-golang/cluster/employer"
	"bilibili-ticket-golang/cluster/executor"
	"bilibili-ticket-golang/cluster/planner"
	"bilibili-ticket-golang/cluster/storage"
	"bilibili-ticket-golang/cluster/worker"
	pb "bilibili-ticket-golang/cluster/worker/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

type successfulBackend struct{}

func (successfulBackend) Attempt(context.Context, domain.ExecutionSpec) executor.Outcome {
	return executor.Outcome{Code: 0, OrderID: "order-1"}
}
func (successfulBackend) Credentials() domain.Credentials { return domain.Credentials{Version: 2} }

type resolver struct{}

func (resolver) Resolve(_ context.Context, _ string, buyers []domain.Buyer) ([]domain.Buyer, error) {
	result := append([]domain.Buyer(nil), buyers...)
	for i := range result {
		result[i].BuyerID = int64(i + 1)
	}
	return result, nil
}

func startIntegrationWorker(t *testing.T) (address string, clientTLS *tls.Config, cleanup func()) {
	t.Helper()
	caCertPEM, caKeyPEM, err := worker.GenerateCA()
	if err != nil {
		t.Fatal(err)
	}
	serverCertPEM, serverKeyPEM, err := worker.GenerateServerCert(caCertPEM, caKeyPEM, []string{"localhost", "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	clientCertPEM, clientKeyPEM, err := worker.GenerateClientCert(caCertPEM, caKeyPEM, "integration-client")
	if err != nil {
		t.Fatal(err)
	}

	serverTLS, err := worker.NewServerTLSConfig(caCertPEM, serverCertPEM, serverKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	clientTLS, err = worker.NewClientTLSConfig(caCertPEM, clientCertPEM, clientKeyPEM, "localhost")
	if err != nil {
		t.Fatal(err)
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	config := worker.Config{
		Listen:        lis.Addr().String(),
		DataDir:       t.TempDir(),
		PollInterval:  10 * time.Second,
		CACertPEM:     caCertPEM,
		ServerCertPEM: serverCertPEM,
		ServerKeyPEM:  serverKeyPEM,
	}
	srv, err := worker.NewServer(config, func(domain.ExecutionSpec) (executor.Backend, error) { return successfulBackend{}, nil })
	if err != nil {
		t.Fatal(err)
	}

	grpcSrv := grpc.NewServer(grpc.Creds(credentials.NewTLS(serverTLS)))
	pb.RegisterWorkerServiceServer(grpcSrv, worker.NewGRPCService(srv))
	go func() { _ = grpcSrv.Serve(lis) }()

	return lis.Addr().String(), clientTLS, func() {
		grpcSrv.Stop()
		_ = lis.Close()
	}
}

func TestEmployerWorkerPlanningDispatchAndSuccessCommit(t *testing.T) {
	ctx := context.Background()
	repository, err := storage.Open(filepath.Join(t.TempDir(), "employer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()

	address, tlsCfg, cleanup := startIntegrationWorker(t)
	defer cleanup()

	client := employer.NewWorkerClient()
	node := domain.WorkerNode{ID: "w", Address: address, Role: domain.RolePrimary, Enabled: true, TLSServerName: "localhost"}
	client.SetTLS(node.ID, tlsCfg)

	account := domain.Account{ID: "a", Role: domain.RolePrimary, Enabled: true, Credentials: domain.Credentials{Version: 1}}
	if err := repository.PutAccount(ctx, account, nil); err != nil {
		t.Fatal(err)
	}
	if err := repository.PutWorker(ctx, node); err != nil {
		t.Fatal(err)
	}
	d := dispatcher.New(client, repository, resolver{})
	d.SetResources([]domain.Account{account}, []domain.WorkerNode{node})

	group := domain.TaskGroup{ID: "g"}
	if err := repository.PutTaskGroup(ctx, group); err != nil {
		t.Fatal(err)
	}
	macro := domain.MacroTask{ID: "m", TaskGroupID: group.ID, ProjectID: 1, ScreenID: 2, SKUID: 3, EventDay: "2026-07-01", EventDayConfirmed: true, OrderCapacity: 4, DesiredReplicas: 1, HardConcurrency: 1, Deadline: time.Now().Add(time.Minute)}
	if err := repository.PutMacroTask(ctx, macro); err != nil {
		t.Fatal(err)
	}
	purchase := domain.PurchaseGroup{ID: "p", MacroTaskID: macro.ID, Buyers: []domain.Buyer{{LogicalID: "buyer"}}}
	intents, err := planner.Plan(macro, []domain.PurchaseGroup{purchase}, domain.PhasePunctual, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.PutIntent(ctx, intents[0]); err != nil {
		t.Fatal(err)
	}
	d.Add(dispatcher.IntentPlan{Macro: macro, Intent: intents[0]})
	if err := d.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := d.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
		stored, err := repository.ListIntents(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(stored) == 1 && stored[0].Succeeded {
			attempts, listErr := repository.ListAttempts(ctx)
			if listErr != nil {
				t.Fatal(listErr)
			}
			if len(attempts) != 1 || attempts[0].Result.OrderID != "order-1" || attempts[0].Result.Credentials.Version != 0 {
				t.Fatalf("unexpected persisted attempt result: %#v", attempts)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("successful worker result was not committed by employer")
}
