package worker

import (
	"context"
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bilibili-ticket-golang/cluster/domain"
	"bilibili-ticket-golang/cluster/executor"
	pb "bilibili-ticket-golang/cluster/worker/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type backend struct {
	block   <-chan struct{}
	outcome executor.Outcome
}

func (b backend) Attempt(ctx context.Context, _ domain.ExecutionSpec) executor.Outcome {
	if b.block != nil {
		select {
		case <-ctx.Done():
			return executor.Outcome{Err: ctx.Err()}
		case <-b.block:
		}
	}
	return b.outcome
}
func (backend) Credentials() domain.Credentials { return domain.Credentials{Version: 3} }

func workerSpec(id string) domain.ExecutionSpec {
	return domain.ExecutionSpec{AttemptID: id, IntentID: "i", ProjectID: 1, ScreenID: 2, SKUID: 3, Buyers: []domain.Buyer{{LogicalID: "b"}}, StartMode: domain.StartImmediate, Deadline: time.Now().Add(time.Minute)}
}

// startTestServer creates a real gRPC worker server with auto-generated mTLS
// and returns a ready client stub and a cleanup function.
func startTestServer(t *testing.T, config Config, factory BackendFactory) (pb.WorkerServiceClient, func()) {
	t.Helper()
	if err := config.Normalize(); err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(config, factory)
	if err != nil {
		t.Fatal(err)
	}
	serverTLS, err := NewServerTLSConfig(config.CACertPEM, config.ServerCertPEM, config.ServerKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	lis, err := net.Listen("tcp", config.Listen)
	if err != nil {
		t.Fatal(err)
	}
	grpcSrv := grpc.NewServer(grpc.Creds(credentials.NewTLS(serverTLS)))
	pb.RegisterWorkerServiceServer(grpcSrv, NewGRPCService(srv))
	go func() { _ = grpcSrv.Serve(lis) }()

	clientTLS := mustClientTLS(t, config.DataDir)

	conn, err := grpc.NewClient(
		lis.Addr().String(),
		grpc.WithTransportCredentials(credentials.NewTLS(clientTLS)),
	)
	if err != nil {
		t.Fatal(err)
	}
	cli := pb.NewWorkerServiceClient(conn)
	return cli, func() {
		conn.Close()
		grpcSrv.Stop()
		_ = lis.Close()
	}
}

func mustClientTLS(t *testing.T, dir string) *tls.Config {
	t.Helper()
	caPEM, err := os.ReadFile(filepath.Join(dir, "ca.pem"))
	if err != nil {
		t.Fatal(err)
	}
	caKeyPEM, err := os.ReadFile(filepath.Join(dir, "ca-key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	clientCertPEM, clientKeyPEM, err := GenerateClientCert(caPEM, caKeyPEM, "test-client")
	if err != nil {
		t.Fatal(err)
	}
	tlsCfg, err := NewClientTLSConfig(caPEM, clientCertPEM, clientKeyPEM, "localhost")
	if err != nil {
		t.Fatal(err)
	}
	return tlsCfg
}

func mustSpecProto(t *testing.T, s domain.ExecutionSpec) *pb.ExecutionSpec {
	t.Helper()
	sp := &pb.ExecutionSpec{
		AttemptId:  s.AttemptID,
		IntentId:   s.IntentID,
		ProjectId:  s.ProjectID,
		ScreenId:   s.ScreenID,
		SkuId:      s.SKUID,
		Buyers:     []*pb.Buyer{{LogicalId: s.Buyers[0].LogicalID}},
		StartMode:  pb.StartMode_START_IMMEDIATE,
		IntervalMs: s.IntervalMS,
		Deadline:   timestamppb.New(s.Deadline),
	}
	return sp
}

func TestAuthIdempotencyConflictAndSingleSlot(t *testing.T) {
	block := make(chan struct{})
	config := Config{Listen: "127.0.0.1:0", DataDir: t.TempDir(), PollInterval: 10 * time.Second}
	cli, cleanup := startTestServer(t, config, func(domain.ExecutionSpec) (executor.Backend, error) { return backend{block: block}, nil })
	defer cleanup()

	ctx := context.Background()
	sp := mustSpecProto(t, workerSpec("a"))

	// Create.
	_, err := cli.Submit(ctx, &pb.SubmitRequest{Spec: sp})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Idempotent.
	_, err = cli.Submit(ctx, &pb.SubmitRequest{Spec: sp})
	if err != nil {
		t.Fatalf("idempotent: %v", err)
	}

	// Conflict with different spec.
	copyOther := *sp
	copyOther.SkuId = 9
	_, err = cli.Submit(ctx, &pb.SubmitRequest{Spec: &copyOther})
	if code := status.Code(err); code != codes.AlreadyExists {
		t.Fatalf("conflict: code=%s err=%v", code, err)
	}

	// Busy — worker single slot.
	busySp := mustSpecProto(t, workerSpec("b"))
	_, err = cli.Submit(ctx, &pb.SubmitRequest{Spec: busySp})
	if code := status.Code(err); code != codes.ResourceExhausted {
		t.Fatalf("busy: code=%s err=%v", code, err)
	}

	// Stop.
	_, err = cli.Stop(ctx, &pb.StopRequest{AttemptId: "a"})
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
}

func TestSuccessPersistsAndSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	listen := "127.0.0.1:0"

	config := Config{Listen: listen, DataDir: dir, PollInterval: 10 * time.Second}
	_ = config.Normalize() // auto-generates TLS

	factory := func(domain.ExecutionSpec) (executor.Backend, error) {
		return backend{outcome: executor.Outcome{OrderID: "o"}}, nil
	}
	srv, err := NewServer(config, factory)
	if err != nil {
		t.Fatal(err)
	}
	serverTLS, err := NewServerTLSConfig(config.CACertPEM, config.ServerCertPEM, config.ServerKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	lis, err := net.Listen("tcp", listen)
	if err != nil {
		t.Fatal(err)
	}
	grpcSrv := grpc.NewServer(grpc.Creds(credentials.NewTLS(serverTLS)))
	pb.RegisterWorkerServiceServer(grpcSrv, NewGRPCService(srv))
	go func() { _ = grpcSrv.Serve(lis) }()
	defer grpcSrv.Stop()

	clientTLS := mustClientTLS(t, dir)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(credentials.NewTLS(clientTLS)))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cli := pb.NewWorkerServiceClient(conn)

	ctx := context.Background()
	sp := mustSpecProto(t, workerSpec("a"))
	_, err = cli.Submit(ctx, &pb.SubmitRequest{Spec: sp})
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(time.Second)
	succeeded := false
	for time.Now().Before(deadline) {
		resp, err := cli.Status(ctx, &pb.StatusRequest{AttemptId: "a"})
		if err != nil {
			t.Fatal(err)
		}
		if resp.Status.State == pb.AttemptState_ATTEMPT_SUCCEEDED {
			succeeded = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !succeeded {
		t.Fatal("attempt did not succeed")
	}

	// Logs.
	logsResp, err := cli.Logs(ctx, &pb.LogsRequest{AttemptId: "a"})
	if err != nil {
		t.Fatal(err)
	}
	foundResponse := false
	for _, entry := range logsResp.Entries {
		if entry.Stage == "response" {
			foundResponse = true
		}
	}
	if !foundResponse {
		t.Fatalf("execution response missing from logs: %#v", logsResp.Entries)
	}

	// Restart.
	grpcSrv.Stop()
	_ = conn.Close()

	restarted, err := NewServer(config, factory)
	if err != nil {
		t.Fatal(err)
	}
	lis2, err := net.Listen("tcp", lis.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	grpcSrv2 := grpc.NewServer(grpc.Creds(credentials.NewTLS(serverTLS)))
	pb.RegisterWorkerServiceServer(grpcSrv2, NewGRPCService(restarted))
	go func() { _ = grpcSrv2.Serve(lis2) }()
	defer grpcSrv2.Stop()

	conn2, err := grpc.NewClient(lis2.Addr().String(), grpc.WithTransportCredentials(credentials.NewTLS(clientTLS)))
	if err != nil {
		t.Fatal(err)
	}
	defer conn2.Close()
	cli2 := pb.NewWorkerServiceClient(conn2)

	resp, err := cli2.Status(ctx, &pb.StatusRequest{AttemptId: "a"})
	if err != nil {
		t.Fatalf("persisted status: %v", err)
	}
	if resp.Status.State != pb.AttemptState_ATTEMPT_SUCCEEDED {
		t.Fatalf("persisted state=%v", resp.Status.State)
	}

	changed := mustSpecProto(t, workerSpec("a"))
	changed.SkuId = 99
	_, err = cli2.Submit(ctx, &pb.SubmitRequest{Spec: changed})
	if code := status.Code(err); code != codes.AlreadyExists {
		t.Fatalf("persisted spec conflict: code=%s err=%v", code, err)
	}
}

func TestTaskLogsAreBoundedAndRedacted(t *testing.T) {
	config := Config{Listen: "127.0.0.1:0", DataDir: t.TempDir(), PollInterval: 10 * time.Second}
	srv, err := NewServer(config, func(domain.ExecutionSpec) (executor.Backend, error) { return backend{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	_ = config.Normalize()

	taskSpec := workerSpec("a")
	task := &task{spec: taskSpec, specHash: taskSpec.Hash()}
	for i := 0; i < 510; i++ {
		srv.logTask(task, "response", "SESSDATA=must-not-leak", 1, true)
	}
	if len(task.logs) != 500 || task.logs[0].Sequence != 11 {
		t.Fatalf("unexpected bounded log window: len=%d first=%d", len(task.logs), task.logs[0].Sequence)
	}
	if strings.Contains(task.logs[0].Message, "must-not-leak") {
		t.Fatalf("credential leaked in API log: %#v", task.logs[0])
	}
}

func TestLeaseDefaultsAndStatusRenews(t *testing.T) {
	block := make(chan struct{})
	config := Config{Listen: "127.0.0.1:0", DataDir: t.TempDir(), PollInterval: 10 * time.Second}
	cli, cleanup := startTestServer(t, config, func(domain.ExecutionSpec) (executor.Backend, error) { return backend{block: block}, nil })
	defer cleanup()

	ctx := context.Background()
	sp := mustSpecProto(t, workerSpec("a"))
	_, err := cli.Submit(ctx, &pb.SubmitRequest{Spec: sp})
	if err != nil {
		t.Fatal(err)
	}

	// First status.
	resp, err := cli.Status(ctx, &pb.StatusRequest{AttemptId: "a"})
	if err != nil {
		t.Fatal(err)
	}
	first := resp.Status.LeaseUntil.AsTime()
	if first.Sub(time.Now()) < 179*time.Second {
		t.Fatalf("lease too short: %v", first.Sub(time.Now()))
	}

	time.Sleep(2 * time.Millisecond)

	// Renewed status.
	resp2, err := cli.Status(ctx, &pb.StatusRequest{AttemptId: "a"})
	if err != nil {
		t.Fatal(err)
	}
	renewed := resp2.Status.LeaseUntil.AsTime()
	if !renewed.After(first) {
		t.Fatal("status did not renew lease")
	}
}

func TestSuccessStoreNeverPersistsCredentials(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenSuccessStore(filepath.Join(dir, "success-orders.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	result := domain.ExecutionResult{AttemptID: "a", SpecHash: "hash", Success: true, Credentials: domain.Credentials{Cookies: map[string]string{"SESSDATA": "secret"}, RefreshToken: "refresh-secret"}}
	if err := store.Append(result); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "success-orders.jsonl"))
	if strings.Contains(string(data), "secret") {
		t.Fatalf("credential leaked to success record: %s", data)
	}
	info, _ := os.Stat(filepath.Join(dir, "success-orders.jsonl"))
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
}

func TestSuccessStoreKeepsLatestPartialSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "success-orders.jsonl")
	store, err := OpenSuccessStore(path)
	if err != nil {
		t.Fatal(err)
	}
	first := domain.ExecutionResult{AttemptID: "a", State: domain.AttemptRunning, SubOrders: []domain.SubOrderResult{{BuyerIndex: 0, State: domain.SubOrderPending}}}
	second := domain.ExecutionResult{AttemptID: "a", State: domain.AttemptRunning, Partial: true, SubOrders: []domain.SubOrderResult{{BuyerIndex: 0, State: domain.SubOrderSucceeded, OrderID: "order-1"}, {BuyerIndex: 1, State: domain.SubOrderFailed}}}
	if err := store.Append(first); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(second); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenSuccessStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got := reopened.All()["a"]
	if !got.Partial || len(got.SubOrders) != 2 || got.SubOrders[0].OrderID != "order-1" {
		t.Fatalf("unexpected snapshot: %#v", got)
	}
}
