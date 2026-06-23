package accounts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"bilibili-ticket-golang/cluster/domain"
)

type Repository interface {
	PutAccount(context.Context, domain.Account, *int64) error
	Account(context.Context, string) (domain.Account, error)
	PutLogicalBuyer(context.Context, domain.Buyer) error
	LogicalBuyer(context.Context, string) (domain.Buyer, error)
	ListLogicalBuyers(context.Context) ([]domain.Buyer, error)
	PutBuyerMapping(context.Context, domain.AccountBuyerMapping) error
	BuyerMapping(context.Context, string, string) (domain.AccountBuyerMapping, error)
	ListAccounts(context.Context) ([]domain.Account, error)
}

type Provisioner interface {
	ListBuyers(context.Context, domain.Account) ([]domain.Buyer, domain.Credentials, error)
	CreateBuyer(context.Context, domain.Account, domain.Buyer) (domain.Buyer, domain.Credentials, error)
}

type Manager struct {
	repository  Repository
	provisioner Provisioner
}

func NewManager(repository Repository, provisioner Provisioner) *Manager {
	return &Manager{repository: repository, provisioner: provisioner}
}

type CredentialDocument struct {
	ID            string              `json:"id"`
	Name          string              `json:"name"`
	Role          domain.ResourceRole `json:"role"`
	Cookies       map[string]string   `json:"cookies"`
	CookieJar     []domain.HTTPCookie `json:"cookieJar,omitempty"`
	RefreshToken  string              `json:"refreshToken"`
	DeviceProfile json.RawMessage     `json:"deviceProfile,omitempty"`
}

func (m *Manager) Import(ctx context.Context, data []byte) (domain.Account, error) {
	var document CredentialDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return domain.Account{}, err
	}
	if len(document.Cookies) == 0 && len(document.CookieJar) == 0 {
		return domain.Account{}, errors.New("cookies are required")
	}
	if document.ID == "" {
		document.ID = randomID("account")
	}
	if document.Role == "" {
		document.Role = domain.RolePrimary
	}
	account := domain.Account{ID: document.ID, Name: document.Name, Role: document.Role, Enabled: true, Credentials: domain.Credentials{Cookies: document.Cookies, CookieJar: document.CookieJar, RefreshToken: document.RefreshToken, Version: 1, DeviceProfile: document.DeviceProfile}}
	if err := m.repository.PutAccount(ctx, account, nil); err != nil {
		return domain.Account{}, err
	}
	return account, nil
}

// SyncBuyers imports an account's existing Bilibili buyers and assigns stable,
// opaque logical IDs. Users never need to create or manage these IDs.
func (m *Manager) SyncBuyers(ctx context.Context, accountID string) ([]domain.Buyer, error) {
	account, err := m.repository.Account(ctx, accountID)
	if err != nil {
		return nil, err
	}
	remote, credentials, err := m.provisioner.ListBuyers(ctx, account)
	if err != nil {
		return nil, err
	}
	result := make([]domain.Buyer, 0, len(remote))
	for _, buyer := range remote {
		if buyer.BuyerID <= 0 {
			continue
		}
		// Skip buyers whose ID card is still masked — we must never persist
		// desensitised data. The buyer will be picked up on a future sync
		// once the full real-name information is available.
		if buyer.IDCard != "" && isMasked(buyer.IDCard) {
			continue
		}
		buyer.LogicalID = logicalBuyerID(buyer)
		logical := buyer
		logical.BuyerID = 0
		// Merge with existing data: keep the most complete (unmasked) info.
		if existing, getErr := m.repository.LogicalBuyer(ctx, logical.LogicalID); getErr == nil {
			logical = mergeBuyer(existing, logical)
		}
		// Final guard: never write masked data into the database.
		if isMasked(logical.IDCard) {
			continue
		}
		if err := m.repository.PutLogicalBuyer(ctx, logical); err != nil {
			return nil, err
		}
		mapping := domain.AccountBuyerMapping{AccountID: account.ID, LogicalBuyerID: buyer.LogicalID, BuyerID: buyer.BuyerID, UpdatedAt: time.Now()}
		if err := m.repository.PutBuyerMapping(ctx, mapping); err != nil {
			return nil, err
		}
		result = append(result, logical)
	}
	old := account.Credentials.Version
	if credentials.Version <= old {
		credentials.Version = old + 1
	}
	account.Credentials = credentials
	if err := m.repository.PutAccount(ctx, account, &old); err != nil {
		return nil, err
	}
	return result, nil
}

// SyncAllBuyers syncs buyers for every account and ensures the logical_buyers
// table always retains the most complete (unmasked) real-name information
// across all accounts. The same real person appearing as a buyer on multiple
// accounts is matched via logicalBuyerID and deduplicated into a single entry.
func (m *Manager) SyncAllBuyers(ctx context.Context) ([]domain.Buyer, error) {
	accounts, err := m.repository.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}
	for _, account := range accounts {
		if !account.Enabled {
			continue
		}
		if _, err := m.SyncBuyers(ctx, account.ID); err != nil {
			// Continue syncing other accounts even if one fails.
			continue
		}
	}
	return m.repository.ListLogicalBuyers(ctx)
}

// isMasked reports whether the ID card string contains mask characters.
func isMasked(s string) bool {
	return strings.Contains(s, "*")
}

