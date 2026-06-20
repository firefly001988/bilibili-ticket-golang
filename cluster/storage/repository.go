package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"bilibili-ticket-golang/cluster/domain"
	_ "modernc.org/sqlite"
)

const schemaVersion = 1

type Repository struct {
	db   *sql.DB
	path string
}

func Open(path string) (*Repository, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	_ = file.Close()
	_ = os.Chmod(path, 0600)
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	r := &Repository{db: db, path: path}
	if err := r.init(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return r, nil
}

func (r *Repository) Close() error { return r.db.Close() }

func (r *Repository) init(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS schema_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS task_groups (id TEXT PRIMARY KEY, payload BLOB NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS macro_tasks (id TEXT PRIMARY KEY, task_group_id TEXT NOT NULL, priority INTEGER NOT NULL, needs_review INTEGER NOT NULL, payload BLOB NOT NULL, FOREIGN KEY(task_group_id) REFERENCES task_groups(id))`,
		`CREATE TABLE IF NOT EXISTS purchase_groups (id TEXT PRIMARY KEY, macro_task_id TEXT NOT NULL, payload BLOB NOT NULL, FOREIGN KEY(macro_task_id) REFERENCES macro_tasks(id))`,
		`CREATE TABLE IF NOT EXISTS intents (id TEXT PRIMARY KEY, macro_task_id TEXT NOT NULL, shape_hash TEXT NOT NULL, succeeded INTEGER NOT NULL DEFAULT 0, payload BLOB NOT NULL, FOREIGN KEY(macro_task_id) REFERENCES macro_tasks(id))`,
		`CREATE TABLE IF NOT EXISTS attempts (id TEXT PRIMARY KEY, intent_id TEXT NOT NULL, account_id TEXT, worker_id TEXT, state TEXT NOT NULL, payload BLOB NOT NULL, FOREIGN KEY(intent_id) REFERENCES intents(id))`,
		`CREATE TABLE IF NOT EXISTS accounts (id TEXT PRIMARY KEY, role TEXT NOT NULL, enabled INTEGER NOT NULL, credential_version INTEGER NOT NULL, payload BLOB NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS logical_buyers (id TEXT PRIMARY KEY, payload BLOB NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS account_buyer_mappings (account_id TEXT NOT NULL, logical_buyer_id TEXT NOT NULL, buyer_id INTEGER NOT NULL, payload BLOB NOT NULL, PRIMARY KEY(account_id, logical_buyer_id), FOREIGN KEY(account_id) REFERENCES accounts(id))`,
		`CREATE TABLE IF NOT EXISTS workers (id TEXT PRIMARY KEY, role TEXT NOT NULL, enabled INTEGER NOT NULL, payload BLOB NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS worker_keys (worker_id TEXT PRIMARY KEY, control_key BLOB NOT NULL, FOREIGN KEY(worker_id) REFERENCES workers(id))`,
		`CREATE TABLE IF NOT EXISTS leases (id TEXT PRIMARY KEY, attempt_id TEXT NOT NULL, account_id TEXT NOT NULL UNIQUE, worker_id TEXT NOT NULL UNIQUE, expires_at INTEGER NOT NULL, payload BLOB NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS buyer_day_occupancy (buyer_id TEXT NOT NULL, event_day TEXT NOT NULL, intent_id TEXT NOT NULL, PRIMARY KEY(buyer_id, event_day))`,
		`CREATE TABLE IF NOT EXISTS execution_results (attempt_id TEXT PRIMARY KEY, success INTEGER NOT NULL, payload BLOB NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS migration_versions (name TEXT PRIMARY KEY, version INTEGER NOT NULL, applied_at INTEGER NOT NULL)`,
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO schema_meta(key,value) VALUES('schema_version',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, fmt.Sprint(schemaVersion))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func marshal(value any) ([]byte, error) { return json.Marshal(value) }

func (r *Repository) PutTaskGroup(ctx context.Context, value domain.TaskGroup) error {
	b, err := marshal(value)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO task_groups(id,payload) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload`, value.ID, b)
	return err
}
func (r *Repository) PutMacroTask(ctx context.Context, value domain.MacroTask) error {
	b, err := marshal(value)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO macro_tasks(id,task_group_id,priority,needs_review,payload) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET priority=excluded.priority,needs_review=excluded.needs_review,payload=excluded.payload`, value.ID, value.TaskGroupID, value.Priority, value.NeedsReview, b)
	return err
}
func (r *Repository) PutPurchaseGroup(ctx context.Context, value domain.PurchaseGroup) error {
	b, err := marshal(value)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO purchase_groups(id,macro_task_id,payload) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload`, value.ID, value.MacroTaskID, b)
	return err
}
func (r *Repository) PutIntent(ctx context.Context, value domain.LogicalOrderIntent) error {
	b, err := marshal(value)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO intents(id,macro_task_id,shape_hash,succeeded,payload) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET succeeded=excluded.succeeded,payload=excluded.payload`, value.ID, value.MacroTaskID, value.ShapeHash, value.Succeeded, b)
	return err
}
func (r *Repository) PutAttempt(ctx context.Context, value domain.ExecutionAttempt) error {
	b, err := marshal(value)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO attempts(id,intent_id,account_id,worker_id,state,payload) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET state=excluded.state,payload=excluded.payload`, value.ID, value.IntentID, value.AccountID, value.WorkerID, value.State, b)
	return err
}
func (r *Repository) PutWorker(ctx context.Context, value domain.WorkerNode) error {
	b, err := marshal(value)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO workers(id,role,enabled,payload) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET role=excluded.role,enabled=excluded.enabled,payload=excluded.payload`, value.ID, value.Role, value.Enabled, b)
	return err
}

func (r *Repository) PutWorkerKey(ctx context.Context, workerID, key string) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO worker_keys(worker_id,control_key) VALUES(?,?) ON CONFLICT(worker_id) DO UPDATE SET control_key=excluded.control_key`, workerID, []byte(key))
	return err
}

func (r *Repository) WorkerKey(ctx context.Context, workerID string) (string, error) {
	var key []byte
	err := r.db.QueryRowContext(ctx, `SELECT control_key FROM worker_keys WHERE worker_id=?`, workerID).Scan(&key)
	return string(key), err
}

func (r *Repository) PutAccount(ctx context.Context, value domain.Account, expectedVersion *int64) error {
	b, err := marshal(value)
	if err != nil {
		return err
	}
	if expectedVersion == nil {
		_, err = r.db.ExecContext(ctx, `INSERT INTO accounts(id,role,enabled,credential_version,payload) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET role=excluded.role,enabled=excluded.enabled,credential_version=excluded.credential_version,payload=excluded.payload`, value.ID, value.Role, value.Enabled, value.Credentials.Version, b)
		return err
	}
	result, err := r.db.ExecContext(ctx, `UPDATE accounts SET role=?,enabled=?,credential_version=?,payload=? WHERE id=? AND credential_version=?`, value.Role, value.Enabled, value.Credentials.Version, b, value.ID, *expectedVersion)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrCredentialConflict
	}
	return nil
}

var ErrCredentialConflict = errors.New("credential version conflict")

func (r *Repository) Account(ctx context.Context, id string) (domain.Account, error) {
	var b []byte
	if err := r.db.QueryRowContext(ctx, `SELECT payload FROM accounts WHERE id=?`, id).Scan(&b); err != nil {
		return domain.Account{}, err
	}
	var value domain.Account
	err := json.Unmarshal(b, &value)
	return value, err
}

func (r *Repository) PutLogicalBuyer(ctx context.Context, value domain.Buyer) error {
	b, err := marshal(value)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO logical_buyers(id,payload) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload`, value.LogicalID, b)
	return err
}

func (r *Repository) PutBuyerMapping(ctx context.Context, value domain.AccountBuyerMapping) error {
	b, err := marshal(value)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO account_buyer_mappings(account_id,logical_buyer_id,buyer_id,payload) VALUES(?,?,?,?) ON CONFLICT(account_id,logical_buyer_id) DO UPDATE SET buyer_id=excluded.buyer_id,payload=excluded.payload`, value.AccountID, value.LogicalBuyerID, value.BuyerID, b)
	return err
}

func (r *Repository) BuyerMapping(ctx context.Context, accountID, logicalBuyerID string) (domain.AccountBuyerMapping, error) {
	var b []byte
	if err := r.db.QueryRowContext(ctx, `SELECT payload FROM account_buyer_mappings WHERE account_id=? AND logical_buyer_id=?`, accountID, logicalBuyerID).Scan(&b); err != nil {
		return domain.AccountBuyerMapping{}, err
	}
	var value domain.AccountBuyerMapping
	err := json.Unmarshal(b, &value)
	return value, err
}

func (r *Repository) MarkIntentSucceeded(ctx context.Context, intent domain.LogicalOrderIntent, result domain.ExecutionResult) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	intent.Succeeded = true
	intent.Terminal = true
	payload, _ := json.Marshal(intent)
	if _, err = tx.ExecContext(ctx, `UPDATE intents SET succeeded=1,payload=? WHERE id=? AND succeeded=0`, payload, intent.ID); err != nil {
		return err
	}
	for _, key := range intent.BuyerDays {
		if _, err = tx.ExecContext(ctx, `INSERT INTO buyer_day_occupancy(buyer_id,event_day,intent_id) VALUES(?,?,?)`, key.BuyerID, key.EventDay, intent.ID); err != nil {
			return err
		}
	}
	resultPayload, _ := json.Marshal(result)
	if _, err = tx.ExecContext(ctx, `INSERT INTO execution_results(attempt_id,success,payload) VALUES(?,?,?) ON CONFLICT(attempt_id) DO UPDATE SET success=excluded.success,payload=excluded.payload`, result.AttemptID, result.Success, resultPayload); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Repository) ListMacroTasks(ctx context.Context) ([]domain.MacroTask, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT payload FROM macro_tasks ORDER BY priority DESC,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.MacroTask
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		var value domain.MacroTask
		if err := json.Unmarshal(b, &value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (r *Repository) ListAccounts(ctx context.Context) ([]domain.Account, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT payload FROM accounts ORDER BY role,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.Account
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		var value domain.Account
		if err := json.Unmarshal(b, &value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (r *Repository) ListTaskGroups(ctx context.Context) ([]domain.TaskGroup, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT payload FROM task_groups ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.TaskGroup
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		var value domain.TaskGroup
		if err := json.Unmarshal(b, &value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (r *Repository) ListWorkers(ctx context.Context) ([]domain.WorkerNode, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT payload FROM workers ORDER BY role,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.WorkerNode
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		var value domain.WorkerNode
		if err := json.Unmarshal(b, &value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (r *Repository) ListPurchaseGroups(ctx context.Context, macroID string) ([]domain.PurchaseGroup, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT payload FROM purchase_groups WHERE macro_task_id=? ORDER BY id`, macroID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.PurchaseGroup
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		var value domain.PurchaseGroup
		if err := json.Unmarshal(b, &value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (r *Repository) ListAttempts(ctx context.Context) ([]domain.ExecutionAttempt, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT payload FROM attempts ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.ExecutionAttempt
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		var value domain.ExecutionAttempt
		if err := json.Unmarshal(b, &value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (r *Repository) ListIntents(ctx context.Context) ([]domain.LogicalOrderIntent, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT payload FROM intents ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.LogicalOrderIntent
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		var value domain.LogicalOrderIntent
		if err := json.Unmarshal(b, &value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (r *Repository) HasMigration(ctx context.Context, name string) (bool, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM migration_versions WHERE name=?`, name).Scan(&n)
	return n > 0, err
}
