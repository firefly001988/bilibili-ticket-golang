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
	_, err := s.accounts.ImportMany(context.Background(), []byte(document))
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

// SyncAllAccountBuyersFast runs a multi-account buyer sync that minimises
// Bilibili API calls: masked lists are fetched concurrently from every
// account, deduplicated, and GetBuyerSensitiveData is called exactly once
// per unique logical buyer (with fallback accounts on failure).
func (s *ClusterService) SyncAllAccountBuyersFast() ([]domain.Buyer, error) {
	ctx := context.Background()
	buyers, err := s.accounts.SyncAllBuyersFast(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.refreshResources(ctx); err != nil {
		return nil, err
	}
	return buyers, nil
}

// UpdateBuyerPhone sets the primary phone number for a logical buyer.
// The default value shown to the user is the Tel currently stored in
// the database.  The phone is also added to Tels if it is new.
func (s *ClusterService) UpdateBuyerPhone(logicalBuyerID, phone string) (domain.Buyer, error) {
	buyer, err := s.accounts.SetBuyerPhone(context.Background(), logicalBuyerID, phone)
	if err != nil {
		return domain.Buyer{}, err
	}
	// Refresh the snapshot so the frontend sees the update immediately.
	if refreshErr := s.refreshResources(context.Background()); refreshErr != nil {
		return buyer, refreshErr
	}
	return buyer, nil
}

// RemoveBuyerFromAccount deletes the buyer mapping for a specific account
// and removes the buyer from the Bilibili account's real-name list.
func (s *ClusterService) RemoveBuyerFromAccount(logicalBuyerID, accountID string) error {
	if err := s.accounts.RemoveBuyerFromAccount(context.Background(), logicalBuyerID, accountID); err != nil {
		return err
	}
	return s.refreshResources(context.Background())
}

// RemoveBuyerFromAllAccounts removes the buyer from every mapped Bilibili
// account and deletes the logical buyer record.
func (s *ClusterService) RemoveBuyerFromAllAccounts(logicalBuyerID string) error {
	if err := s.accounts.RemoveBuyerFromAllAccounts(context.Background(), logicalBuyerID); err != nil {
		return err
	}
	return s.refreshResources(context.Background())
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
