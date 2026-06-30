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
	"log"
	"strings"
	"sync"
	"time"

	"bilibili-ticket-golang/cluster/domain"
)

type Repository interface {
	PutAccount(context.Context, domain.Account, *int64) error
	Account(context.Context, string) (domain.Account, error)
	PutLogicalBuyer(context.Context, domain.Buyer) error
	LogicalBuyer(context.Context, string) (domain.Buyer, error)
	ListLogicalBuyers(context.Context) ([]domain.Buyer, error)
	SetBuyerPhone(context.Context, string, string) (domain.Buyer, error)
	PutBuyerMapping(context.Context, domain.AccountBuyerMapping) error
	BuyerMapping(context.Context, string, string) (domain.AccountBuyerMapping, error)
	ListBuyerMappings(context.Context) ([]domain.AccountBuyerMapping, error)
	DeleteBuyerMapping(context.Context, string, string) error
	DeleteBuyerAllMappings(context.Context, string) error
	ListAccounts(context.Context) ([]domain.Account, error)
}

type Provisioner interface {
	ListBuyers(context.Context, domain.Account) ([]domain.Buyer, domain.Credentials, error)
	ListBuyersMasked(context.Context, domain.Account) ([]domain.Buyer, domain.Credentials, error)
	GetBuyerSensitiveData(context.Context, domain.Account, int64) (domain.Buyer, error)
	CreateBuyer(context.Context, domain.Account, domain.Buyer) (domain.Buyer, domain.Credentials, error)
	DeleteBuyer(context.Context, domain.Account, int64) error
}

type Manager struct {
	repository      Repository
	provisioner     Provisioner
	syncConcurrency int
}

func NewManager(repository Repository, provisioner Provisioner) *Manager {
	return &Manager{repository: repository, provisioner: provisioner, syncConcurrency: 1}
}

// SetSyncConcurrency sets the maximum number of accounts to sync buyers for
// concurrently. The default is 1 (serial).
func (m *Manager) SetSyncConcurrency(n int) {
	if n < 1 {
		n = 1
	}
	m.syncConcurrency = n
}

// SetBuyerPhone updates the primary phone number for a logical buyer and
// adds it to the Tels collection if not already present.
func (m *Manager) SetBuyerPhone(ctx context.Context, logicalBuyerID, phone string) (domain.Buyer, error) {
	if phone == "" {
		return domain.Buyer{}, errors.New("phone number is required")
	}
	return m.repository.SetBuyerPhone(ctx, logicalBuyerID, phone)
}

// RemoveBuyerFromAccount deletes the buyer mapping for a specific account
// and optionally calls the Bilibili API to remove the buyer from the
// account's real-name list.
func (m *Manager) RemoveBuyerFromAccount(ctx context.Context, logicalBuyerID, accountID string) error {
	mapping, err := m.repository.BuyerMapping(ctx, accountID, logicalBuyerID)
	if err != nil {
		return fmt.Errorf("buyer %s not mapped on account %s: %w", logicalBuyerID, accountID, err)
	}
	account, err := m.repository.Account(ctx, accountID)
	if err != nil {
		return fmt.Errorf("account %s: %w", accountID, err)
	}
	// Try to delete from Bilibili — best effort, log on failure.
	if err := m.provisioner.DeleteBuyer(ctx, account, mapping.BuyerID); err != nil {
		log.Printf("[accounts] delete buyer %d from Bilibili account %s failed: %v", mapping.BuyerID, accountID, err)
	}
	if err := m.repository.DeleteBuyerMapping(ctx, accountID, logicalBuyerID); err != nil {
		return fmt.Errorf("delete mapping: %w", err)
	}
	log.Printf("[accounts] removed buyer %s from account %s", logicalBuyerID, accountID)
	return nil
}

