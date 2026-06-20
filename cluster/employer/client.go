package employer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"bilibili-ticket-golang/cluster/dispatcher"
	"bilibili-ticket-golang/cluster/domain"
	"bilibili-ticket-golang/cluster/worker"
)

type WorkerClient struct {
	httpClient *http.Client
	mu         sync.RWMutex
	keys       map[string]string
}

func NewWorkerClient() *WorkerClient {
	return &WorkerClient{httpClient: &http.Client{Timeout: 20 * time.Second}, keys: make(map[string]string)}
}
func (c *WorkerClient) SetKey(workerID, key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.keys[workerID] = key
}

func (c *WorkerClient) RemoveKey(workerID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.keys, workerID)
}

func (c *WorkerClient) Submit(ctx context.Context, node domain.WorkerNode, spec domain.ExecutionSpec) error {
	response, err := c.do(ctx, node, http.MethodPost, "/v1/tasks", spec)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted && response.StatusCode != http.StatusOK {
		return responseError(response)
	}
	return nil
}

func (c *WorkerClient) Status(ctx context.Context, node domain.WorkerNode, attemptID string) (dispatcher.WorkerStatus, error) {
	response, err := c.do(ctx, node, http.MethodGet, "/v1/tasks/"+attemptID, nil)
	if err != nil {
		return dispatcher.WorkerStatus{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return dispatcher.WorkerStatus{}, responseError(response)
	}
	var status worker.Status
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		return dispatcher.WorkerStatus{}, err
	}
	return dispatcher.WorkerStatus{State: status.State, Result: status.Result}, nil
}

func (c *WorkerClient) Stop(ctx context.Context, node domain.WorkerNode, attemptID string) error {
	response, err := c.do(ctx, node, http.MethodPost, "/v1/tasks/"+attemptID+"/stop", nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted && response.StatusCode != http.StatusOK {
		return responseError(response)
	}
	return nil
}

func (c *WorkerClient) Ack(ctx context.Context, node domain.WorkerNode, attemptID string) error {
	response, err := c.do(ctx, node, http.MethodPost, "/v1/tasks/"+attemptID+"/ack", nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return responseError(response)
	}
	return nil
}

func (c *WorkerClient) Health(ctx context.Context, node domain.WorkerNode) (map[string]any, error) {
	response, err := c.do(ctx, node, http.MethodGet, "/v1/health", nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, responseError(response)
	}
	var health map[string]any
	err = json.NewDecoder(response.Body).Decode(&health)
	return health, err
}

func (c *WorkerClient) do(ctx context.Context, node domain.WorkerNode, method, path string, value any) (*http.Response, error) {
	var body io.Reader
	if value != nil {
		data, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(node.BaseURL, "/")+path, body)
	if err != nil {
		return nil, err
	}
	c.mu.RLock()
	key := c.keys[node.ID]
	c.mu.RUnlock()
	if key == "" {
		return nil, fmt.Errorf("no control key configured for worker %s", node.ID)
	}
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", "application/json")
	return c.httpClient.Do(request)
}

func responseError(response *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	return fmt.Errorf("worker returned %s: %s", response.Status, strings.TrimSpace(string(data)))
}
