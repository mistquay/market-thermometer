package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	ds "market-thermometer/internal/datasource"
)

// ---- 数据质量：只读查询 ----

// Calendar 返回该市场生效参数下得分表中 ≥ from 的交易日（引擎的交易日历，以沪深300 收盘价为准）。
func (s *Store) Calendar(ctx context.Context, mkt, version, from string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT trade_date FROM score_daily WHERE market=? AND params_version=? AND trade_date>=? ORDER BY trade_date`,
		mkt, version, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// StockDates 返回某类证券在 ≥ from 的日线日期（code → 升序日期），一次扫描，用于缺口检测。
func (s *Store) StockDates(ctx context.Context, kind, from string) (map[string][]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT b.code, b.d FROM stock_bar b JOIN stock_list l ON l.code=b.code
		WHERE l.kind=? AND b.d>=? ORDER BY b.code, b.d`, kind, dateInt(from))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var code string
		var d int
		if err := rows.Scan(&code, &d); err != nil {
			return nil, err
		}
		out[code] = append(out[code], intDate(d))
	}
	return out, rows.Err()
}

// KindLastDate 返回某类证券本地日线的最新日期；没有时返回 ""。
func (s *Store) KindLastDate(ctx context.Context, kind string) (string, error) {
	var last sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT MAX(last_date) FROM stock_list WHERE kind=?`, kind).Scan(&last)
	if err != nil || !last.Valid {
		return "", err
	}
	return intDate(int(last.Int64)), nil
}

// SaveJSON / LoadJSON 把小对象（如最近一次交叉核对结果）以 JSON 存进 meta。
func (s *Store) SaveJSON(ctx context.Context, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.SetSetting(ctx, key, string(b))
}

// LoadJSON 读取 SaveJSON 保存的对象；不存在时返回 false。
func (s *Store) LoadJSON(ctx context.Context, key string, v any) (bool, error) {
	js, err := s.Setting(ctx, key)
	if err != nil || js == "" {
		return false, err
	}
	if err := json.Unmarshal([]byte(js), v); err != nil {
		return false, errors.Join(errors.New(key+" 损坏"), err)
	}
	return true, nil
}

// InsertMissingBars 只插入本地还没有的日线（已有的日期不覆盖），返回插入条数。用于补缺口。
func (s *Store) InsertMissingBars(ctx context.Context, code, source string, bars []ds.StockBar) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	st, err := tx.PrepareContext(ctx, `INSERT INTO stock_bar(code,d,close,mult,source) VALUES(?,?,?,?,?) ON CONFLICT(code,d) DO NOTHING`)
	if err != nil {
		return 0, err
	}
	defer st.Close()
	n := 0
	for _, b := range bars {
		r, err := st.ExecContext(ctx, code, dateInt(b.Date), b.Close, b.Mult, source)
		if err != nil {
			return 0, err
		}
		k, _ := r.RowsAffected()
		n += int(k)
	}
	return n, tx.Commit()
}