// mergeBuyer combines existing and incoming buyer data, preferring the more
// complete (unmasked) field values. This ensures that a later sync with masked
// data does not overwrite previously stored full real-name information.
func mergeBuyer(existing, incoming domain.Buyer) domain.Buyer {
	merged := incoming
	// Prefer existing unmasked ID card over incoming masked one.
	if isMasked(incoming.IDCard) && !isMasked(existing.IDCard) && existing.IDCard != "" {
		merged.IDCard = existing.IDCard
	}
	// Prefer existing non-empty name if incoming is empty.
	if incoming.Name == "" && existing.Name != "" {
		merged.Name = existing.Name
	}
	// Prefer existing non-empty tel if incoming is empty.
	if incoming.Tel == "" && existing.Tel != "" {
		merged.Tel = existing.Tel
	}
	return merged
}

// normalizeIDCard strips mask characters (e.g. '*' in "3201**********1234")
// and normalises case/whitespace so that the same person produces the same
// logical ID regardless of whether the API returned masked or unmasked data.
// When the full unmasked ID card is available this is a no-op; when only the
// masked version is available the mask characters are removed so the hash is
// based on the visible portions only.
func normalizeIDCard(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '*' {
			return -1
		}
		return r
	}, strings.ToUpper(strings.TrimSpace(s)))
}

func logicalBuyerID(buyer domain.Buyer) string {
	identity := strings.ToLower(strings.TrimSpace(buyer.Name)) + "\x00" + normalizeIDCard(buyer.IDCard) + fmt.Sprintf("\x00%d", buyer.Type)
	sum := sha256.Sum256([]byte(identity))
	return "buyer-" + hex.EncodeToString(sum[:12])
}

// EnsureBuyer never mutates a Bilibili account without explicit confirmation.
func (m *Manager) EnsureBuyer(ctx context.Context, accountID string, buyer domain.Buyer, confirmed bool) (domain.AccountBuyerMapping, error) {
	if buyer.LogicalID == "" {
		return domain.AccountBuyerMapping{}, errors.New("logical buyer id is required")
	}
	if mapping, err := m.repository.BuyerMapping(ctx, accountID, buyer.LogicalID); err == nil {
		return mapping, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return domain.AccountBuyerMapping{}, err
	}
	account, err := m.repository.Account(ctx, accountID)
	if err != nil {
		return domain.AccountBuyerMapping{}, err
	}
	remote, credentials, err := m.provisioner.ListBuyers(ctx, account)
	if err != nil {
		return domain.AccountBuyerMapping{}, err
	}
	for _, candidate := range remote {
		if sameBuyer(candidate, buyer) {
			return m.saveMapping(ctx, account, buyer, candidate.BuyerID, credentials)
		}
	}
	if !confirmed {
		return domain.AccountBuyerMapping{}, ErrConfirmationRequired
	}
	created, credentials, err := m.provisioner.CreateBuyer(ctx, account, buyer)
	if err != nil {
		return domain.AccountBuyerMapping{}, err
	}
	return m.saveMapping(ctx, account, buyer, created.BuyerID, credentials)
}

var ErrConfirmationRequired = errors.New("explicit confirmation is required to create buyer")

func (m *Manager) saveMapping(ctx context.Context, account domain.Account, buyer domain.Buyer, buyerID int64, credentials domain.Credentials) (domain.AccountBuyerMapping, error) {
	if buyerID <= 0 {
		return domain.AccountBuyerMapping{}, fmt.Errorf("invalid provisioned buyer id")
	}
	logical := buyer
	logical.BuyerID = 0
	// Never persist desensitised (masked) data into the database.
	if logical.IDCard != "" && isMasked(logical.IDCard) {
		return domain.AccountBuyerMapping{}, fmt.Errorf("cannot save buyer with masked ID card: %s", logical.IDCard)
	}
	// Merge with existing data: keep the most complete (unmasked) info.
	if existing, getErr := m.repository.LogicalBuyer(ctx, logical.LogicalID); getErr == nil {
		logical = mergeBuyer(existing, logical)
	}
	if isMasked(logical.IDCard) {
		return domain.AccountBuyerMapping{}, fmt.Errorf("cannot save buyer with masked ID card after merge: %s", logical.IDCard)
	}
	if err := m.repository.PutLogicalBuyer(ctx, logical); err != nil {
		return domain.AccountBuyerMapping{}, err
	}
	mapping := domain.AccountBuyerMapping{AccountID: account.ID, LogicalBuyerID: buyer.LogicalID, BuyerID: buyerID, UpdatedAt: time.Now()}
	if err := m.repository.PutBuyerMapping(ctx, mapping); err != nil {
		return domain.AccountBuyerMapping{}, err
	}
	old := account.Credentials.Version
	if credentials.Version <= old {
		credentials.Version = old + 1
	}
	account.Credentials = credentials
	if err := m.repository.PutAccount(ctx, account, &old); err != nil {
		return domain.AccountBuyerMapping{}, err
	}
	return mapping, nil
}

func sameBuyer(a, b domain.Buyer) bool {
	if a.BuyerID > 0 && b.BuyerID > 0 {
		return a.BuyerID == b.BuyerID
	}
	return strings.EqualFold(strings.TrimSpace(a.Name), strings.TrimSpace(b.Name)) &&
		a.Tel == b.Tel &&
		normalizeIDCard(a.IDCard) == normalizeIDCard(b.IDCard)
}

func randomID(prefix string) string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return prefix + "-" + hex.EncodeToString(b[:])
}
