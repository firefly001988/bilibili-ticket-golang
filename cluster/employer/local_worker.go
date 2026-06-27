package employer

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"bilibili-ticket-golang/cluster/domain"
	"bilibili-ticket-golang/cluster/executor"
	"bilibili-ticket-golang/cluster/worker"
)

// localWorkerSlot tracks a single in-process worker instance.
type localWorkerSlot struct {
	server   *worker.Server
	listener net.Listener
	node     domain.WorkerNode
	dataDir  string
}

type LocalWorkerManager struct {
	mu     sync.Mutex
	client *WorkerClient
	slots  map[string]*localWorkerSlot
	opts   LocalWorkerOptions
}

type LocalWorkerOptions struct {
	PluginDir     string
	CaptchaPlugin string
	Version       string
}

// AddWorker creates and starts a new in-process worker. Use id="" for
// auto-generation of a unique ID.
func (m *LocalWorkerManager) AddWorker(ctx context.Context, client *WorkerClient, id, name, listen string, opts LocalWorkerOptions) (domain.WorkerNode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.slots == nil {
		m.slots = make(map[string]*localWorkerSlot)
	}
	m.client = client
	m.opts = opts

	if id == "" {
		id = m.nextID()
	}
	if name == "" {
		name = id
	}

	// If already running under this ID, stop it first.
	if slot := m.slots[id]; slot != nil && slot.server != nil {
		if slot.listener != nil {
			slot.listener.Close()
		}
		m.slots[id] = nil
	}

	dataDir := filepath.Join("data", id)
	return m.startLocked(ctx, id, name, listen, dataDir)
}

// StartWorker starts an existing (previously added) local worker.
func (m *LocalWorkerManager) StartWorker(ctx context.Context, client *WorkerClient, workerID string, opts LocalWorkerOptions) (domain.WorkerNode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.slots == nil {
		m.slots = make(map[string]*localWorkerSlot)
	}
	m.client = client
	m.opts = opts

	slot := m.slots[workerID]
	if slot == nil {
		return domain.WorkerNode{}, fmt.Errorf("worker %q not added yet", workerID)
	}
	if slot.server != nil {
		if slot.listener != nil {
			slot.listener.Close()
		}
		m.slots[workerID] = nil
	}

	return m.startLocked(ctx, workerID, slot.node.Name, slot.node.Address, slot.dataDir)
}

// StopWorker stops a specific local worker by closing its listener.
func (m *LocalWorkerManager) StopWorker(workerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.slots == nil {
		return nil
	}
	slot := m.slots[workerID]
	if slot != nil && slot.listener != nil {
		slot.listener.Close()
	}
	m.slots[workerID] = nil
	return nil
}

// RemoveWorker stops and permanently removes a worker.
func (m *LocalWorkerManager) RemoveWorker(workerID string) error {
	return m.StopWorker(workerID)
}

// Start starts the primary "local" worker on 127.0.0.1:18080.
func (m *LocalWorkerManager) Start(ctx context.Context, client *WorkerClient, opts LocalWorkerOptions) (domain.WorkerNode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.slots == nil {
		m.slots = make(map[string]*localWorkerSlot)
	}
	m.client = client
	m.opts = opts

	if slot, exists := m.slots["local"]; exists && slot != nil && slot.server != nil {
		return slot.node, nil
	}
	return m.startLocked(ctx, "local", "Local Worker", "127.0.0.1:18080", "data/local-worker")
}

// Stop stops all local workers.
func (m *LocalWorkerManager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, slot := range m.slots {
		if slot != nil && slot.listener != nil {
			slot.listener.Close()
		}
		m.slots[id] = nil
	}
	return nil
}

func (m *LocalWorkerManager) Healthy(workerID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	slot := m.slots[workerID]
	return slot != nil && slot.server != nil
}

func (m *LocalWorkerManager) nextID() string {
	next := 2
	for {
		id := fmt.Sprintf("local-%d", next)
		if _, exists := m.slots[id]; !exists {
			return id
		}
		next++
	}
}

func (m *LocalWorkerManager) startLocked(ctx context.Context, id, name, listen, dataDir string) (domain.WorkerNode, error) {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return domain.WorkerNode{}, err
	}

	tlsBundle, _, err := worker.LoadOrGenerateLocalTLS(dataDir)
	if err != nil {
		return domain.WorkerNode{}, fmt.Errorf("local TLS for %s: %w", id, err)
	}

	caPEM, certPEM, keyPEM, err := worker.LoadLocalServerTLS(dataDir)
	if err != nil {
		return domain.WorkerNode{}, fmt.Errorf("load local server TLS for %s: %w", id, err)
	}

	config := worker.Config{
		Listen:          listen,
		DataDir:         dataDir,
		WorkerID:        id,
		Version:         m.opts.Version,
		PollIntervalSec: 15,
		PluginDir:       m.opts.PluginDir,
		CaptchaPlugin:   m.opts.CaptchaPlugin,
		CACertPEM:       caPEM,
		ServerCertPEM:   certPEM,
		ServerKeyPEM:    keyPEM,
	}
	configData, _ := json.MarshalIndent(config, "", "  ")
	_ = os.WriteFile(filepath.Join(dataDir, "worker.json"), configData, 0600)

	factory := func(spec domain.ExecutionSpec) (executor.Backend, error) {
		return executor.NewBilibiliBackend(spec.Credentials)
	}

	server, err := worker.NewServer(config, factory)
	if err != nil {
		return domain.WorkerNode{}, fmt.Errorf("init local worker %s: %w", id, err)
	}

	lis, err := net.Listen("tcp", listen)
	if err != nil {
		return domain.WorkerNode{}, fmt.Errorf("listen %s: %w", listen, err)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- server.ServeOn(lis) }()

	serverName := "localhost"
	node := domain.WorkerNode{
		ID:            id,
		Name:          name,
		Address:       listen,
		Type:          domain.WorkerTypeLocal,
		Enabled:       true,
		TLSServerName: serverName,
	}

	if err := m.client.SetTLSFromConfig(id, domain.WorkerTLSConfig{
		CACertPEM:     tlsBundle.CAPEM,
		ClientCertPEM: tlsBundle.CertPEM,
		ClientKeyPEM:  tlsBundle.KeyPEM,
		ServerName:    serverName,
	}); err != nil {
		_ = lis.Close()
		return domain.WorkerNode{}, fmt.Errorf("TLS for local worker %s: %w", id, err)
	}

	m.slots[id] = &localWorkerSlot{server: server, listener: lis, node: node, dataDir: dataDir}
	go func(l net.Listener, srv *worker.Server) {
		if serveErr := <-errCh; serveErr != nil {
			fmt.Fprintf(os.Stderr, "[local-worker %s] gRPC error: %v\n", id, serveErr)
		}
		m.mu.Lock()
		if slot := m.slots[id]; slot != nil && slot.server == srv {
			_ = l.Close()
			m.slots[id] = nil
		}
		m.mu.Unlock()
	}(lis, server)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		checkCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		_, err := m.client.Health(checkCtx, node)
		cancel()
		if err == nil {
			return node, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = lis.Close()
	m.slots[id] = nil
	return domain.WorkerNode{}, fmt.Errorf("local worker %s unhealthy", id)
}
