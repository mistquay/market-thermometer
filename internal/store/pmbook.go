package store

import (
	"context"
	"strconv"
	"strings"

	ds "market-thermometer/internal/datasource"
)

// 预测市场盘口快照与同时刻的现货价（用于模拟盘撮合、短期涨跌盘定价）。
// 盘口只存二元市场第一个结果代币（Up / Yes）：另一个代币的盘口是它的镜像（卖价 = 1 − 对方买价，数量相同）。
const pmBookSchema = `
CREATE TABLE IF NOT EXISTS pm_book_token (id INTEGER PRIMARY KEY, token TEXT NOT NULL UNIQUE);
CREATE TABLE IF NOT EXISTS pm_book (k INT NOT NULL, t INT NOT NULL, bids TEXT NOT NULL, asks TEXT NOT NULL, PRIMARY KEY (k, t)) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS pm_book_t ON pm_book(t);
CREATE TABLE IF NOT EXISTS pm_spot (symbol TEXT NOT NULL, t INT NOT NULL, p REAL NOT NULL, PRIMARY KEY (symbol, t)) WITHOUT ROWID;
`

func (s *Store) migratePMBook() error {
	_, err := s.db.Exec(pmBookSchema)
	return err
}

// encodeLevels 把盘口档位编码成紧凑文本 “价:量,价:量”。
func encodeLevels(ls []ds.BookLevel) string {
	var b strings.Builder
	for i, l := range ls {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(l.Price, 'f', -1, 64))
		b.WriteByte(':')
		b.WriteString(strconv.FormatFloat(l.Size, 'f', -1, 64))
	}
	return b.String()
}

func decodeLevels(s string) []ds.BookLevel {
	out := []ds.BookLevel{}
	if s == "" {
		return out
	}
	for part := range strings.SplitSeq(s, ",") {
		p, q, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		price, err1 := strconv.ParseFloat(p, 64)
		size, err2 := strconv.ParseFloat(q, 64)
		if err1 == nil && err2 == nil {
			out = append(out, ds.BookLevel{Price: price, Size: size})
		}
	}
	return out
}

// PMBookKey 返回代币在盘口表里的短键（没有时新建）。
func (s *Store) PMBookKey(ctx context.Context, token string) (int64, error) {
	if _, err := s.db.ExecContext(ctx, `INSERT INTO pm_book_token(token) VALUES(?) ON CONFLICT(token) DO NOTHING`, token); err != nil {
		return 0, err
	}
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM pm_book_token WHERE token=?`, token).Scan(&id)
	return id, err
}

// PMBookRow 是一条待写入的盘口快照（T 为 Unix 毫秒）。
type PMBookRow struct {
	Key  int64
	T    int64
	Bids []ds.BookLevel // 价格从高到低
	Asks []ds.BookLevel // 价格从低到高
}

// PMSpot 是一个现货价点（T 为 Unix 毫秒）。
type PMSpot struct {
	Symbol string  `json:"symbol"`
	T      int64   `json:"t"`
	P      float64 `json:"p"`
}

// SavePMSnapshot 在一个事务里写入一批盘口快照与现货价（同一时刻重复时覆盖）。
func (s *Store) SavePMSnapshot(ctx context.Context, books []PMBookRow, spots []PMSpot) error {
	if len(books) == 0 && len(spots) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	bs, err := tx.PrepareContext(ctx, `INSERT INTO pm_book(k,t,bids,asks) VALUES(?,?,?,?) ON CONFLICT(k,t) DO UPDATE SET bids=excluded.bids, asks=excluded.asks`)
	if err != nil {
		return err
	}
	defer bs.Close()
	for _, b := range books {
		if _, err := bs.ExecContext(ctx, b.Key, b.T, encodeLevels(b.Bids), encodeLevels(b.Asks)); err != nil {
			return err
		}
	}
	ss, err := tx.PrepareContext(ctx, `INSERT INTO pm_spot(symbol,t,p) VALUES(?,?,?) ON CONFLICT(symbol,t) DO UPDATE SET p=excluded.p`)
	if err != nil {
		return err
	}
	defer ss.Close()
	for _, p := range spots {
		if _, err := ss.ExecContext(ctx, p.Symbol, p.T, p.P); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PMBookSnap 是读出的一条盘口快照。
type PMBookSnap struct {
	T    int64          `json:"t"`
	Bids []ds.BookLevel `json:"bids"`
	Asks []ds.BookLevel `json:"asks"`
}

// PMBooks 读取代币在 [from, to]（Unix 毫秒，to 为 0 不限）内的盘口快照，按时间升序，最多 limit 条。
func (s *Store) PMBooks(ctx context.Context, token string, from, to int64, limit int) ([]PMBookSnap, error) {
	if to == 0 {
		to = 1 << 62
	}
	if limit <= 0 {
		limit = 100_000
	}
	rows, err := s.db.QueryContext(ctx, `SELECT b.t, b.bids, b.asks FROM pm_book b JOIN pm_book_token k ON k.id=b.k
		WHERE k.token=? AND b.t>=? AND b.t<=? ORDER BY b.t LIMIT ?`, token, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PMBookSnap{}
	for rows.Next() {
		var x PMBookSnap
		var bids, asks string
		if err := rows.Scan(&x.T, &bids, &asks); err != nil {
			return nil, err
		}
		x.Bids, x.Asks = decodeLevels(bids), decodeLevels(asks)
		out = append(out, x)
	}
	return out, rows.Err()
}

// PMSpots 读取现货价（升序）。
func (s *Store) PMSpots(ctx context.Context, symbol string, from, to int64) ([]PMSpot, error) {
	if to == 0 {
		to = 1 << 62
	}
	rows, err := s.db.QueryContext(ctx, `SELECT symbol, t, p FROM pm_spot WHERE symbol=? AND t>=? AND t<=? ORDER BY t`, symbol, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PMSpot{}
	for rows.Next() {
		var x PMSpot
		if err := rows.Scan(&x.Symbol, &x.T, &x.P); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// PrunePMBooks 删除早于 before（Unix 毫秒）的盘口快照与现货价，返回删除的盘口条数。
func (s *Store) PrunePMBooks(ctx context.Context, before int64) (int64, error) {
	r, err := s.db.ExecContext(ctx, `DELETE FROM pm_book WHERE t<?`, before)
	if err != nil {
		return 0, err
	}
	n, _ := r.RowsAffected()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM pm_spot WHERE t<?`, before); err != nil {
		return n, err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM pm_book_token WHERE id NOT IN (SELECT DISTINCT k FROM pm_book)`)
	return n, err
}

// PMBookStats 是本地盘口数据概况。
type PMBookStats struct {
	Books  int   `json:"books"`
	Tokens int   `json:"tokens"`
	Spots  int   `json:"spots"`
	First  int64 `json:"first"` // Unix 毫秒
	Last   int64 `json:"last"`
}

func (s *Store) PMBookStatsAll(ctx context.Context) (PMBookStats, error) {
	var x PMBookStats
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(MIN(t),0), COALESCE(MAX(t),0) FROM pm_book`).Scan(&x.Books, &x.First, &x.Last)
	if err != nil {
		return x, err
	}
	if err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pm_book_token`).Scan(&x.Tokens); err != nil {
		return x, err
	}
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pm_spot`).Scan(&x.Spots)
	return x, err
}
