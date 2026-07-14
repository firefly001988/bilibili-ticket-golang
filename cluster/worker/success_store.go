package worker

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"

	"bilibili-ticket-golang/cluster/domain"
)

type SuccessStore struct {
	mu      sync.Mutex
	path    string
	results map[string]domain.ExecutionResult
}

func OpenSuccessStore(path string) (*SuccessStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	s := &SuccessStore{path: path, results: make(map[string]domain.ExecutionResult)}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDONLY, 0600)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	_ = os.Chmod(path, 0600)
	reader := bufio.NewReader(f)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			var result domain.ExecutionResult
			if json.Unmarshal(line, &result) == nil && result.AttemptID != "" {
				s.results[result.AttemptID] = result
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	return s, nil
}

func (s *SuccessStore) Append(result domain.ExecutionResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	persisted := result
	persisted.Credentials = domain.Credentials{}
	data, err := json.Marshal(persisted)
	if err == nil {
		_, err = f.Write(append(data, '\n'))
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		s.results[result.AttemptID] = persisted
	}
	return err
}

func (s *SuccessStore) All() map[string]domain.ExecutionResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]domain.ExecutionResult, len(s.results))
	for k, v := range s.results {
		out[k] = v
	}
	return out
}
