package scheduler

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const logPersistDir = "logs"

// maxPersistedEntriesPerTask caps the number of log entries stored per task in memory.
const maxPersistedEntriesPerTask = 10000

// maxLinesOnLoad is the maximum number of lines retained per task on restart.
const maxLinesOnLoad = 100

// LogStorage provides persistent storage for task logs using JSON-lines format.
// Each task gets its own file: logs/<taskID>.log
// Writes are append-only; on restart each file is truncated to the last maxLinesOnLoad lines.
type LogStorage struct {
	mu      sync.RWMutex
	dirPath string
	entries map[string][]LogEntry
}

// NewLogStorage creates a new LogStorage with the default directory path.
func NewLogStorage() *LogStorage {
	return &LogStorage{
		dirPath: logPersistDir,
		entries: make(map[string][]LogEntry),
	}
}

// filePath returns the full path for a task's log file.
func (ls *LogStorage) filePath(taskID string) string {
	return filepath.Join(ls.dirPath, taskID+".log")
}

// Load reads all persisted .log files from the logs/ directory.
// Each file is truncated to the last maxLinesOnLoad lines (oldest entries discarded).
// Missing directory is not an error (first launch).
func (ls *LogStorage) Load() error {
	if err := os.MkdirAll(ls.dirPath, 0755); err != nil {
		return err
	}

	dirEntries, err := os.ReadDir(ls.dirPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	ls.mu.Lock()
	defer ls.mu.Unlock()

	for _, de := range dirEntries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".log") {
			continue
		}
		taskID := strings.TrimSuffix(de.Name(), ".log")
		logs, err := ls.readAndTruncateLocked(taskID)
		if err != nil {
			continue // skip corrupted files
		}
		if len(logs) > 0 {
			ls.entries[taskID] = logs
		}
	}
	return nil
}

// readAndTruncateLocked reads a JSON-lines log file, keeps only the last
// maxLinesOnLoad entries, will not rewrite the file. Caller must hold the write lock.
func (ls *LogStorage) readAndTruncateLocked(taskID string) ([]LogEntry, error) {
	fp := ls.filePath(taskID)
	file, err := os.Open(fp)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	var allEntries []LogEntry
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry LogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue // skip corrupted lines
		}
		allEntries = append(allEntries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Truncate to last maxLinesOnLoad
	if len(allEntries) > maxLinesOnLoad {
		allEntries = allEntries[len(allEntries)-maxLinesOnLoad:]
	}

	return allEntries, nil
}

// writeAllLocked overwrites the file with the given entries as JSON lines.
func (ls *LogStorage) writeAllLocked(taskID string, entries []LogEntry) error {
	fp := ls.filePath(taskID)
	if len(entries) == 0 {
		_ = os.Remove(fp)
		return nil
	}

	file, err := os.OpenFile(fp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	for _, entry := range entries {
		data, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		if _, err := file.Write(data); err != nil {
			return err
		}
		if _, err := file.Write([]byte("\n")); err != nil {
			return err
		}
	}
	return nil
}

// ── Public API ─────────────────────────────────────────────

// Save is a no-op in append-only mode (entries are persisted immediately on Append).
// Kept for API compatibility.
func (ls *LogStorage) Save() error { return nil }

// Append adds a log entry for the given task and appends it to disk immediately.
// The in-memory buffer is capped at maxPersistedEntriesPerTask.
func (ls *LogStorage) Append(taskID string, entry LogEntry) {
	ls.mu.Lock()
	defer ls.mu.Unlock()

	ls.entries[taskID] = append(ls.entries[taskID], entry)

	if len(ls.entries[taskID]) > maxPersistedEntriesPerTask {
		ls.entries[taskID] = ls.entries[taskID][len(ls.entries[taskID])-maxPersistedEntriesPerTask:]
	}

	// Append one JSON line to the log file
	_ = os.MkdirAll(ls.dirPath, 0755)
	fp := ls.filePath(taskID)
	f, err := os.OpenFile(fp, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	data, _ := json.Marshal(entry)
	data = append(data, '\n')
	f.Write(data)
}

// GetEntries returns all persisted log entries for a task (oldest first).
func (ls *LogStorage) GetEntries(taskID string) []LogEntry {
	ls.mu.RLock()
	defer ls.mu.RUnlock()
	entries := ls.entries[taskID]
	if entries == nil {
		return nil
	}
	out := make([]LogEntry, len(entries))
	copy(out, entries)
	return out
}

// GetAllTaskIDs returns the IDs of all tasks that have persisted logs.
func (ls *LogStorage) GetAllTaskIDs() []string {
	ls.mu.RLock()
	defer ls.mu.RUnlock()
	ids := make([]string, 0, len(ls.entries))
	for id := range ls.entries {
		ids = append(ids, id)
	}
	return ids
}

// Clear removes all persisted logs for a task (memory + disk).
func (ls *LogStorage) Clear(taskID string) {
	ls.mu.Lock()
	delete(ls.entries, taskID)
	ls.mu.Unlock()
	_ = os.Remove(ls.filePath(taskID))
}

// Flush is a no-op in append-only mode (entries are persisted immediately on Append).
// Kept for API compatibility.
func (ls *LogStorage) Flush() {}
