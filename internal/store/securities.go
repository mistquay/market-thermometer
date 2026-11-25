package store

import (
	"context"
	"strings"
	"time"
)

// 证券名称与搜索。类别：stock（沪深 A 股）、fund（场内基金）、ofund（场外基金，来自 fund_info）。
// 同一代码可能既是场内又是场外（如 LOF），所以引用证券时总是带上类别。
const securitySchema = `
CREATE TABLE IF NOT EXISTS security_name (code TEXT PRIMARY KEY, name TEXT NOT NULL, updated_at TEXT NOT NULL) WITHOUT ROWID;
`

func (s *Store) migrateSecurities() error {
	_, err := s.db.Exec(securitySchema)
	return err
}

// 证券类别。
const (
	SecStock = "stock"
	SecFund  = "fund"
	SecOFund = "ofund"
)

// SaveNames 写入证券名称（覆盖）。
func (s *Store) SaveNames(ctx context.Context, names map[string]string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	st, err := tx.PrepareContext(ctx, `INSERT INTO security_name(code,name,updated_at) VALUES(?,?,?)
		ON CONFLICT(code) DO UPDATE SET name=excluded.name, updated_at=excluded.updated_at`)
	if err != nil {
		return err
	}
	defer st.Close()
	for c, n := range names {
		if _, err := st.ExecContext(ctx, c, n, ts(now)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// NameCount 返回已有名称的证券数。
func (s *Store) NameCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM security_name`).Scan(&n)
	return n, err
}

// Security 是搜索结果中的一只证券。
type Security struct {
	Code     string `json:"code"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Type     string `json:"type,omitempty"` // 场外基金类型
	HasData  bool   `json:"hasData"`        // 本地是否有日线 / 净值
	Delisted bool   `json:"delisted,omitempty"`
}

// SearchSecurities 按代码前缀或名称关键字搜索 A 股、场内基金与场外基金，最多 limit 条。
func (s *Store) SearchSecurities(ctx context.Context, q string, limit int) ([]Security, error) {
	q = strings.TrimSpace(q)
	out := []Security{}
	if q == "" {
		return out, nil
	}
	like := "%" + strings.NewReplacer("%", "", "_", "").Replace(q) + "%"
	rows, err := s.db.QueryContext(ctx, `
		SELECT l.code, l.kind, COALESCE(n.name,''), '', l.last_date IS NOT NULL, l.delisted
		  FROM stock_list l LEFT JOIN security_name n ON n.code=l.code
		 WHERE l.code LIKE ? OR n.name LIKE ?
		UNION ALL
		SELECT f.code, 'ofund', f.name, COALESCE(f.type,''), EXISTS(SELECT 1 FROM fund_nav v WHERE v.code=f.code), 0
		  FROM fund_info f
		 WHERE f.code LIKE ? OR f.name LIKE ?
		 ORDER BY 1, 2 LIMIT ?`, q+"%", like, q+"%", like, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var x Security
		if err := rows.Scan(&x.Code, &x.Kind, &x.Name, &x.Type, &x.HasData, &x.Delisted); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// SecurityNames 返回这些代码的名称（场内取 security_name，场外取 fund_info）。键为 kind:code。
func (s *Store) SecurityNames(ctx context.Context, items []Security) (map[string]string, error) {
	out := map[string]string{}
	for _, it := range items {
		var name string
		q := `SELECT name FROM security_name WHERE code=?`
		if it.Kind == SecOFund {
			q = `SELECT name FROM fund_info WHERE code=?`
		}
		if err := s.db.QueryRowContext(ctx, q, it.Code).Scan(&name); err == nil {
			out[it.Kind+":"+it.Code] = name
		}
	}
	return out, nil
}
