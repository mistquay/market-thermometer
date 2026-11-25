package store

import (
	"context"
	"strconv"
	"strings"
	"time"

	"market-thermometer/internal/breadth"
	ds "market-thermometer/internal/datasource"
)

// 个股日线是我们自己的数据：从任一个股数据源同步进来后长期保存，广度等派生指标只从这里计算，
// 不依赖数据源是否还在。日期存为整数 YYYYMMDD 以节省空间（约 2,000 万行）。
const breadthSchema = `
DROP TABLE IF EXISTS stock_close;
DROP TABLE IF EXISTS stock_meta;
CREATE TABLE IF NOT EXISTS stock_list (
  code TEXT PRIMARY KEY, kind TEXT NOT NULL DEFAULT 'stock', delisted INT NOT NULL DEFAULT 0,
  last_date INT, source TEXT, updated_at TEXT
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS stock_bar (
  code TEXT NOT NULL, d INT NOT NULL, close REAL NOT NULL, mult REAL NOT NULL, source TEXT NOT NULL,
  PRIMARY KEY (code, d)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS breadth_state (code TEXT PRIMARY KEY, state_json TEXT NOT NULL) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS breadth_count (
  trade_date TEXT PRIMARY KEY, above INT NOT NULL, total INT NOT NULL
) WITHOUT ROWID;
`

func (s *Store) migrateBreadth() error {
	if _, err := s.db.Exec(breadthSchema); err != nil {
		return err
	}
	// 早期版本的 stock_list 没有 kind 列
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('stock_list') WHERE name='kind'`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		_, err := s.db.Exec(`ALTER TABLE stock_list ADD COLUMN kind TEXT NOT NULL DEFAULT 'stock'`)
		return err
	}
	return nil
}

func dateInt(d string) int {
	n, _ := strconv.Atoi(strings.ReplaceAll(d, "-", ""))
	return n
}

func intDate(n int) string {
	s := strconv.Itoa(n)
	if len(s) != 8 {
		return s
	}
	return s[:4] + "-" + s[4:6] + "-" + s[6:]
}

// ---- 个股清单与日线 ----

// StockInfo 是本地个股清单的一项。LastDate 为本地已有日线的最后日期（空表示还没有数据）。
type StockInfo struct {
	ds.Stock
	LastDate string
}

// UpsertStockList 写入数据源给出的证券清单（只更新类别与退市标记，不动已有日线）。
func (s *Store) UpsertStockList(ctx context.Context, stocks []ds.Stock, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	st, err := tx.PrepareContext(ctx, `INSERT INTO stock_list(code,kind,delisted,updated_at) VALUES(?,?,?,?)
		ON CONFLICT(code) DO UPDATE SET kind=excluded.kind, delisted=excluded.delisted, updated_at=excluded.updated_at`)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, x := range stocks {
		kind := x.Kind
		if kind == "" {
			kind = ds.KindOf(x.Code)
		}
		if _, err := st.ExecContext(ctx, x.Code, kind, x.Delisted, ts(now)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// StockList 返回本地个股清单，按代码排序。
func (s *Store) StockList(ctx context.Context) ([]StockInfo, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT code, kind, delisted, COALESCE(last_date,0) FROM stock_list ORDER BY code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StockInfo
	for rows.Next() {
		var x StockInfo
		var last int
		if err := rows.Scan(&x.Code, &x.Kind, &x.Delisted, &last); err != nil {
			return nil, err
		}
		if last > 0 {
			x.LastDate = intDate(last)
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// SaveStockBars 在一个事务中写入多只股票的日线（已有日期覆盖），并更新各股 last_date 与来源。
func (s *Store) SaveStockBars(ctx context.Context, source string, bars map[string][]ds.StockBar) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ins, err := tx.PrepareContext(ctx, `INSERT INTO stock_bar(code,d,close,mult,source) VALUES(?,?,?,?,?)
		ON CONFLICT(code,d) DO UPDATE SET close=excluded.close, mult=excluded.mult, source=excluded.source`)
	if err != nil {
		return err
	}
	defer ins.Close()
	upd, err := tx.PrepareContext(ctx, `UPDATE stock_list SET last_date=MAX(COALESCE(last_date,0), ?), source=? WHERE code=?`)
	if err != nil {
		return err
	}
	defer upd.Close()
	for code, bs := range bars {
		last := 0
		for _, b := range bs {
			d := dateInt(b.Date)
			if _, err := ins.ExecContext(ctx, code, d, b.Close, b.Mult, source); err != nil {
				return err
			}
			last = max(last, d)
		}
		if last > 0 {
			if _, err := upd.ExecContext(ctx, last, source, code); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// StockBars 返回某只股票日期严格大于 after（空为不限）的本地日线，按日期升序。
func (s *Store) StockBars(ctx context.Context, code, after string) ([]ds.StockBar, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT d, close, mult FROM stock_bar WHERE code=? AND d>? ORDER BY d`, code, dateInt(after))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ds.StockBar
	for rows.Next() {
		var d int
		var b ds.StockBar
		if err := rows.Scan(&d, &b.Close, &b.Mult); err != nil {
			return nil, err
		}
		b.Date = intDate(d)
		out = append(out, b)
	}
	return out, rows.Err()
}