// RemoveBuyerFromAllAccounts removes the buyer from every mapped account
// (calling the Bilibili API for each) and then deletes the logical buyer.
func (m *Manager) RemoveBuyerFromAllAccounts(ctx context.Context, logicalBuyerID string) error {
	mappings, err := m.repository.ListBuyerMappings(ctx)
	if err != nil {
		return err
	}
	var firstErr error
	for _, mapping := range mappings {
		if mapping.LogicalBuyerID != logicalBuyerID {
			continue
		}
		if err := m.RemoveBuyerFromAccount(ctx, mapping.LogicalBuyerID, mapping.AccountID); err != nil {
			log.Printf("[accounts] remove buyer %s from account %s: %v", logicalBuyerID, mapping.AccountID, err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	// Clean up the logical buyer itself (mappings already removed by above).
	if err := m.repository.DeleteBuyerAllMappings(ctx, logicalBuyerID); err != nil {
		log.Printf("[accounts] delete buyer all mappings for %s: %v", logicalBuyerID, err)
	}
	if firstErr != nil {
		return firstErr
	}
	log.Printf("[accounts] removed buyer %s from all accounts", logicalBuyerID)
	return nil
}

type CredentialDocument struct {
	ID            string              `json:"id"`
	Name          string              `json:"name"`
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
	account := domain.Account{ID: document.ID, Name: document.Name, Enabled: true, Credentials: domain.Credentials{Cookies: document.Cookies, CookieJar: document.CookieJar, RefreshToken: document.RefreshToken, Version: 1, DeviceProfile: document.DeviceProfile}}
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
		// Skip buyers whose ID card or phone is still masked — we must
		// never persist desensitised data. The buyer will be picked up on
		// a future sync once the full real-name information is available.
		if (buyer.IDCard != "" && isMasked(buyer.IDCard)) ||
			(buyer.Tel != "" && isMasked(buyer.Tel)) {
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
		if isMasked(logical.IDCard) || logical.IDCard == "" {
			continue
		}
		// Populate Tels from Tel for single-account sync path.
		if logical.Tel != "" && !isMasked(logical.Tel) && len(logical.Tels) == 0 {
			logical.Tels = []string{logical.Tel}
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
//
// Accounts are synced concurrently up to the configured syncConcurrency limit.
func (m *Manager) SyncAllBuyers(ctx context.Context) ([]domain.Buyer, error) {
	accounts, err := m.repository.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}
	concurrency := m.syncConcurrency
	if concurrency <= 0 {
		concurrency = 1
	}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error

	for _, account := range accounts {
		if !account.Enabled {
			continue
		}
		accID := account.ID
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if _, err := m.SyncBuyers(ctx, accID); err != nil {
				log.Printf("[accounts] sync buyers for account %s: %v", accID, err)
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return m.repository.ListLogicalBuyers(ctx)
}

// buyerAccEntry records a buyer's ID on a specific account together with
// the account credentials (refreshed from the API) for later persistence.
type buyerAccEntry struct {
	AccountID   string
	BuyerID     int64
	Credentials domain.Credentials
}

// SyncAllBuyersFast performs a multi-account buyer sync that minimises
// Bilibili API calls:
//  1. Concurrently fetch masked buyer lists from every enabled account.
//  2. Deduplicate across accounts (by logicalBuyerID, supplemented with
//     name+tel for fully-masked IDs).
//  3. Call GetBuyerSensitiveData exactly once per unique logical buyer,
//     falling back to alternate accounts when the first attempt fails.
//  4. Persist unmasked records and account→buyer mappings.
func (m *Manager) SyncAllBuyersFast(ctx context.Context) ([]domain.Buyer, error) {
	accounts, err := m.repository.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}

	// ── Phase 1: parallel masked list fetch ──────────────────────────
	concurrency := m.syncConcurrency
	if concurrency <= 0 {
		concurrency = 1
	}
	type accountFetch struct {
		Account     domain.Account
		Buyers      []domain.Buyer
		Credentials domain.Credentials
		Err         error
	}
	fetches := make([]accountFetch, 0, len(accounts))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, account := range accounts {
		if !account.Enabled {
			continue
		}
		acc := account
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			buyers, creds, e := m.provisioner.ListBuyersMasked(ctx, acc)
			if e != nil {
				log.Printf("[accounts] fast sync: list masked buyers for account %s: %v", acc.ID, e)
			}
			mu.Lock()
			fetches = append(fetches, accountFetch{Account: acc, Buyers: buyers, Credentials: creds, Err: e})
			mu.Unlock()
		}()
	}
	wg.Wait()

	// Collect refreshed credentials per account (even on error we keep old).
	accountCreds := make(map[string]domain.Credentials, len(fetches))
	for _, f := range fetches {
		if f.Err == nil && f.Credentials.Version > 0 {
			accountCreds[f.Account.ID] = f.Credentials
		}
	}

	// ── Phase 2: dedup across accounts ───────────────────────────────
	// dedupKey is the primary dedup key for a buyer across accounts.
	type dedupKey struct {
		logicalID string
	}
	// grouped maps a dedup key to the accounts that own this buyer
	// and whether any account already returned unmasked data.
	type groupedEntry struct {
		entries     []buyerAccEntry
		buyer       domain.Buyer // best available buyer data so far
		hasUnmasked bool
	}
	grouped := make(map[dedupKey]*groupedEntry)

	for _, f := range fetches {
		if f.Err != nil {
			continue
		}
		for _, buyer := range f.Buyers {
			if buyer.BuyerID <= 0 {
				continue
			}
			logID := logicalBuyerID(buyer)
			key := dedupKey{logicalID: logID}
			ge, ok := grouped[key]
			if !ok {
				ge = &groupedEntry{buyer: buyer}
				grouped[key] = ge
			}
			ge.entries = append(ge.entries, buyerAccEntry{
				AccountID:   f.Account.ID,
				BuyerID:     buyer.BuyerID,
				Credentials: f.Credentials,
			})
			// Merge best available data from each account's buyer instance.
			// Never overwrite an existing unmasked value; log on conflict.
			if buyer.IDCard != "" && !isMasked(buyer.IDCard) {
				if ge.buyer.IDCard == "" || isMasked(ge.buyer.IDCard) {
					ge.buyer.IDCard = buyer.IDCard
					ge.hasUnmasked = true
				} else if ge.buyer.IDCard != buyer.IDCard {
					log.Printf("[accounts] fast sync: ID card mismatch for %s: existing=%s incoming=%s",
						logID, ge.buyer.IDCard, buyer.IDCard)
				}
			}
			// Collect all unique non-masked phone numbers across accounts.
			if buyer.Tel != "" && !isMasked(buyer.Tel) {
				exists := false
				for _, t := range ge.buyer.Tels {
					if t == buyer.Tel {
						exists = true
						break
					}
				}
				if !exists {
					ge.buyer.Tels = append(ge.buyer.Tels, buyer.Tel)
				}
				// Keep the first non-masked phone as the primary Tel.
				if ge.buyer.Tel == "" || isMasked(ge.buyer.Tel) {
					ge.buyer.Tel = buyer.Tel
				}
			}
			if buyer.Name != "" && ge.buyer.Name == "" {
				ge.buyer.Name = buyer.Name
			}
		}
	}

	// ── Phase 3: on-demand unmasked fetch ─────────────────────────────
	for _, ge := range grouped {
		if ge.hasUnmasked && !isMasked(ge.buyer.IDCard) {
			continue // already complete, skip sensitive API
		}
		if len(ge.entries) == 0 {
			continue
		}
		// Try each account that owns this buyer until one succeeds.
		var fetched domain.Buyer
		for _, entry := range ge.entries {
			acc, accErr := m.repository.Account(ctx, entry.AccountID)
			if accErr != nil {
				log.Printf("[accounts] fast sync: lookup account %s for sensitive data: %v", entry.AccountID, accErr)
				continue
			}
			sensitive, sensErr := m.provisioner.GetBuyerSensitiveData(ctx, acc, entry.BuyerID)
			if sensErr != nil {
				log.Printf("[accounts] fast sync: sensitive data for buyer %d on account %s: %v", entry.BuyerID, entry.AccountID, sensErr)
				continue
			}
			if sensitive.IDCard == "" || isMasked(sensitive.IDCard) {
				continue
			}
			fetched = sensitive
			break
		}
		if fetched.IDCard != "" {
			merged := mergeBuyer(ge.buyer, fetched)
			if !isMasked(merged.IDCard) {
				ge.hasUnmasked = true
				ge.buyer = merged
			}
			// Add sensitive phone to Tels if not already present.
			if fetched.Tel != "" && !isMasked(fetched.Tel) {
				exists := false
				for _, t := range ge.buyer.Tels {
					if t == fetched.Tel {
						exists = true
						break
					}
				}
				if !exists {
					ge.buyer.Tels = append(ge.buyer.Tels, fetched.Tel)
				}
			}
		}
	}

	// ── Phase 4: persist ─────────────────────────────────────────────
	var firstErr error
	seenLogical := make(map[string]bool)

	for key, ge := range grouped {
		buyer := ge.buyer
		if buyer.LogicalID == "" {
			buyer.LogicalID = key.logicalID
		}
		// Only persist if we have valid unmasked data.
		if isMasked(buyer.IDCard) || buyer.IDCard == "" {
			// Try to enrich from existing DB record.
			if existing, getErr := m.repository.LogicalBuyer(ctx, buyer.LogicalID); getErr == nil {
				buyer = mergeBuyer(existing, buyer)
			}
			if isMasked(buyer.IDCard) || buyer.IDCard == "" {
				log.Printf("[accounts] fast sync: skipping buyer %s (still masked)", buyer.LogicalID)
				continue
			}
		}
		if buyer.LogicalID == "" {
			continue
		}
		logical := buyer
		logical.BuyerID = 0
		// Merge with existing: keep the most complete info.
		if existing, getErr := m.repository.LogicalBuyer(ctx, logical.LogicalID); getErr == nil {
			logical = mergeBuyer(existing, logical)
		}
		if isMasked(logical.IDCard) || logical.IDCard == "" {
			continue
		}
		// Ensure Tel is populated from Tels when we have phone numbers.
		if logical.Tel == "" && len(logical.Tels) > 0 {
			logical.Tel = logical.Tels[0]
		}
		if err := m.repository.PutLogicalBuyer(ctx, logical); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		seenLogical[logical.LogicalID] = true
		for _, entry := range ge.entries {
			mapping := domain.AccountBuyerMapping{
				AccountID:      entry.AccountID,
				LogicalBuyerID: buyer.LogicalID,
				BuyerID:        entry.BuyerID,
				UpdatedAt:      time.Now(),
			}
			if err := m.repository.PutBuyerMapping(ctx, mapping); err != nil {
				log.Printf("[accounts] fast sync: put mapping %s→%s: %v", entry.AccountID, buyer.LogicalID, err)
			}
		}
	}

	// Persist refreshed credentials for every account.
	for accountID, creds := range accountCreds {
		acc, accErr := m.repository.Account(ctx, accountID)
		if accErr != nil {
			continue
		}
		old := acc.Credentials.Version
		if creds.Version <= old {
			creds.Version = old + 1
		}
		acc.Credentials = creds
		if putErr := m.repository.PutAccount(ctx, acc, &old); putErr != nil {
			log.Printf("[accounts] fast sync: update account %s credentials: %v", accountID, putErr)
		}
	}

	if firstErr != nil {
		return nil, firstErr
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
	} else if !isMasked(incoming.IDCard) && !isMasked(existing.IDCard) &&
		existing.IDCard != "" && incoming.IDCard != "" &&
		existing.IDCard != incoming.IDCard {
		log.Printf("[accounts] mergeBuyer: ID card mismatch for logical %s: existing=%s incoming=%s (keeping existing)",
			existing.LogicalID, existing.IDCard, incoming.IDCard)
	}

	// Prefer existing unmasked phone over incoming masked one.
	if isMasked(incoming.Tel) && !isMasked(existing.Tel) && existing.Tel != "" {
		merged.Tel = existing.Tel
	} else if !isMasked(incoming.Tel) && !isMasked(existing.Tel) &&
		existing.Tel != "" && incoming.Tel != "" &&
		existing.Tel != incoming.Tel {
		log.Printf("[accounts] mergeBuyer: phone mismatch for logical %s: existing=%s incoming=%s (keeping existing)",
			existing.LogicalID, existing.Tel, incoming.Tel)
	}

	// Prefer existing non-empty name if incoming is empty.
	if incoming.Name == "" && existing.Name != "" {
		merged.Name = existing.Name
	}
	// Prefer existing non-empty tel if incoming is empty.
	if incoming.Tel == "" && existing.Tel != "" {
		merged.Tel = existing.Tel
	}
	// Merge Tels arrays, keeping unique values from both sides.
	if len(existing.Tels) > 0 || len(incoming.Tels) > 0 {
		seen := make(map[string]bool)
		for _, t := range existing.Tels {
			if t != "" && !isMasked(t) {
				seen[t] = true
			}
		}
		for _, t := range incoming.Tels {
			if t != "" && !isMasked(t) {
				seen[t] = true
			}
		}
		merged.Tels = make([]string, 0, len(seen))
		for t := range seen {
			merged.Tels = append(merged.Tels, t)
		}
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
		buyer.LogicalID = logicalBuyerID(buyer)
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
	if (logical.IDCard != "" && isMasked(logical.IDCard)) ||
		(logical.Tel != "" && isMasked(logical.Tel)) {
		return domain.AccountBuyerMapping{}, fmt.Errorf("cannot save buyer with masked data: IDCard=%s Tel=%s", logical.IDCard, logical.Tel)
	}
	// Merge with existing data: keep the most complete (unmasked) info.
	if existing, getErr := m.repository.LogicalBuyer(ctx, logical.LogicalID); getErr == nil {
		logical = mergeBuyer(existing, logical)
	}
	if (logical.IDCard != "" && isMasked(logical.IDCard)) ||
		(logical.Tel != "" && isMasked(logical.Tel)) {
		// If Tel is still masked but we have unmasked Tels, use the first.
		if logical.Tel != "" && isMasked(logical.Tel) {
			for _, t := range logical.Tels {
				if t != "" && !isMasked(t) {
					logical.Tel = t
					break
				}
			}
		}
		if isMasked(logical.IDCard) || (logical.Tel != "" && isMasked(logical.Tel)) {
			return domain.AccountBuyerMapping{}, fmt.Errorf("cannot save buyer with masked data after merge: IDCard=%s Tel=%s", logical.IDCard, logical.Tel)
		}
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
		normalizeIDCard(a.IDCard) == normalizeIDCard(b.IDCard) &&
		a.Type == b.Type
}

func randomID(prefix string) string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return prefix + "-" + hex.EncodeToString(b[:])
}
