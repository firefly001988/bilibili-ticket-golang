package cluster_service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	accountsmgr "bilibili-ticket-golang/cluster/accounts"
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

// ExportAccounts exports selected accounts as JSON compatible with ImportAccount.
// accountIDsJSON must be a JSON string array.
func (s *ClusterService) ExportAccounts(accountIDsJSON string) (string, error) {
	var accountIDs []string
	if err := json.Unmarshal([]byte(accountIDsJSON), &accountIDs); err != nil {
		return "", err
	}
	accountIDs = uniqueAccountIDs(accountIDs)
	if len(accountIDs) == 0 {
		return "", fmt.Errorf("no account selected")
	}
	ctx := context.Background()
	documents := make([]accountsmgr.CredentialDocument, 0, len(accountIDs))
	for _, accountID := range accountIDs {
		account, err := s.repository.Account(ctx, accountID)
		if err != nil {
			return "", fmt.Errorf("account %s not found: %w", accountID, err)
		}
		documents = append(documents, credentialDocumentFromAccount(account))
	}
	var (
		data []byte
		err  error
	)
	if len(documents) == 1 {
		data, err = json.MarshalIndent(documents[0], "", "  ")
	} else {
		data, err = json.MarshalIndent(documents, "", "  ")
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// RefreshAccountsStatus refreshes login/VIP/cookie status for selected accounts
// using the same Bilibili API checks used during startup.
func (s *ClusterService) RefreshAccountsStatus(accountIDsJSON string) error {
	var accountIDs []string
	if err := json.Unmarshal([]byte(accountIDsJSON), &accountIDs); err != nil {
		return err
	}
	accountIDs = uniqueAccountIDs(accountIDs)
	if len(accountIDs) == 0 {
		return fmt.Errorf("no account selected")
	}
	ctx := context.Background()
	var failures []string
	for _, accountID := range accountIDs {
		if _, err := s.refreshAccountStatus(ctx, accountID); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", accountID, err))
		}
	}
	if err := s.refreshResources(ctx); err != nil {
		return err
	}
	if len(failures) > 0 {
		return fmt.Errorf("refresh account status partially failed: %s", strings.Join(failures, "; "))
	}
	return nil
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

func credentialDocumentFromAccount(account domain.Account) accountsmgr.CredentialDocument {
	return accountsmgr.CredentialDocument{
		ID:            account.ID,
		Name:          account.Name,
		Cookies:       account.Credentials.Cookies,
		CookieJar:     account.Credentials.CookieJar,
		RefreshToken:  account.Credentials.RefreshToken,
		DeviceProfile: account.Credentials.DeviceProfile,
	}
}

func uniqueAccountIDs(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

type accountStatusRefreshResult struct {
	Refreshed bool
	Disabled  bool
}

func (s *ClusterService) refreshAccountStatus(ctx context.Context, accountID string) (accountStatusRefreshResult, error) {
	account, err := s.repository.Account(ctx, accountID)
	if err != nil {
		return accountStatusRefreshResult{}, err
	}
	client, jar, err := accountClient(account)
	if err != nil {
		return accountStatusRefreshResult{}, err
	}
	client.SetRefreshToken(account.Credentials.RefreshToken)

	loginInfo, statusErr := client.GetAccountStatus()
	if statusErr != nil || loginInfo == nil || !loginInfo.Login || loginInfo.UID == 0 {
		reason := "api error"
		if statusErr != nil {
			reason = statusErr.Error()
		} else if loginInfo == nil {
			reason = "nil response"
		} else if !loginInfo.Login {
			reason = "not logged in"
		}
		log.Printf("[cluster] account %s (%s) login check failed: %s — disabling", account.ID, account.Name, reason)
		account.Enabled = false
		account.Credentials.Version++
		if putErr := s.repository.PutAccount(ctx, account, nil); putErr != nil {
			return accountStatusRefreshResult{}, putErr
		}
		return accountStatusRefreshResult{Disabled: true}, nil
	}

	changed := false
	if !account.Enabled {
		account.Enabled = true
		changed = true
	}
	if account.Name == "" && loginInfo.Name != "" {
		account.Name = loginInfo.Name
		changed = true
	}
	if loginInfo.IsVip != account.VipStatus {
		account.VipStatus = loginInfo.IsVip
		changed = true
		if loginInfo.IsVip == 1 {
			log.Printf("[cluster] account %s (%s) is VIP", account.ID, account.Name)
		}
	}

	result := accountStatusRefreshResult{}
	refreshed, refreshErr := client.CheckAndUpdateCookie()
	if refreshErr != nil {
		log.Printf("[cluster] cookie refresh for account %s: %v", account.ID, refreshErr)
	} else if refreshed {
		updated := credentialsFrom(client, jar, account.Credentials)
		updated.Version = account.Credentials.Version + 1
		account.Credentials = updated
		changed = true
		result.Refreshed = true
	}
	if changed {
		if err := s.repository.PutAccount(ctx, account, nil); err != nil {
			return accountStatusRefreshResult{}, err
		}
	}
	return result, nil
}
