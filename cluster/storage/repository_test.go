package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"bilibili-ticket-golang/cluster/domain"
	"bilibili-ticket-golang/cmd/gui/store/configuration"
	response "bilibili-ticket-golang/lib/models/bili/response"
)

func openTestRepository(t *testing.T) *Repository {
	t.Helper()
	r, err := Open(filepath.Join(t.TempDir(), "employer.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func TestDatabasePermissionsAndCredentialCAS(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "employer.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
	account := domain.Account{ID: "a", Enabled: true, Credentials: domain.Credentials{Version: 1}}
	if err := r.PutAccount(context.Background(), account, nil); err != nil {
		t.Fatal(err)
	}
	wrong := int64(2)
	account.Credentials.Version = 3
	if err := r.PutAccount(context.Background(), account, &wrong); !errors.Is(err, ErrCredentialConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
}

func TestLegacyMigrationIsIdempotentAndBlocksUnreviewedMacros(t *testing.T) {
	r := openTestRepository(t)
	legacy := configuration.NewDataStorage()
	legacy.TicketData.Tickets = []configuration.TicketEntry{{ProjectID: 1, ScreenID: 2, SkuID: 3, Start: time.Now().Unix(), Expire: time.Now().Add(time.Hour).Unix(), Buyers: []response.TicketBuyer{{BuyerType: response.ForceRealName, ID: 7, Name: "A"}}}}
	if err := r.MigrateLegacy(context.Background(), legacy); err != nil {
		t.Fatal(err)
	}
	if err := r.MigrateLegacy(context.Background(), legacy); err != nil {
		t.Fatal(err)
	}
	macros, err := r.ListMacroTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(macros) != 1 || macros[0].Dispatchable() || !macros[0].NeedsReview {
		t.Fatalf("unexpected migration: %#v", macros)
	}
	accounts, err := r.ListAccounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 0 {
		t.Fatalf("migration created an unusable placeholder account: %#v", accounts)
	}
}

func TestSuccessTransactionOccupiesAllBuyerDays(t *testing.T) {
	r := openTestRepository(t)
	ctx := context.Background()
	if err := r.PutTaskGroup(ctx, domain.TaskGroup{ID: "g"}); err != nil {
		t.Fatal(err)
	}
	m := domain.MacroTask{ID: "m", TaskGroupID: "g", EventDay: "2026-07-01", EventDayConfirmed: true, ProjectID: 1, ScreenID: 2, SKUID: 3}
	if err := r.PutMacroTask(ctx, m); err != nil {
		t.Fatal(err)
	}
	i, _ := domain.NewIntent("i", m, domain.PhasePunctual, []domain.Buyer{{LogicalID: "a"}, {LogicalID: "b"}}, time.Now())
	if err := r.PutIntent(ctx, i); err != nil {
		t.Fatal(err)
	}
	if err := r.MarkIntentSucceeded(ctx, i, domain.ExecutionResult{AttemptID: "x", Success: true}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := r.db.QueryRow(`SELECT count(*) FROM buyer_day_occupancy`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}
