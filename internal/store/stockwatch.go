package store

import (
	"context"
	"time"
)

// StockWatch 是本地自选股 / 自选场内基金记录。
type StockWatch struct {
	Kind    string `json:"kind"` // stock | fund
	Code    string `json:"code"`
	AddedAt string `json:"addedAt"`
}

func (s *Store) migrateStockWatch() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS stock_watch (
		kind     TEXT NOT NULL,
		code     TEXT NOT NULL,
		added_at TEXT NOT NULL,
		PRIMARY KEY (kind, code)
	)`)
	return err
}

func (s *Store) StockWatched(ctx context.Context) ([]StockWatch, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT kind, code, added_at FROM stock_watch ORDER BY added_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StockWatch{}
	for rows.Next() {
		var w StockWatch
		if err := rows.Scan(&w.Kind, &w.Code, &w.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) WatchStock(ctx context.Context, kind, code string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO stock_watch(kind, code, added_at) VALUES(?,?,?)
		ON CONFLICT(kind, code) DO UPDATE SET added_at=excluded.added_at`, kind, code, ts(now))
	return err
}

func (s *Store) UnwatchStock(ctx context.Context, kind, code string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM stock_watch WHERE kind=? AND code=?`, kind, code)
	return err
}