// StockStats 汇总本地证券数据：股票（含退市）与场内基金数量、有日线的数量、最早 / 最晚日期。
type StockStats struct {
	Stocks   int    `json:"stocks"`
	WithBars int    `json:"withBars"`
	Delisted int    `json:"delisted"`
	Funds    int    `json:"funds"` // 有日线的场内基金数
	First    string `json:"first"`
	Last     string `json:"last"`
}

func (s *Store) StockStats(ctx context.Context) (StockStats, error) {
	var st StockStats
	var last int
	err := s.db.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(kind='stock'),0), COALESCE(SUM(kind='stock' AND last_date IS NOT NULL),0),
		COALESCE(SUM(kind='stock' AND delisted=1 AND last_date IS NOT NULL),0),
		COALESCE(SUM(kind='fund' AND last_date IS NOT NULL),0), COALESCE(MAX(last_date),0) FROM stock_list`).
		Scan(&st.Stocks, &st.WithBars, &st.Delisted, &st.Funds, &last)
	if err != nil {
		return st, err
	}
	if last > 0 {
		st.Last = intDate(last)
		var first int
		if err := s.db.QueryRowContext(ctx, `SELECT MIN(d) FROM stock_bar`).Scan(&first); err == nil {
			st.First = intDate(first)
		}
	}
	return st, nil
}

// ---- 广度增量状态与计数 ----

// BreadthStates 返回全部个股增量状态（code → JSON）。
func (s *Store) BreadthStates(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT code, state_json FROM breadth_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var c, js string
		if err := rows.Scan(&c, &js); err != nil {
			return nil, err
		}
		out[c] = js
	}
	return out, rows.Err()
}

// SaveBreadth 在同一事务中写入一批个股状态，并把计数**累加**进 breadth_count。
// 状态与计数同时提交，中途中断不会重复计数。
func (s *Store) SaveBreadth(ctx context.Context, states map[string]string, c breadth.Counts) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	st, err := tx.PrepareContext(ctx, `INSERT INTO breadth_state(code,state_json) VALUES(?,?)
		ON CONFLICT(code) DO UPDATE SET state_json=excluded.state_json`)
	if err != nil {
		return err
	}
	defer st.Close()
	for code, js := range states {
		if _, err := st.ExecContext(ctx, code, js); err != nil {
			return err
		}
	}
	ct, err := tx.PrepareContext(ctx, `INSERT INTO breadth_count(trade_date,above,total) VALUES(?,?,?)
		ON CONFLICT(trade_date) DO UPDATE SET above=above+excluded.above, total=total+excluded.total`)
	if err != nil {
		return err
	}
	defer ct.Close()
	for d, v := range c {
		if _, err := ct.ExecContext(ctx, d, v.Above, v.Total); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// BreadthCounts 返回全部日计数及升序日期。
func (s *Store) BreadthCounts(ctx context.Context) ([]string, breadth.Counts, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT trade_date, above, total FROM breadth_count ORDER BY trade_date`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var dates []string
	c := breadth.Counts{}
	for rows.Next() {
		var d string
		var v breadth.Count
		if err := rows.Scan(&d, &v.Above, &v.Total); err != nil {
			return nil, nil, err
		}
		dates = append(dates, d)
		c[d] = v
	}
	return dates, c, rows.Err()
}

// ResetBreadth 清空广度状态与计数（不动个股日线），用于从本地日线全量重算。
func (s *Store) ResetBreadth(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM breadth_state; DELETE FROM breadth_count;`)
	return err
}
