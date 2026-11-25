package store

import (
	"context"
	"strings"
	"time"

	ds "market-thermometer/internal/datasource"
)

// 场外基金：清单（全部，用于搜索）、自选（只同步这些的净值）、净值（我们自己的数据）。
const fundSchema = `
CREATE TABLE IF NOT EXISTS fund_info (code TEXT PRIMARY KEY, name TEXT NOT NULL, type TEXT, updated_at TEXT) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS fund_watch (code TEXT PRIMARY KEY, added_at TEXT NOT NULL) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS fund_nav (
  code TEXT NOT NULL, d INT NOT NULL, nav REAL NOT NULL, acc_nav REAL NOT NULL, source TEXT NOT NULL,
  PRIMARY KEY (code, d)
) WITHOUT ROWID;
`

func (s *Store) migrateFunds() error {
	_, err := s.db.Exec(fundSchema)
	return err
}

// ReplaceFundInfo 写入基金清单（覆盖名称与类型）。
func (s *Store) ReplaceFundInfo(ctx context.Context, funds []ds.FundInfo, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	st, err := tx.PrepareContext(ctx, `INSERT INTO fund_info(code,name,type,updated_at) VALUES(?,?,?,?)
		ON CONFLICT(code) DO UPDATE SET name=excluded.name, type=excluded.type, updated_at=excluded.updated_at`)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, f := range funds {
		if _, err := st.ExecContext(ctx, f.Code, f.Name, f.Type, ts(now)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// FundInfoCount 返回本地基金清单数量。
func (s *Store) FundInfoCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM fund_info`).Scan(&n)
	return n, err
}

// SearchFunds 按代码前缀或名称关键字搜索，最多 limit 条。
func (s *Store) SearchFunds(ctx context.Context, q string, limit int) ([]ds.FundInfo, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return []ds.FundInfo{}, nil
	}
	like := "%" + strings.NewReplacer("%", "", "_", "").Replace(q) + "%"
	rows, err := s.db.QueryContext(ctx, `SELECT code, name, COALESCE(type,'') FROM fund_info
		WHERE code LIKE ? OR name LIKE ? ORDER BY (code LIKE ?) DESC, code LIMIT ?`, q+"%", like, q+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ds.FundInfo{}
	for rows.Next() {
		var f ds.FundInfo
		if err := rows.Scan(&f.Code, &f.Name, &f.Type); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// WatchFund / UnwatchFund 维护自选基金。
func (s *Store) WatchFund(ctx context.Context, code string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO fund_watch(code,added_at) VALUES(?,?) ON CONFLICT(code) DO NOTHING`, code, ts(now))
	return err
}

func (s *Store) UnwatchFund(ctx context.Context, code string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM fund_watch WHERE code=?`, code)
	return err
}

// WatchedFund 是自选基金及其本地净值概况。
type WatchedFund struct {
	ds.FundInfo
	First   string  `json:"first"`
	Last    string  `json:"last"`
	Count   int     `json:"count"`
	NAV     float64 `json:"nav"`
	AccNAV  float64 `json:"accNav"`
	AddedAt string  `json:"addedAt"`
}

func (s *Store) WatchedFunds(ctx context.Context) ([]WatchedFund, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT w.code, COALESCE(i.name,''), COALESCE(i.type,''), w.added_at,
		COALESCE(MIN(n.d),0), COALESCE(MAX(n.d),0), COUNT(n.d),
		COALESCE((SELECT nav FROM fund_nav WHERE code=w.code ORDER BY d DESC LIMIT 1),0),
		COALESCE((SELECT acc_nav FROM fund_nav WHERE code=w.code ORDER BY d DESC LIMIT 1),0)
		FROM fund_watch w LEFT JOIN fund_info i ON i.code=w.code LEFT JOIN fund_nav n ON n.code=w.code
		GROUP BY w.code ORDER BY w.added_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WatchedFund{}
	for rows.Next() {
		var f WatchedFund
		var first, last int
		if err := rows.Scan(&f.Code, &f.Name, &f.Type, &f.AddedAt, &first, &last, &f.Count, &f.NAV, &f.AccNAV); err != nil {
			return nil, err
		}
		if first > 0 {
			f.First, f.Last = intDate(first), intDate(last)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// SaveFundNAV 写入净值（已有日期覆盖）。
func (s *Store) SaveFundNAV(ctx context.Context, code, source string, navs []ds.FundNAV) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	st, err := tx.PrepareContext(ctx, `INSERT INTO fund_nav(code,d,nav,acc_nav,source) VALUES(?,?,?,?,?)
		ON CONFLICT(code,d) DO UPDATE SET nav=excluded.nav, acc_nav=excluded.acc_nav, source=excluded.source`)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, n := range navs {
		if _, err := st.ExecContext(ctx, code, dateInt(n.Date), n.NAV, n.AccNAV, source); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// FundNAV 返回某基金全部本地净值，按日期升序。
func (s *Store) FundNAV(ctx context.Context, code string) ([]ds.FundNAV, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT d, nav, acc_nav FROM fund_nav WHERE code=? ORDER BY d`, code)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ds.FundNAV{}
	for rows.Next() {
		var d int
		var n ds.FundNAV
		if err := rows.Scan(&d, &n.NAV, &n.AccNAV); err != nil {
			return nil, err
		}
		n.Date = intDate(d)
		out = append(out, n)
	}
	return out, rows.Err()
}
