package golens

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	// Pure-Go SQLite driver (no CGo).
	_ "modernc.org/sqlite"
)

// sqliteStorage persists summarized roll-ups to a local SQLite database.
type sqliteStorage struct {
	db *sql.DB
}

const sqliteSchema = `
CREATE TABLE IF NOT EXISTS metrics (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    type TEXT NOT NULL,
    labels TEXT,
    count INTEGER NOT NULL DEFAULT 0,
    sum REAL NOT NULL DEFAULT 0,
    min REAL NOT NULL DEFAULT 0,
    max REAL NOT NULL DEFAULT 0,
    window_start INTEGER NOT NULL,
    window_end INTEGER NOT NULL,
    created_at INTEGER DEFAULT (strftime('%s','now'))
);
CREATE INDEX IF NOT EXISTS idx_metrics_name ON metrics(name);
CREATE INDEX IF NOT EXISTS idx_metrics_window ON metrics(window_start, window_end);
`

func newSQLiteStorage(path string) (*sqliteStorage, error) {
	if path == "" {
		path = "golens.db"
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("golens: open sqlite: %w", err)
	}
	if _, err := db.Exec(sqliteSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("golens: init sqlite schema: %w", err)
	}
	return &sqliteStorage{db: db}, nil
}

func (s *sqliteStorage) Store(ctx context.Context, m AggregatedMetric) error {
	labels, _ := json.Marshal(m.Labels)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO metrics (name, type, labels, count, sum, min, max, window_start, window_end)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.Name, m.Type, string(labels), m.Count, m.Sum, m.Min, m.Max,
		m.WindowStart.Unix(), m.WindowEnd.Unix(),
	)
	return err
}

func (s *sqliteStorage) Query(ctx context.Context, q Query) ([]AggregatedMetric, error) {
	query := `SELECT name, type, labels, count, sum, min, max, window_start, window_end FROM metrics WHERE 1=1`
	args := []interface{}{}
	if q.Name != "" {
		query += ` AND name = ?`
		args = append(args, q.Name)
	}
	if !q.From.IsZero() {
		query += ` AND window_end >= ?`
		args = append(args, q.From.Unix())
	}
	if !q.To.IsZero() {
		query += ` AND window_start <= ?`
		args = append(args, q.To.Unix())
	}
	query += ` ORDER BY window_start ASC`
	if q.Limit > 0 {
		query += fmt.Sprintf(` LIMIT %d`, q.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]AggregatedMetric, 0)
	for rows.Next() {
		var m AggregatedMetric
		var labelsJSON string
		var ws, we int64
		if err := rows.Scan(&m.Name, &m.Type, &labelsJSON, &m.Count, &m.Sum, &m.Min, &m.Max, &ws, &we); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(labelsJSON), &m.Labels)
		m.WindowStart = time.Unix(ws, 0)
		m.WindowEnd = time.Unix(we, 0)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *sqliteStorage) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}
