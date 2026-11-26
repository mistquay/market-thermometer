package store

import (
	"context"
	"database/sql"

	ds "market-thermometer/internal/datasource"
)

// 加密货币 K 线：我们自己的数据，数据源（币安、OKX）可替换。时间为开盘时间（UTC 毫秒）。
const cryptoSchema = `
CREATE TABLE IF NOT EXISTS crypto_bar (
  symbol TEXT NOT NULL, interval TEXT NOT NULL, t INT NOT NULL,
  open REAL NOT NULL, high REAL NOT NULL, low REAL NOT NULL, close REAL NOT NULL,
  volume REAL NOT NULL, quote REAL NOT NULL, source TEXT NOT NULL,
  PRIMARY KEY (symbol, interval, t)
) WITHOUT ROWID;
`

func (s *Store) migrateCrypto() error {
	_, err := s.db.Exec(cryptoSchema)
	return err
}

// SaveKlines 写入 K 线（同一时间已有时覆盖）。
func (s *Store) SaveKlines(ctx context.Context, symbol, interval, source string, ks []ds.Kline) error {
	if len(ks) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	st, err := tx.PrepareContext(ctx, `INSERT INTO crypto_bar(symbol,interval,t,open,high,low,close,volume,quote,source) VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(symbol,interval,t) DO UPDATE SET open=excluded.open, high=excluded.high, low=excluded.low, close=excluded.close,
		volume=excluded.volume, quote=excluded.quote, source=excluded.source`)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, k := range ks {
		if _, err := st.ExecContext(ctx, symbol, interval, k.T, k.Open, k.High, k.Low, k.Close, k.Volume, k.Quote, source); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Klines 读取 [from, to) 的 K 线（UTC 毫秒，0 为不限），按时间升序。
func (s *Store) Klines(ctx context.Context, symbol, interval string, from, to int64) ([]ds.Kline, error) {
	if to == 0 {
		to = 1 << 62
	}
	rows, err := s.db.QueryContext(ctx, `SELECT t, open, high, low, close, volume, quote FROM crypto_bar
		WHERE symbol=? AND interval=? AND t>=? AND t<? ORDER BY t`, symbol, interval, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ds.Kline{}
	for rows.Next() {
		var k ds.Kline
		if err := rows.Scan(&k.T, &k.Open, &k.High, &k.Low, &k.Close, &k.Volume, &k.Quote); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// KlinesBefore 读取开盘时间 < to（0 为不限）的最后 limit 根 K 线，按时间升序；供页面向前翻页。
func (s *Store) KlinesBefore(ctx context.Context, symbol, interval string, to int64, limit int) ([]ds.Kline, error) {
	if to == 0 {
		to = 1 << 62
	}
	rows, err := s.db.QueryContext(ctx, `SELECT t, open, high, low, close, volume, quote FROM crypto_bar
		WHERE symbol=? AND interval=? AND t<? ORDER BY t DESC LIMIT ?`, symbol, interval, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ds.Kline{}
	for rows.Next() {
		var k ds.Kline
		if err := rows.Scan(&k.T, &k.Open, &k.High, &k.Low, &k.Close, &k.Volume, &k.Quote); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}

// KlineLast 返回某周期最后一根 K 线的开盘时间；没有时返回 0。
func (s *Store) KlineLast(ctx context.Context, symbol, interval string) (int64, error) {
	var t sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT MAX(t) FROM crypto_bar WHERE symbol=? AND interval=?`, symbol, interval).Scan(&t)
	return t.Int64, err
}

// KlineCoverage 是一个品种一个周期的本地覆盖情况。
type KlineCoverage struct {
	Symbol   string `json:"symbol"`
	Interval string `json:"interval"`
	First    int64  `json:"first"`
	Last     int64  `json:"last"`
	Count    int    `json:"count"`
	Source   string `json:"source"`
}

// KlineCoverages 汇总本地加密 K 线。
func (s *Store) KlineCoverages(ctx context.Context) ([]KlineCoverage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT symbol, interval, MIN(t), MAX(t), COUNT(*), MAX(source) FROM crypto_bar GROUP BY symbol, interval ORDER BY symbol, interval`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []KlineCoverage{}
	for rows.Next() {
		var c KlineCoverage
		if err := rows.Scan(&c.Symbol, &c.Interval, &c.First, &c.Last, &c.Count, &c.Source); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
