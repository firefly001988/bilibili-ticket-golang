package accounts

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"bilibili-ticket-golang/cluster/domain"
)

type Repository interface {
	PutAccount(context.Context, domain.Account, *int64) error
	Account(context.Context, string) (domain.Account, error)
	PutLogicalBuyer(context.Context, domain.Buyer) error
	PutBuyerMapping(context.Context, domain.AccountBuyerMapping) error
	BuyerMapping(context.Context, string, string) (domain.AccountBuyerMapping, error)
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
	if err := m.repository.PutLogicalBuyer(ctx, buyer); err != nil {
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
	return a.Name == b.Name && a.Tel == b.Tel && a.IDCard == b.IDCard
}

func randomID(prefix string) string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return prefix + "-" + hex.EncodeToString(b[:])
}
