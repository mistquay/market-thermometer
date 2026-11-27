package store

import (
	"context"
	"strings"
	"time"
)

// 聪明钱 / 鲸鱼钱包关注列表（用于 Polymarket 跟单与大额异动监控）。
const pmWhaleSchema = `
CREATE TABLE IF NOT EXISTS pm_whale (
  address TEXT PRIMARY KEY,
  label TEXT NOT NULL DEFAULT '',
  note TEXT NOT NULL DEFAULT '',
  added_at TEXT NOT NULL
) WITHOUT ROWID;
`

func (s *Store) migratePMWhale() error {
	_, err := s.db.Exec(pmWhaleSchema)
	return err
}

// PMWhale 是一个关注的 Polymarket 钱包。
type PMWhale struct {
	Address string `json:"address"`
	Label   string `json:"label"`
	Note    string `json:"note"`
	AddedAt string `json:"addedAt"`
}

func (s *Store) PMWhales(ctx context.Context) ([]PMWhale, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT address, label, note, added_at FROM pm_whale ORDER BY added_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PMWhale{}
	for rows.Next() {
		var w PMWhale
		if err := rows.Scan(&w.Address, &w.Label, &w.Note, &w.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) SavePMWhale(ctx context.Context, w PMWhale, now time.Time) error {
	addr := strings.ToLower(strings.TrimSpace(w.Address))
	_, err := s.db.ExecContext(ctx, `INSERT INTO pm_whale(address,label,note,added_at) VALUES(?,?,?,?)
		ON CONFLICT(address) DO UPDATE SET label=excluded.label, note=excluded.note`,
		addr, strings.TrimSpace(w.Label), strings.TrimSpace(w.Note), ts(now))
	return err
}

func (s *Store) DeletePMWhale(ctx context.Context, address string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM pm_whale WHERE address=?`, strings.ToLower(strings.TrimSpace(address)))
	return err
}
