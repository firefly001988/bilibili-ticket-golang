package cluster_service

import (
	"strings"

	"bilibili-ticket-golang/cluster/domain"
)

func normalizeWorkerTags(tags []string, typ domain.WorkerType) []string {
	seen := make(map[string]struct{}, len(tags))
	result := make([]string, 0, len(tags))
	system := map[string]struct{}{
		string(domain.WorkerTypeLocal):  {},
		string(domain.WorkerTypeRemote): {},
	}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if _, reserved := system[tag]; reserved {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		result = append(result, tag)
	}
	return result
}

func workerEffectiveTags(worker domain.WorkerNode) []string {
	result := normalizeWorkerTags(worker.Tags, worker.Type)
	systemTag := string(worker.Type)
	if systemTag == "" {
		systemTag = string(domain.WorkerTypeRemote)
	}
	return append([]string{systemTag}, result...)
}
