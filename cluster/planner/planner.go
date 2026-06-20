package planner

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"time"

	"bilibili-ticket-golang/cluster/domain"
)

func Plan(macro domain.MacroTask, groups []domain.PurchaseGroup, phase domain.Phase, now time.Time) ([]domain.LogicalOrderIntent, error) {
	if !macro.Dispatchable() {
		return nil, fmt.Errorf("macro task is not dispatchable until event day is reviewed and confirmed")
	}
	capacity := macro.EffectiveCapacity()
	for _, group := range groups {
		if group.MacroTaskID != macro.ID {
			return nil, fmt.Errorf("purchase group %s belongs to another macro", group.ID)
		}
		if len(group.Buyers) == 0 || len(group.Buyers) > capacity {
			return nil, fmt.Errorf("purchase group %s exceeds SKU capacity", group.ID)
		}
	}
	var shapes [][]domain.Buyer
	switch phase {
	case domain.PhasePunctual:
		if macro.SmartMerge {
			shapes = bestFitDecreasing(groups, capacity)
		} else {
			for _, group := range stableGroups(groups) {
				shapes = append(shapes, append([]domain.Buyer(nil), group.Buyers...))
			}
		}
	case domain.PhaseReflow:
		for _, group := range stableGroups(groups) {
			if group.AllowSplit {
				for _, buyer := range group.Buyers {
					shapes = append(shapes, []domain.Buyer{buyer})
				}
			} else {
				shapes = append(shapes, append([]domain.Buyer(nil), group.Buyers...))
			}
		}
	default:
		return nil, fmt.Errorf("unknown phase %q", phase)
	}
	intents := make([]domain.LogicalOrderIntent, 0, len(shapes))
	for index, buyers := range shapes {
		id := intentID(macro.ID, phase, index, buyers)
		intent, err := domain.NewIntent(id, macro, phase, buyers, now)
		if err != nil {
			return nil, err
		}
		intents = append(intents, intent)
	}
	return intents, nil
}

func stableGroups(groups []domain.PurchaseGroup) []domain.PurchaseGroup {
	ordered := append([]domain.PurchaseGroup(nil), groups...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].CreatedAt.Equal(ordered[j].CreatedAt) {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].CreatedAt.Before(ordered[j].CreatedAt)
	})
	return ordered
}

func bestFitDecreasing(groups []domain.PurchaseGroup, capacity int) [][]domain.Buyer {
	ordered := stableGroups(groups)
	sort.SliceStable(ordered, func(i, j int) bool { return len(ordered[i].Buyers) > len(ordered[j].Buyers) })
	var bins [][]domain.Buyer
	for _, group := range ordered {
		best, remainder := -1, capacity+1
		for i, bin := range bins {
			left := capacity - len(bin) - len(group.Buyers)
			if left >= 0 && left < remainder {
				best, remainder = i, left
			}
		}
		if best < 0 {
			bins = append(bins, append([]domain.Buyer(nil), group.Buyers...))
		} else {
			bins[best] = append(bins[best], group.Buyers...)
		}
	}
	return bins
}

func intentID(macroID string, phase domain.Phase, index int, buyers []domain.Buyer) string {
	value := fmt.Sprintf("%s/%s/%d", macroID, phase, index)
	for _, buyer := range buyers {
		value += "/" + buyer.LogicalID
	}
	sum := sha256.Sum256([]byte(value))
	return "intent-" + hex.EncodeToString(sum[:8])
}
