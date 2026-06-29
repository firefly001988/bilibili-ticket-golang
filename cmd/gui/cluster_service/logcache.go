package cluster_service

import (
	"encoding/json"
	"os"
	"path/filepath"

	clusterworker "bilibili-ticket-golang/cluster/worker"
)

const (
	logCacheDir    = "logs/attempt"
	maxCachedLines = 100
)

// readCachedLogs reads the last maxCachedLines log entries from the local
// JSONL cache file for the given attempt by seeking from the end of the
// file. Returns an empty slice if the file does not exist.
func readCachedLogs(attemptID string) ([]clusterworker.LogEntry, error) {
	path := filepath.Join(logCacheDir, attemptID+".jsonl")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	if size == 0 {
		return nil, nil
	}

	// Read backwards from EOF in chunks, collecting at most maxCachedLines.
	const chunkSize = 4096
	var buf []byte
	pos := size
	linesFound := 0

	for pos > 0 && linesFound <= maxCachedLines {
		readSize := int64(chunkSize)
		if pos < readSize {
			readSize = pos
		}
		pos -= readSize

		chunk := make([]byte, readSize)
		if _, err := f.ReadAt(chunk, pos); err != nil {
			return nil, err
		}
		buf = append(chunk, buf...)

		// Count newlines in the accumulated buffer.
		linesFound = 0
		for _, b := range buf {
			if b == '\n' {
				linesFound++
			}
		}
	}

	// buf now contains the trailing portion of the file that includes
	// at least maxCachedLines (or the whole file). Carve off any leading
	// partial line and keep only the last maxCachedLines.
	lines := splitLines(buf)
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1] // drop trailing empty from final '\n'
	}
	if len(lines) > maxCachedLines {
		lines = lines[len(lines)-maxCachedLines:]
	}

	entries := make([]clusterworker.LogEntry, 0, len(lines))
	for _, line := range lines {
		var entry clusterworker.LogEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// splitLines splits a byte slice by '\n' without allocating strings.
func splitLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			lines = append(lines, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}

// appendCachedLogs appends new log entries to the local JSONL cache file.
// Creates the directory and file if they do not exist.
func appendCachedLogs(attemptID string, entries []clusterworker.LogEntry) error {
	if len(entries) == 0 {
		return nil
	}
	path := filepath.Join(logCacheDir, attemptID+".jsonl")
	if err := os.MkdirAll(logCacheDir, 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, entry := range entries {
		data, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		if _, err := f.Write(append(data, '\n')); err != nil {
			return err
		}
	}
	return f.Sync()
}

// mergeLogs merges cached and live log entries, deduplicating by Sequence.
// Returns the merged result plus the new entries that should be appended
// to the cache (live entries whose Sequence is not in cached).
func mergeLogs(cached, live []clusterworker.LogEntry) (merged []clusterworker.LogEntry, newEntries []clusterworker.LogEntry) {
	seen := make(map[int64]struct{}, len(cached))
	for _, e := range cached {
		seen[e.Sequence] = struct{}{}
	}
	merged = make([]clusterworker.LogEntry, 0, len(cached)+len(live))
	merged = append(merged, cached...)
	for _, e := range live {
		if _, ok := seen[e.Sequence]; !ok {
			merged = append(merged, e)
			newEntries = append(newEntries, e)
		}
	}
	return merged, newEntries
}

// removeCachedLogs deletes the local log cache file for the given attempt.
func removeCachedLogs(attemptID string) error {
	path := filepath.Join(logCacheDir, attemptID+".jsonl")
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
