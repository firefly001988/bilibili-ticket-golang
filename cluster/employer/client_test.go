package employer

import (
	"context"
	"crypto/tls"
	"net"
	"testing"
	"time"

	"bilibili-ticket-golang/cluster/domain"
	"bilibili-ticket-golang/cluster/executor"
	"bilibili-ticket-golang/cluster/worker"
	pb "bilibili-ticket-golang/cluster/worker/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

type employerBackend struct{}

func (employerBackend) Attempt(context.Context, domain.ExecutionSpec) executor.Outcome {
	return executor.Outcome{OrderID: "order"}
}
func (employerBackend) Credentials() domain.Credentials { return domain.Credentials{Version: 2} }

// startTestWorker creates a gRPC worker server with auto-generated TLS and
// returns the address, the client TLS config, and a cleanup function.
func startTestWorker(t *testing.T, backendFactory worker.BackendFactory) (address string, clientTLS *tls.Config, cleanup func()) {
	t.Helper()

	caCertPEM, caKeyPEM, err := worker.GenerateCA()
	if err != nil {
		t.Fatal(err)
	}
	serverCertPEM, serverKeyPEM, err := worker.GenerateServerCert(caCertPEM, caKeyPEM, []string{"localhost", "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	clientCertPEM, clientKeyPEM, err := worker.GenerateClientCert(caCertPEM, caKeyPEM, "test-client")
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
	srv, err := worker.NewServer(config, backendFactory)
	if err != nil {
		t.Fatal(err)
	}

	grpcServer := grpc.NewServer(grpc.Creds(credentials.NewTLS(serverTLS)))
	pb.RegisterWorkerServiceServer(grpcServer, worker.NewGRPCService(srv))
	go func() { _ = grpcServer.Serve(lis) }()

	return lis.Addr().String(), clientTLS, func() {
		grpcServer.Stop()
		_ = lis.Close()
	}
}

func TestWorkerClientUsesSameProtocolForRemoteWorkers(t *testing.T) {
	address, tlsCfg, cleanup := startTestWorker(t, func(domain.ExecutionSpec) (executor.Backend, error) { return employerBackend{}, nil })
	defer cleanup()

	node := domain.WorkerNode{ID: "remote", Address: address, TLSServerName: "localhost"}
	client := NewWorkerClient()
	client.SetTLS(node.ID, tlsCfg)

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
			logs, err := client.Logs(context.Background(), node, "a")
			if err != nil {
				t.Fatal(err)
			}
			if len(logs) == 0 {
				t.Fatal("worker logs missing")
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("attempt did not complete")
}
