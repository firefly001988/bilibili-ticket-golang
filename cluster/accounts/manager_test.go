package accounts

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"bilibili-ticket-golang/cluster/domain"
	"bilibili-ticket-golang/cluster/storage"
)

type provisioner struct {
	created int
	buyers  map[string][]domain.Buyer
}

func (p *provisioner) ListBuyers(_ context.Context, account domain.Account) ([]domain.Buyer, domain.Credentials, error) {
	return p.buyers[account.ID], domain.Credentials{Version: account.Credentials.Version + 1}, nil
}

func (p *provisioner) ListBuyersMasked(ctx context.Context, account domain.Account) ([]domain.Buyer, domain.Credentials, error) {
	return p.ListBuyers(ctx, account)
}

func (p *provisioner) GetBuyerSensitiveData(_ context.Context, _ domain.Account, buyerID int64) (domain.Buyer, error) {
	for _, b := range p.buyers {
		for _, bb := range b {
			if bb.BuyerID == buyerID {
				return bb, nil
			}
		}
	}
	return domain.Buyer{}, errors.New("buyer not found")
}

func (p *provisioner) DeleteBuyer(_ context.Context, _ domain.Account, buyerID int64) error {
	return nil
}

func TestSyncBuyersCreatesOpaqueCrossAccountIdentity(t *testing.T) {
	r, err := storage.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	p := &provisioner{buyers: map[string][]domain.Buyer{
		"a": {{BuyerID: 11, Name: "张三", Tel: "13800000000", IDCard: "110101199001011234", Type: 0}},
		"b": {{BuyerID: 99, Name: "张三", Tel: "13800000000", IDCard: "110101199001011234", Type: 0}},
	}}
	manager := NewManager(r, p)
	ctx := context.Background()
	for _, id := range []string{"a", "b"} {
		if err := r.PutAccount(ctx, domain.Account{ID: id, Enabled: true, Credentials: domain.Credentials{Version: 1}}, nil); err != nil {
			t.Fatal(err)
		}
	}
	first, err := manager.SyncBuyers(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.SyncBuyers(ctx, "b")
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || len(second) != 1 || first[0].LogicalID == "" || first[0].LogicalID != second[0].LogicalID {
		t.Fatalf("buyers were not matched automatically: first=%#v second=%#v", first, second)
	}
	if first[0].BuyerID != 0 || second[0].BuyerID != 0 {
		t.Fatalf("account-specific buyer ids leaked into logical buyers: %#v %#v", first, second)
	}
	for accountID, want := range map[string]int64{"a": 11, "b": 99} {
		mapping, err := r.BuyerMapping(ctx, accountID, first[0].LogicalID)
		if err != nil || mapping.BuyerID != want {
			t.Fatalf("mapping %s=%#v err=%v", accountID, mapping, err)
		}
	}
}
func (p *provisioner) CreateBuyer(_ context.Context, _ domain.Account, buyer domain.Buyer) (domain.Buyer, domain.Credentials, error) {
	p.created++
	buyer.BuyerID = 42
	return buyer, domain.Credentials{Version: 2}, nil
}

func TestImportAndExplicitProvisioning(t *testing.T) {
	r, err := storage.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	p := &provisioner{}
	manager := NewManager(r, p)
	ctx := context.Background()
	account, err := manager.Import(ctx, []byte(`{"id":"a","cookies":{"SESSDATA":"secret"}}`))
	if err != nil {
		t.Fatal(err)
	}
	buyer := domain.Buyer{LogicalID: "person", Name: "A", IDCard: "x"}
	if _, err := manager.EnsureBuyer(ctx, account.ID, buyer, false); !errors.Is(err, ErrConfirmationRequired) || p.created != 0 {
		t.Fatalf("confirmation guard failed: %v", err)
	}
	mapping, err := manager.EnsureBuyer(ctx, account.ID, buyer, true)
	if err != nil {
		t.Fatal(err)
	}
	if mapping.BuyerID != 42 || p.created != 1 {
		t.Fatalf("unexpected mapping: %#v", mapping)
	}
	if _, err := manager.EnsureBuyer(ctx, account.ID, buyer, false); err != nil || p.created != 1 {
		t.Fatalf("mapping was not reused: %v", err)
	}
}

func TestImportManyAcceptsCredentialArray(t *testing.T) {
	r, err := storage.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	manager := NewManager(r, &provisioner{})
	ctx := context.Background()
	accounts, err := manager.ImportMany(ctx, []byte(`[
		{"id":"bili-1","name":"A","cookies":{"SESSDATA":"a"}},
		{"id":"bili-2","name":"B","cookies":{"SESSDATA":"b"}}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 2 || accounts[0].ID != "bili-1" || accounts[1].ID != "bili-2" {
		t.Fatalf("unexpected imported accounts: %#v", accounts)
	}
	for _, id := range []string{"bili-1", "bili-2"} {
		if _, err := r.Account(ctx, id); err != nil {
			t.Fatalf("account %s was not persisted: %v", id, err)
		}
	}
}
