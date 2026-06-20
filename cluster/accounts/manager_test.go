package accounts

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"bilibili-ticket-golang/cluster/domain"
	"bilibili-ticket-golang/cluster/storage"
)

type provisioner struct{ created int }

func (p *provisioner) ListBuyers(context.Context, domain.Account) ([]domain.Buyer, domain.Credentials, error) {
	return nil, domain.Credentials{Version: 1}, nil
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
	if len(account.Credentials.DeviceProfile) == 0 {
		t.Fatal("device profile not generated")
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
