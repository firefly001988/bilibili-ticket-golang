package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"bilibili-ticket-golang/cluster/domain"
	"bilibili-ticket-golang/store/configuration"
)

const LegacyMigrationName = "legacy-messagepack-v1"

// MigrateLegacy imports only account and membership-ticket data. The legacy
// MessagePack remains authoritative for BWS, notifications, locale and settings.
func (r *Repository) MigrateLegacy(ctx context.Context, legacy *configuration.DataStorage) error {
	if legacy == nil {
		return nil
	}
	done, err := r.HasMigration(ctx, LegacyMigrationName)
	if err != nil || done {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	taskGroup := domain.TaskGroup{ID: "migrated-default", Name: "Migrated tickets", CreatedAt: now}
	groupPayload, _ := json.Marshal(taskGroup)
	if _, err = tx.ExecContext(ctx, `INSERT INTO task_groups(id,payload) VALUES(?,?)`, taskGroup.ID, groupPayload); err != nil {
		return err
	}

	cookies := make(map[string]string, len(legacy.Cookies))
	cookieJar := make([]domain.HTTPCookie, 0, len(legacy.Cookies))
	for _, cookie := range legacy.Cookies {
		cookies[cookie.Name] = cookie.Value
		cookieJar = append(cookieJar, domain.HTTPCookie{Name: cookie.Name, Value: cookie.Value, Domain: cookie.Domain, Path: cookie.Path, Secure: cookie.Secure, HTTPOnly: cookie.HttpOnly, Expires: cookie.Expires})
	}
	account := domain.Account{ID: "migrated-account", Name: "Migrated account", Role: domain.RolePrimary, Enabled: len(cookies) > 0, Credentials: domain.Credentials{Cookies: cookies, CookieJar: cookieJar, RefreshToken: legacy.RefreshToken, Version: 1}}
	accountPayload, _ := json.Marshal(account)
	if _, err = tx.ExecContext(ctx, `INSERT INTO accounts(id,role,enabled,credential_version,payload) VALUES(?,?,?,?,?)`, account.ID, account.Role, account.Enabled, account.Credentials.Version, accountPayload); err != nil {
		return err
	}

	entries := legacy.TicketData.GetTicketsNoMutate()
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Hash() < entries[j].Hash() })
	macros := make(map[string]domain.MacroTask)
	for _, entry := range entries {
		key := fmt.Sprintf("%d/%d/%d/%d/%d", entry.ProjectID, entry.ScreenID, entry.SkuID, entry.Start, entry.Expire)
		macroID := stableID("macro", key)
		if _, ok := macros[macroID]; !ok {
			macro := domain.MacroTask{ID: macroID, TaskGroupID: taskGroup.ID, ProjectID: entry.ProjectID, ScreenID: entry.ScreenID, SKUID: entry.SkuID, NeedsReview: true, SmartMerge: false, OrderCapacity: 4, CapacitySource: domain.CapacityDefault, Priority: 0, DesiredReplicas: 1, HardConcurrency: 1, StartAt: time.Unix(entry.Start, 0), Deadline: time.Unix(entry.Expire, 0)}
			macros[macroID] = macro
			payload, _ := json.Marshal(macro)
			if _, err = tx.ExecContext(ctx, `INSERT INTO macro_tasks(id,task_group_id,priority,needs_review,payload) VALUES(?,?,?,?,?)`, macro.ID, macro.TaskGroupID, macro.Priority, true, payload); err != nil {
				return err
			}
		}
		buyers := make([]domain.Buyer, len(entry.Buyers))
		for i, old := range entry.Buyers {
			logicalID := stableID("buyer", fmt.Sprintf("%d/%s/%s", old.ID, old.Name, old.Tel))
			buyers[i] = domain.Buyer{LogicalID: logicalID, BuyerID: old.ID, Name: old.Name, Tel: old.Tel, Type: int(old.BuyerType)}
		}
		purchase := domain.PurchaseGroup{ID: stableID("purchase", entry.Hash()), MacroTaskID: macroID, Buyers: buyers, AllowSplit: false, CreatedAt: now}
		payload, _ := json.Marshal(purchase)
		if _, err = tx.ExecContext(ctx, `INSERT INTO purchase_groups(id,macro_task_id,payload) VALUES(?,?,?)`, purchase.ID, purchase.MacroTaskID, payload); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO migration_versions(name,version,applied_at) VALUES(?,?,?)`, LegacyMigrationName, 1, now.Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

func stableID(prefix, value string) string {
	sum := sha256.Sum256([]byte(value))
	return prefix + "-" + hex.EncodeToString(sum[:8])
}
