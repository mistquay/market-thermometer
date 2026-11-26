package store

import (
	"context"
	"database/sql"

	ds "market-thermometer/internal/datasource"
)

// 个股日线扩展字段（OHLC、成交、估值、市值）：与 stock_bar 分表，广度与组合的热路径不受影响；
// 只有提供这些字段的数据源（stockdb）会写入，腾讯、导入、补缺口的日线没有扩展字段。
const extSchema = `
CREATE TABLE IF NOT EXISTS stock_daily_ext (
  code TEXT NOT NULL, d INT NOT NULL,
  open REAL, high REAL, low REAL, volume REAL, amount REAL,
  pe_ttm REAL, pb REAL, total_mv REAL, float_mv REAL,
  PRIMARY KEY (code, d)
) WITHOUT ROWID;
`

func (s *Store) migrateExt() error {
	_, err := s.db.Exec(extSchema)
	return err
}

func np(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

// SaveStockExt 写入扩展字段（同一天已有时覆盖）。
func (s *Store) SaveStockExt(ctx context.Context, ext map[string][]ds.StockExt) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	st, err := tx.PrepareContext(ctx, `INSERT INTO stock_daily_ext(code,d,open,high,low,volume,amount,pe_ttm,pb,total_mv,float_mv)
		VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(code,d) DO UPDATE SET open=excluded.open, high=excluded.high, low=excluded.low,
		volume=excluded.volume, amount=excluded.amount, pe_ttm=excluded.pe_ttm, pb=excluded.pb, total_mv=excluded.total_mv, float_mv=excluded.float_mv`)
	if err != nil {
		return err
	}
	defer st.Close()
	for code, rows := range ext {
		for _, e := range rows {
			if _, err := st.ExecContext(ctx, code, dateInt(e.Date), e.Open, e.High, e.Low, e.Volume, e.Amount,
				np(e.PETTM), np(e.PB), np(e.TotalMV), np(e.FloatMV)); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// StockExt 读取某只证券的扩展字段（升序）。
func (s *Store) StockExt(ctx context.Context, code string) ([]ds.StockExt, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT d, COALESCE(open,0), COALESCE(high,0), COALESCE(low,0), COALESCE(volume,0), COALESCE(amount,0),
		pe_ttm, pb, total_mv, float_mv FROM stock_daily_ext WHERE code=? ORDER BY d`, code)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ds.StockExt
	for rows.Next() {
		var d int
		var e ds.StockExt
		var pe, pb, tmv, fmv sql.NullFloat64
		if err := rows.Scan(&d, &e.Open, &e.High, &e.Low, &e.Volume, &e.Amount, &pe, &pb, &tmv, &fmv); err != nil {
			return nil, err
		}
		e.Date = intDate(d)
		e.PETTM, e.PB, e.TotalMV, e.FloatMV = nptr(pe), nptr(pb), nptr(tmv), nptr(fmv)
		out = append(out, e)
	}
	return out, rows.Err()
}

func nptr(x sql.NullFloat64) *float64 {
	if !x.Valid {
		return nil
	}
	v := x.Float64
	return &v
}

// ExtStats 返回扩展表的证券数、行数与日期范围。
func (s *Store) ExtStats(ctx context.Context) (codes, rows int, first, last string, err error) {
	var a, b sql.NullInt64
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT code), COUNT(*), MIN(d), MAX(d) FROM stock_daily_ext`).Scan(&codes, &rows, &a, &b)
	if a.Valid {
		first, last = intDate(int(a.Int64)), intDate(int(b.Int64))
	}
	return
}

// ExtCoveredFrom 返回扩展字段已从 from 起完整覆盖的证券：扩展表最早日期不晚于本地日线在 from 之后的第一天
// （回填续跑时跳过它们；晚于 from 上市的股票同样适用）。
func (s *Store) ExtCoveredFrom(ctx context.Context, from string) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT e.code FROM (SELECT code, MIN(d) m FROM stock_daily_ext GROUP BY code) e
		WHERE e.m <= (SELECT MIN(b.d) FROM stock_bar b WHERE b.code=e.code AND b.d >= ?)`, dateInt(from))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out[c] = true
	}
	return out, rows.Err()
}
