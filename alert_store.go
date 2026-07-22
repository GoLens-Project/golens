package golens

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// AlertStore is the persistence abstraction for alert rules and the firing log.
type AlertStore interface {
	// InitRules creates the required tables if they don't exist.
	InitRules(ctx context.Context) error
	// SeedFromConfig inserts config-file rules that don't already exist.
	SeedFromConfig(ctx context.Context, rules []AlertRule) error
	// LoadRules returns all persisted rules.
	LoadRules(ctx context.Context) ([]AlertRule, error)
	// SaveRule upserts a single rule.
	SaveRule(ctx context.Context, r AlertRule) error
	// DeleteRule removes a rule by ID.
	DeleteRule(ctx context.Context, id string) error
	// AppendLog records an alert firing event.
	AppendLog(ctx context.Context, e AlertLogEntry) error
	// ListLog returns the most recent log entries (newest first).
	ListLog(ctx context.Context, limit int) ([]AlertLogEntry, error)
	// Close releases resources.
	Close() error
}

// ---------------------------------------------------------------------------
// SQLite implementation
// ---------------------------------------------------------------------------

const alertSchema = `
CREATE TABLE IF NOT EXISTS alert_rules (
    id TEXT PRIMARY KEY,
    data TEXT NOT NULL,
    updated_at INTEGER DEFAULT (strftime('%s','now'))
);
CREATE TABLE IF NOT EXISTS alert_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    rule_id TEXT NOT NULL,
    rule_name TEXT NOT NULL,
    metric TEXT NOT NULL,
    value REAL NOT NULL,
    threshold REAL NOT NULL,
    condition TEXT NOT NULL,
    fired_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_alert_log_fired ON alert_log(fired_at);
CREATE INDEX IF NOT EXISTS idx_alert_log_rule ON alert_log(rule_id);
`

type sqliteAlertStore struct {
	db *sql.DB
}

func newSQLiteAlertStore(db *sql.DB) *sqliteAlertStore {
	return &sqliteAlertStore{db: db}
}

func (s *sqliteAlertStore) InitRules(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, alertSchema)
	return err
}

func (s *sqliteAlertStore) SeedFromConfig(ctx context.Context, rules []AlertRule) error {
	for _, rule := range rules {
		var exists int
		err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_rules WHERE id = ?`, rule.ID).Scan(&exists)
		if err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		if err := s.SaveRule(ctx, rule); err != nil {
			return err
		}
	}
	return nil
}

func (s *sqliteAlertStore) LoadRules(ctx context.Context) ([]AlertRule, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM alert_rules ORDER BY updated_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AlertRule
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var r AlertRule
		if err := json.Unmarshal([]byte(data), &r); err != nil {
			continue
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *sqliteAlertStore) SaveRule(ctx context.Context, r AlertRule) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO alert_rules (id, data, updated_at) VALUES (?, ?, ?)`,
		r.ID, string(data), time.Now().Unix(),
	)
	return err
}

func (s *sqliteAlertStore) DeleteRule(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM alert_rules WHERE id = ?`, id)
	return err
}

func (s *sqliteAlertStore) AppendLog(ctx context.Context, e AlertLogEntry) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO alert_log (rule_id, rule_name, metric, value, threshold, condition, fired_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.RuleID, e.RuleName, e.Metric, e.Value, e.Threshold, e.Condition, e.FiredAt.Unix(),
	)
	return err
}

func (s *sqliteAlertStore) ListLog(ctx context.Context, limit int) ([]AlertLogEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, rule_id, rule_name, metric, value, threshold, condition, fired_at
		 FROM alert_log ORDER BY fired_at DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AlertLogEntry
	for rows.Next() {
		var e AlertLogEntry
		var ts int64
		if err := rows.Scan(&e.ID, &e.RuleID, &e.RuleName, &e.Metric, &e.Value, &e.Threshold, &e.Condition, &ts); err != nil {
			return nil, err
		}
		e.FiredAt = time.Unix(ts, 0)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *sqliteAlertStore) Close() error { return nil } // DB closed by main storage

// ---------------------------------------------------------------------------
// Memory implementation (no persistence)
// ---------------------------------------------------------------------------

type memoryAlertStore struct {
	mu   sync.Mutex
	rules map[string]AlertRule
	log   []AlertLogEntry
	seq   int64
}

func newMemoryAlertStore() *memoryAlertStore {
	return &memoryAlertStore{rules: make(map[string]AlertRule)}
}

func (s *memoryAlertStore) InitRules(_ context.Context) error { return nil }

func (s *memoryAlertStore) SeedFromConfig(_ context.Context, rules []AlertRule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range rules {
		if _, exists := s.rules[r.ID]; !exists {
			s.rules[r.ID] = r
		}
	}
	return nil
}

func (s *memoryAlertStore) LoadRules(_ context.Context) ([]AlertRule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]AlertRule, 0, len(s.rules))
	for _, r := range s.rules {
		out = append(out, r)
	}
	return out, nil
}

func (s *memoryAlertStore) SaveRule(_ context.Context, r AlertRule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rules[r.ID] = r
	return nil
}

func (s *memoryAlertStore) DeleteRule(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rules, id)
	return nil
}

func (s *memoryAlertStore) AppendLog(_ context.Context, e AlertLogEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	e.ID = s.seq
	s.log = append([]AlertLogEntry{e}, s.log...)
	// Cap in-memory log at 1000 entries.
	if len(s.log) > 1000 {
		s.log = s.log[:1000]
	}
	return nil
}

func (s *memoryAlertStore) ListLog(_ context.Context, limit int) ([]AlertLogEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 || limit > len(s.log) {
		limit = len(s.log)
	}
	out := make([]AlertLogEntry, limit)
	copy(out, s.log[:limit])
	return out, nil
}

func (s *memoryAlertStore) Close() error { return nil }

// newAlertStore creates the appropriate AlertStore based on the storage config.
// If the main storage backend is "sqlite", the alert store shares the same DB.
func newAlertStore(cfg StorageConfig) (AlertStore, *sql.DB, error) {
	if cfg.Backend == "sqlite" {
		path := cfg.Path
		if path == "" {
			path = "golens.db"
		}
		db, err := sql.Open("sqlite", path)
		if err != nil {
			return nil, nil, fmt.Errorf("golens: open sqlite for alerts: %w", err)
		}
		store := newSQLiteAlertStore(db)
		return store, db, nil
	}
	return newMemoryAlertStore(), nil, nil
}

// newAlertStoreFromDB creates an alert store reusing an existing DB connection.
func newAlertStoreFromDB(db *sql.DB) AlertStore {
	return newSQLiteAlertStore(db)
}
