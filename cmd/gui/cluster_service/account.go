package cluster_service

import (
	"context"
	"encoding/json"
	"fmt"

	"bilibili-ticket-golang/cluster/domain"
)

// SetAccountTags updates user-managed tags for an account.
func (s *ClusterService) SetAccountTags(accountID string, tagsJSON string) error {
	var tags []string
	if err := json.Unmarshal([]byte(tagsJSON), &tags); err != nil {
		return err
	}
	ctx := context.Background()
	account, err := s.repository.Account(ctx, accountID)
	if err != nil {
		return fmt.Errorf("account %s not found: %w", accountID, err)
	}
	oldVersion := account.Credentials.Version
	account.Tags = normalizeAccountTags(tags)
	if err := s.repository.PutAccount(ctx, account, &oldVersion); err != nil {
		return err
	}
	return s.refreshResources(ctx)
}

// ImportAccount imports an account from an encoded credential document.
func (s *ClusterService) ImportAccount(document string) error {
	_, err := s.accounts.Import(context.Background(), []byte(document))
	if err == nil {
		err = s.refreshResources(context.Background())
	}
	return err
}

// SyncAccountBuyers synchronizes buyers from a single account into the
// logical buyer pool, deduplicating by real-name identity.
func (s *ClusterService) SyncAccountBuyers(accountID string) ([]domain.Buyer, error) {
	buyers, err := s.accounts.SyncBuyers(context.Background(), accountID)
	if err != nil {
		return nil, err
	}
	if err := s.refreshResources(context.Background()); err != nil {
		return nil, err
	}
	return buyers, nil
}

// SyncAllAccountBuyers syncs buyers from every enabled account and ensures
// the logical_buyers table retains the most complete (unmasked) real-name
// information. The same real person on multiple accounts is matched and
// deduplicated into a single logical buyer entry.
func (s *ClusterService) SyncAllAccountBuyers() ([]domain.Buyer, error) {
	buyers, err := s.accounts.SyncAllBuyers(context.Background())
	if err != nil {
		return nil, err
	}
	if err := s.refreshResources(context.Background()); err != nil {
		return nil, err
	}
	return buyers, nil
}

// DeleteAccount removes an account. The account must not have active attempts.
func (s *ClusterService) DeleteAccount(accountID string) error {
	for _, attempt := range s.dispatcher.Attempts() {
		if attempt.AccountID == accountID && !attempt.State.Terminal() {
			return fmt.Errorf("account is used by active attempt %s", attempt.ID)
		}
	}
	if err := s.repository.DeleteAccount(context.Background(), accountID); err != nil {
		return err
	}
	return s.refreshResources(context.Background())
}
