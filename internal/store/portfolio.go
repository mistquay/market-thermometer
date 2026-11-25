package store

import (
	"context"
	"database/sql"
	"time"
)

// 组合：只记录目标权重与起始日（P3 投研分析用）；订单、成交、资金属于 P4 模拟交易，不在这里。
const portfolioSchema = `
CREATE TABLE IF NOT EXISTS portfolio (
  id INTEGER PRIMARY KEY, name TEXT NOT NULL, note TEXT NOT NULL DEFAULT '', start_date TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS portfolio_item (
  portfolio_id INTEGER NOT NULL, kind TEXT NOT NULL, code TEXT NOT NULL, weight REAL NOT NULL,
  PRIMARY KEY (portfolio_id, kind, code)
) WITHOUT ROWID;
`

func (s *Store) migratePortfolio() error {
	_, err := s.db.Exec(portfolioSchema)
	return err
}

// PortfolioItem 是组合中的一个持仓（Kind 为 stock / fund / ofund）。
type PortfolioItem struct {
	Kind   string  `json:"kind"`
	Code   string  `json:"code"`
	Weight float64 `json:"weight"`
	Name   string  `json:"name,omitempty"`
}

// Portfolio 是一个组合。
type Portfolio struct {
	ID        int64           `json:"id"`
	Name      string          `json:"name"`
	Note      string          `json:"note"`
	Start     string          `json:"start"` // 起始日，空为所有持仓都有数据的最早日
	Items     []PortfolioItem `json:"items"`
	UpdatedAt string          `json:"updatedAt"`
}

// Portfolios 返回全部组合（含持仓），按 ID 升序。
func (s *Store) Portfolios(ctx context.Context) ([]Portfolio, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, note, start_date, updated_at FROM portfolio ORDER BY id`)
	if err != nil {
		return nil, err
	}
	out := []Portfolio{}
	for rows.Next() {
		var p Portfolio
		if err := rows.Scan(&p.ID, &p.Name, &p.Note, &p.Start, &p.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Items, err = s.portfolioItems(ctx, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Portfolio 读取一个组合；不存在时返回 sql.ErrNoRows。
func (s *Store) Portfolio(ctx context.Context, id int64) (Portfolio, error) {
	p := Portfolio{ID: id}
	err := s.db.QueryRowContext(ctx, `SELECT name, note, start_date, updated_at FROM portfolio WHERE id=?`, id).
		Scan(&p.Name, &p.Note, &p.Start, &p.UpdatedAt)
	if err != nil {
		return p, err
	}
	p.Items, err = s.portfolioItems(ctx, id)
	return p, err
}

func (s *Store) portfolioItems(ctx context.Context, id int64) ([]PortfolioItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT i.kind, i.code, i.weight,
		COALESCE(CASE WHEN i.kind='ofund' THEN (SELECT name FROM fund_info f WHERE f.code=i.code)
		              ELSE (SELECT name FROM security_name n WHERE n.code=i.code) END, '')
		FROM portfolio_item i WHERE i.portfolio_id=? ORDER BY i.weight DESC, i.code`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PortfolioItem{}
	for rows.Next() {
		var it PortfolioItem
		if err := rows.Scan(&it.Kind, &it.Code, &it.Weight, &it.Name); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// SavePortfolio 新增（ID 为 0）或更新组合，持仓整体替换。返回 ID。
func (s *Store) SavePortfolio(ctx context.Context, p Portfolio, now time.Time) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	id := p.ID
	if id == 0 {
		r, err := tx.ExecContext(ctx, `INSERT INTO portfolio(name,note,start_date,created_at,updated_at) VALUES(?,?,?,?,?)`,
			p.Name, p.Note, p.Start, ts(now), ts(now))
		if err != nil {
			return 0, err
		}
		if id, err = r.LastInsertId(); err != nil {
			return 0, err
		}
	} else {
		r, err := tx.ExecContext(ctx, `UPDATE portfolio SET name=?, note=?, start_date=?, updated_at=? WHERE id=?`,
			p.Name, p.Note, p.Start, ts(now), id)
		if err != nil {
			return 0, err
		}
		if n, _ := r.RowsAffected(); n == 0 {
			return 0, sql.ErrNoRows
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM portfolio_item WHERE portfolio_id=?`, id); err != nil {
			return 0, err
		}
	}
	for _, it := range p.Items {
		if _, err := tx.ExecContext(ctx, `INSERT INTO portfolio_item(portfolio_id,kind,code,weight) VALUES(?,?,?,?)`,
			id, it.Kind, it.Code, it.Weight); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit()
}

// DeletePortfolio 删除组合及其持仓。
func (s *Store) DeletePortfolio(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM portfolio_item WHERE portfolio_id=?`, id); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM portfolio WHERE id=?`, id)
	return err
}
