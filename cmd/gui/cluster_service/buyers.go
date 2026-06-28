package cluster_service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"bilibili-ticket-golang/cluster/dispatcher"
	"bilibili-ticket-golang/cluster/domain"
	clusterstorage "bilibili-ticket-golang/cluster/storage"
)

// ProvisionBuyer creates or updates a buyer on a specific account.
func (s *ClusterService) ProvisionBuyer(document string, confirmed bool) error {
	var input struct {
		AccountID string       `json:"accountId"`
		Buyer     domain.Buyer `json:"buyer"`
	}
	if err := json.Unmarshal([]byte(document), &input); err != nil {
		return err
	}
	_, err := s.accounts.EnsureBuyer(context.Background(), input.AccountID, input.Buyer, confirmed)
	return err
}

// SyncBuyerToAccount provisions a logical buyer onto a target Bilibili
// account. If the buyer already exists on that account's real-name list the
// call is a no-op; otherwise a new buyer is created on the remote account.
func (s *ClusterService) SyncBuyerToAccount(logicalBuyerID, targetAccountID string) error {
	buyer, err := s.repository.LogicalBuyer(context.Background(), logicalBuyerID)
	if err != nil {
		return fmt.Errorf("logical buyer %s: %w", logicalBuyerID, err)
	}
	_, err = s.accounts.EnsureBuyer(context.Background(), targetAccountID, buyer, true)
	if err != nil {
		return err
	}
	return s.refreshResources(context.Background())
}

// SyncBuyerToAllAccounts provisions a logical buyer onto every enabled
// Bilibili account that does not already have it. Accounts that already
// contain the buyer are skipped without any remote calls.
func (s *ClusterService) SyncBuyerToAllAccounts(logicalBuyerID string) error {
	buyer, err := s.repository.LogicalBuyer(context.Background(), logicalBuyerID)
	if err != nil {
		return fmt.Errorf("logical buyer %s: %w", logicalBuyerID, err)
	}
	accounts, err := s.repository.ListAccounts(context.Background())
	if err != nil {
		return err
	}
	for _, account := range accounts {
		if !account.Enabled {
			continue
		}
		if _, err := s.repository.BuyerMapping(context.Background(), account.ID, logicalBuyerID); err == nil {
			// Already provisioned on this account — skip.
			continue
		}
		if _, err := s.accounts.EnsureBuyer(context.Background(), account.ID, buyer, true); err != nil {
			return err
		}
	}
	return s.refreshResources(context.Background())
}

type buyerResolver struct {
	repository *clusterstorage.Repository
	ensureFn   func(ctx context.Context, accountID string, buyer domain.Buyer) error
}

func (r buyerResolver) Resolve(ctx context.Context, accountID string, buyers []domain.Buyer) ([]domain.Buyer, error) {
	result := append([]domain.Buyer(nil), buyers...)
	for i := range result {
		mapping, err := r.repository.BuyerMapping(ctx, accountID, result[i].LogicalID)
		if err == nil {
			// Buyer already mapped — merge in the BuyerID. Use the
			// stored unmasked record as the authoritative source so
			// workers always receive complete real-name data.
			full, fullErr := r.repository.LogicalBuyer(ctx, result[i].LogicalID)
			if fullErr != nil {
				// Fall back to the incoming buyer when the DB record
				// is unavailable (e.g. masked and therefore filtered).
				// This preserves forward progress for already-mapped
				// buyers whose DB entry is temporarily masked.
				result[i].BuyerID = mapping.BuyerID
				continue
			}
			full.BuyerID = mapping.BuyerID
			result[i] = full
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("buyer %s is not provisioned on account %s: %w", result[i].LogicalID, accountID, err)
		}
		// Buyer not yet provisioned on this account — auto-sync using the
		// stored unmasked real-name information, then retry the mapping
		// lookup once.
		if r.ensureFn == nil {
			return nil, fmt.Errorf("%w: buyer %s on account %s", dispatcher.ErrBuyerUnavailable, result[i].LogicalID, accountID)
		}
		full, fullErr := r.repository.LogicalBuyer(ctx, result[i].LogicalID)
		if fullErr != nil {
			// LogicalBuyer itself guards against masked data — if it
			// failed (e.g. masked ID card), we cannot proceed.
			return nil, fmt.Errorf("%w: buyer %s on account %s (logical lookup: %w)", dispatcher.ErrBuyerUnavailable, result[i].LogicalID, accountID, fullErr)
		}
		if ensureErr := r.ensureFn(ctx, accountID, full); ensureErr != nil {
			return nil, fmt.Errorf("%w: buyer %s on account %s (ensure: %w)", dispatcher.ErrBuyerUnavailable, result[i].LogicalID, accountID, ensureErr)
		}
		mapping2, retryErr := r.repository.BuyerMapping(ctx, accountID, result[i].LogicalID)
		if retryErr != nil {
			return nil, fmt.Errorf("%w: buyer %s on account %s after ensure: %w", dispatcher.ErrBuyerUnavailable, result[i].LogicalID, accountID, retryErr)
		}
		full.BuyerID = mapping2.BuyerID
		result[i] = full
	}
	return result, nil
}

type biliProvisioner struct{}

func (biliProvisioner) ListBuyers(_ context.Context, account domain.Account) ([]domain.Buyer, domain.Credentials, error) {
	client, jar, err := accountClient(account)
	if err != nil {
		return nil, account.Credentials, err
	}
	err, list := client.GetRealnameBuyerListNew()
	if err != nil {
		return nil, credentialsFrom(client, jar, account.Credentials), err
	}
	result := make([]domain.Buyer, len(list))
	for i, value := range list {
		buyer := domain.Buyer{BuyerID: value.Id, Name: value.Name, Tel: value.Tel, IDCard: value.IdCard, Type: value.IdType}
		// Fetch full sensitive data (unmasked ID card, phone, etc.) for each buyer.
		if value.Id > 0 {
			sensitiveErr, sensitive := client.GetTargetBuyerSensitiveData(value.Id)
			if sensitiveErr == nil && sensitive.PersonalId != "" {
				buyer.IDCard = sensitive.PersonalId
				if sensitive.Tel != "" {
					buyer.Tel = sensitive.Tel
				}
				if sensitive.Name != "" {
					buyer.Name = sensitive.Name
				}
				if sensitive.IdType != 0 {
					buyer.Type = sensitive.IdType
				}
			}
		}
		result[i] = buyer
	}
	return result, credentialsFrom(client, jar, account.Credentials), nil
}

func (biliProvisioner) CreateBuyer(ctx context.Context, account domain.Account, buyer domain.Buyer) (domain.Buyer, domain.Credentials, error) {
	client, jar, err := accountClient(account)
	if err != nil {
		return domain.Buyer{}, account.Credentials, err
	}
	if err := client.CreateBuyer(buyer.Name, buyer.Tel, buyer.Type, buyer.IDCard, false); err != nil {
		return domain.Buyer{}, credentialsFrom(client, jar, account.Credentials), err
	}
	list, credentials, err := (biliProvisioner{}).ListBuyers(ctx, account)
	if err != nil {
		return domain.Buyer{}, credentials, err
	}
	for _, value := range list {
		if value.Name == buyer.Name && value.Tel == buyer.Tel {
			buyer.BuyerID = value.BuyerID
			return buyer, credentials, nil
		}
	}
	return domain.Buyer{}, credentials, fmt.Errorf("created buyer was not returned by API")
}
