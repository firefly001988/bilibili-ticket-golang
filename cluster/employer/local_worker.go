package employer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"bilibili-ticket-golang/cluster/domain"
	"bilibili-ticket-golang/cluster/worker"
)

type LocalWorkerManager struct {
	mu      sync.Mutex
	command *exec.Cmd
	node    domain.WorkerNode
	client  *WorkerClient
}

type LocalWorkerOptions struct {
	BinaryPath string
	DataDir    string
	Listen     string
	Version    string
}

func (m *LocalWorkerManager) Start(ctx context.Context, client *WorkerClient, options LocalWorkerOptions) (domain.WorkerNode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.command != nil && m.command.Process != nil {
		return m.node, nil
	}
	if options.BinaryPath == "" {
		executable, err := os.Executable()
		if err != nil {
			return domain.WorkerNode{}, err
		}
		options.BinaryPath = filepath.Join(filepath.Dir(executable), "ticket-worker")
	}
	if options.DataDir == "" {
		options.DataDir = "data/local-worker"
	}
	if options.Listen == "" {
		options.Listen = "127.0.0.1:18080"
	}
	if err := os.MkdirAll(options.DataDir, 0700); err != nil {
		return domain.WorkerNode{}, err
	}
	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		return domain.WorkerNode{}, err
	}
	key := hex.EncodeToString(keyBytes)
	keyPath := filepath.Join(options.DataDir, "control.key")
	if err := os.WriteFile(keyPath, []byte(key), 0600); err != nil {
		return domain.WorkerNode{}, err
	}
	config := worker.Config{Listen: options.Listen, BearerKey: key, DataDir: options.DataDir, WorkerID: "local", Version: options.Version, PollIntervalSec: 15}
	configData, _ := json.MarshalIndent(config, "", "  ")
	configPath := filepath.Join(options.DataDir, "worker.json")
	if err := os.WriteFile(configPath, configData, 0600); err != nil {
		return domain.WorkerNode{}, err
	}
	command := exec.Command(options.BinaryPath, "serve", "--config", configPath)
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err := command.Start(); err != nil {
		return domain.WorkerNode{}, err
	}
	node := domain.WorkerNode{ID: "local", Name: "Local Worker", BaseURL: "http://" + options.Listen, Role: domain.RolePrimary, Enabled: true}
	client.SetKey(node.ID, key)
	m.command, m.node, m.client = command, node, client
	go func() {
		_ = command.Wait()
		m.mu.Lock()
		if m.command == command {
			m.command = nil
		}
		m.mu.Unlock()
	}()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		checkCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		_, err := client.Health(checkCtx, node)
		cancel()
		if err == nil {
			return node, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = command.Process.Kill()
	return domain.WorkerNode{}, fmt.Errorf("local worker did not become healthy")
}

func (m *LocalWorkerManager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.command == nil || m.command.Process == nil {
		return nil
	}
	err := m.command.Process.Kill()
	m.command = nil
	return err
}
