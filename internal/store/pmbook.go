package store

import (
	"context"
	"database/sql"
	"log"
	"sort"
	"strconv"
	"strings"

	ds "market-thermometer/internal/datasource"
)

// 预测市场盘口快照与同时刻的现货价（用于模拟盘撮合、短期涨跌盘定价）。
// 存放在独立的高频快照库 tickDB，避免与主库（A 股日线/配置）争用写锁并减小主库备份体积。
// 盘口只存二元市场第一个结果代币（Up / Yes）：另一个代币的盘口是它的镜像（卖价 = 1 − 对方买价，数量相同）。
const pmBookSchema = `
CREATE TABLE IF NOT EXISTS pm_book_token (id INTEGER PRIMARY KEY, token TEXT NOT NULL UNIQUE);
CREATE TABLE IF NOT EXISTS pm_book (k INT NOT NULL, t INT NOT NULL, bids TEXT NOT NULL, asks TEXT NOT NULL, PRIMARY KEY (k, t)) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS pm_book_t ON pm_book(t);
CREATE TABLE IF NOT EXISTS pm_spot (symbol TEXT NOT NULL, t INT NOT NULL, p REAL NOT NULL, PRIMARY KEY (symbol, t)) WITHOUT ROWID;
`

func (s *Store) migratePMBook() error {
	_, err := s.tickDB.Exec(pmBookSchema)
	return err
}

// migrateLegacyPMBookToTick 一次性把旧版本写在主库里的 pm_book_token / pm_book / pm_spot 迁移到 tickDB。
func (s *Store) migrateLegacyPMBookToTick() error {
	ctx := context.Background()
	done, err := s.Meta(ctx, "pm.tick_migrated")
	if err != nil || done == "1" {
		return err
	}
	hasTable := func(name string) (bool, error) {
		var n int
		err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n)
		return n > 0, err
	}
	hasBook, err := hasTable("pm_book")
	if err != nil {
		return err
	}
	hasSpot, err := hasTable("pm_spot")
	if err != nil {
		return err
	}
	if !hasBook && !hasSpot {
		return s.SetMeta(ctx, "pm.tick_migrated", "1")
	}

	var migratedBooks, migratedSpots int
	if hasBook {
		if hasTok, _ := hasTable("pm_book_token"); hasTok {
			rows, err := s.db.QueryContext(ctx, `SELECT id, token FROM pm_book_token ORDER BY id`)
			if err != nil {
				return err
			}
			type tokRow struct {
				id  int64
				tok string
			}
			var toks []tokRow
			for rows.Next() {
				var r tokRow
				if err := rows.Scan(&r.id, &r.tok); err != nil {
					rows.Close()
					return err
				}
				toks = append(toks, r)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
			if len(toks) > 0 {
				tx, err := s.tickDB.BeginTx(ctx, nil)
				if err != nil {
					return err
				}
				st, err := tx.PrepareContext(ctx, `INSERT INTO pm_book_token(id, token) VALUES(?, ?) ON CONFLICT(token) DO NOTHING`)
				if err != nil {
					tx.Rollback()
					return err
				}
				for _, r := range toks {
					if _, err := st.ExecContext(ctx, r.id, r.tok); err != nil {
						st.Close()
						tx.Rollback()
						return err
					}
				}
				st.Close()
				if err := tx.Commit(); err != nil {
					return err
				}
			}
		}

		// 分批迁移 pm_book（每批 5000 条）
		rows, err := s.db.QueryContext(ctx, `SELECT k, t, bids, asks FROM pm_book`)
		if err != nil {
			return err
		}
		type bookRaw struct {
			k, t       int64
			bids, asks string
		}
		batch := make([]bookRaw, 0, 5000)
		flushBooks := func() error {
			if len(batch) == 0 {
				return nil
			}
			tx, err := s.tickDB.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			st, err := tx.PrepareContext(ctx, `INSERT INTO pm_book(k,t,bids,asks) VALUES(?,?,?,?) ON CONFLICT(k,t) DO NOTHING`)
			if err != nil {
				tx.Rollback()
				return err
			}
			for _, r := range batch {
				if _, err := st.ExecContext(ctx, r.k, r.t, r.bids, r.asks); err != nil {
					st.Close()
					tx.Rollback()
					return err
				}
			}
			st.Close()
			if err := tx.Commit(); err != nil {
				return err
			}
			migratedBooks += len(batch)
			batch = batch[:0]
			return nil
		}
		for rows.Next() {
			var r bookRaw
			if err := rows.Scan(&r.k, &r.t, &r.bids, &r.asks); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, r)
			if len(batch) >= 5000 {
				if err := flushBooks(); err != nil {
					rows.Close()
					return err
				}
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if err := flushBooks(); err != nil {
			return err
		}
	}

	if hasSpot {
		rows, err := s.db.QueryContext(ctx, `SELECT symbol, t, p FROM pm_spot`)
		if err != nil {
			return err
		}
		batch := make([]PMSpot, 0, 5000)
		flushSpots := func() error {
			if len(batch) == 0 {
				return nil
			}
			tx, err := s.tickDB.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			st, err := tx.PrepareContext(ctx, `INSERT INTO pm_spot(symbol,t,p) VALUES(?,?,?) ON CONFLICT(symbol,t) DO NOTHING`)
			if err != nil {
				tx.Rollback()
				return err
			}
			for _, r := range batch {
				if _, err := st.ExecContext(ctx, r.Symbol, r.T, r.P); err != nil {
					st.Close()
					tx.Rollback()
					return err
				}
			}
			st.Close()
			if err := tx.Commit(); err != nil {
				return err
			}
			migratedSpots += len(batch)
			batch = batch[:0]
			return nil
		}
		for rows.Next() {
			var r PMSpot
			if err := rows.Scan(&r.Symbol, &r.T, &r.P); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, r)
			if len(batch) >= 5000 {
				if err := flushSpots(); err != nil {
					rows.Close()
					return err
				}
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if err := flushSpots(); err != nil {
			return err
		}
	}

	if migratedBooks > 0 || migratedSpots > 0 {
		log.Printf("store: 已迁移高频快照到独立库 %s（盘口 %d 条、现货 %d 条）", s.tickPath, migratedBooks, migratedSpots)
	}
	// 清空主库中的旧表，释放页给主库复用（不做耗时的全库 VACUUM）
	for _, stmt := range []string{`DROP TABLE IF EXISTS pm_book`, `DROP TABLE IF EXISTS pm_book_token`, `DROP TABLE IF EXISTS pm_spot`} {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return s.SetMeta(ctx, "pm.tick_migrated", "1")
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
	if _, err := s.tickDB.ExecContext(ctx, `INSERT INTO pm_book_token(token) VALUES(?) ON CONFLICT(token) DO NOTHING`, token); err != nil {
		return 0, err
	}
	var id int64
	err := s.tickDB.QueryRowContext(ctx, `SELECT id FROM pm_book_token WHERE token=?`, token).Scan(&id)
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
	tx, err := s.tickDB.BeginTx(ctx, nil)
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
	rows, err := s.tickDB.QueryContext(ctx, `SELECT b.t, b.bids, b.asks FROM pm_book b JOIN pm_book_token k ON k.id=b.k
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

// PMBookAtOrAfter 返回代币在 [atMs, maxMs] 内的第一条盘口快照（用于策略回放按信号后下一张快照成交）。
func (s *Store) PMBookAtOrAfter(ctx context.Context, token string, atMs, maxMs int64) (PMBookSnap, bool, error) {
	if maxMs == 0 {
		maxMs = 1 << 62
	}
	var x PMBookSnap
	var bids, asks string
	err := s.tickDB.QueryRowContext(ctx, `SELECT b.t, b.bids, b.asks FROM pm_book b JOIN pm_book_token k ON k.id=b.k
		WHERE k.token=? AND b.t>=? AND b.t<=? ORDER BY b.t ASC LIMIT 1`, token, atMs, maxMs).Scan(&x.T, &bids, &asks)
	if err == sql.ErrNoRows {
		return PMBookSnap{}, false, nil
	}
	if err != nil {
		return PMBookSnap{}, false, err
	}
	x.Bids, x.Asks = decodeLevels(bids), decodeLevels(asks)
	return x, true, nil
}

// PMBookAtOrBefore 返回代币在 atMs 之前（含）的最近一条盘口快照（结合心跳判定盘口是否仍然有效）。
func (s *Store) PMBookAtOrBefore(ctx context.Context, token string, atMs int64) (PMBookSnap, bool, error) {
	var x PMBookSnap
	var bids, asks string
	err := s.tickDB.QueryRowContext(ctx, `SELECT b.t, b.bids, b.asks FROM pm_book b JOIN pm_book_token k ON k.id=b.k
		WHERE k.token=? AND b.t<=? ORDER BY b.t DESC LIMIT 1`, token, atMs).Scan(&x.T, &bids, &asks)
	if err == sql.ErrNoRows {
		return PMBookSnap{}, false, nil
	}
	if err != nil {
		return PMBookSnap{}, false, err
	}
	x.Bids, x.Asks = decodeLevels(bids), decodeLevels(asks)
	return x, true, nil
}

// PMSpotBefore 返回某品种在 atMs 之前（含）的最近一条现货价（用于回放时的采集心跳校验）。
func (s *Store) PMSpotBefore(ctx context.Context, symbol string, atMs int64) (PMSpot, bool, error) {
	var x PMSpot
	err := s.tickDB.QueryRowContext(ctx, `SELECT symbol, t, p FROM pm_spot WHERE symbol=? AND t<=? ORDER BY t DESC LIMIT 1`, symbol, atMs).
		Scan(&x.Symbol, &x.T, &x.P)
	if err == sql.ErrNoRows {
		return PMSpot{}, false, nil
	}
	return x, err == nil, err
}

// PMSpots 读取现货价（升序）。
func (s *Store) PMSpots(ctx context.Context, symbol string, from, to int64) ([]PMSpot, error) {
	if to == 0 {
		to = 1 << 62
	}
	rows, err := s.tickDB.QueryContext(ctx, `SELECT symbol, t, p FROM pm_spot WHERE symbol=? AND t>=? AND t<=? ORDER BY t`, symbol, from, to)
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
	r, err := s.tickDB.ExecContext(ctx, `DELETE FROM pm_book WHERE t<?`, before)
	if err != nil {
		return 0, err
	}
	n, _ := r.RowsAffected()
	if _, err := s.tickDB.ExecContext(ctx, `DELETE FROM pm_spot WHERE t<?`, before); err != nil {
		return n, err
	}
	_, err = s.tickDB.ExecContext(ctx, `DELETE FROM pm_book_token WHERE id NOT IN (SELECT DISTINCT k FROM pm_book)`)
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
	err := s.tickDB.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(MIN(t),0), COALESCE(MAX(t),0) FROM pm_book`).Scan(&x.Books, &x.First, &x.Last)
	if err != nil {
		return x, err
	}
	if err = s.tickDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM pm_book_token`).Scan(&x.Tokens); err != nil {
		return x, err
	}
	err = s.tickDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM pm_spot`).Scan(&x.Spots)
	return x, err
}

// PMTickHealth 描述近期盘口/现货采集的连续性与延迟分布（用于数据质量页监控写库争用和断流）。
type PMTickHealth struct {
	Symbol      string  `json:"symbol"`
	Samples     int     `json:"samples"`
	First       int64   `json:"first"`
	Last        int64   `json:"last"`
	P50Ms       int64   `json:"p50Ms"`
	P95Ms       int64   `json:"p95Ms"`
	P99Ms       int64   `json:"p99Ms"`
	MaxGapMs    int64   `json:"maxGapMs"`
	GapsOver10s int     `json:"gapsOver10s"`
	UptimeRatio float64 `json:"uptimeRatio"` // 间隔 ≤10s 的时长占总跨度的比例
}

// PMTickHealthStats 统计 sinceMs 以来某现货品种的采集间隔分布。
func (s *Store) PMTickHealthStats(ctx context.Context, symbol string, sinceMs int64) (PMTickHealth, error) {
	h := PMTickHealth{Symbol: symbol}
	rows, err := s.tickDB.QueryContext(ctx, `SELECT t FROM pm_spot WHERE symbol=? AND t>=? ORDER BY t`, symbol, sinceMs)
	if err != nil {
		return h, err
	}
	defer rows.Close()
	var ts []int64
	for rows.Next() {
		var t int64
		if err := rows.Scan(&t); err != nil {
			return h, err
		}
		ts = append(ts, t)
	}
	if err := rows.Err(); err != nil {
		return h, err
	}
	h.Samples = len(ts)
	if len(ts) == 0 {
		return h, nil
	}
	h.First, h.Last = ts[0], ts[len(ts)-1]
	if len(ts) < 2 {
		h.UptimeRatio = 1
		return h, nil
	}
	gaps := make([]int64, 0, len(ts)-1)
	var okSpan, totalSpan int64
	for i := 1; i < len(ts); i++ {
		g := ts[i] - ts[i-1]
		if g < 0 {
			continue
		}
		gaps = append(gaps, g)
		totalSpan += g
		if g > 10_000 {
			h.GapsOver10s++
		} else {
			okSpan += g
		}
	}
	if len(gaps) == 0 {
		return h, nil
	}
	sort.Slice(gaps, func(i, j int) bool { return gaps[i] < gaps[j] })
	pct := func(q float64) int64 {
		idx := int(float64(len(gaps)-1) * q)
		return gaps[idx]
	}
	h.P50Ms, h.P95Ms, h.P99Ms, h.MaxGapMs = pct(0.50), pct(0.95), pct(0.99), gaps[len(gaps)-1]
	if totalSpan > 0 {
		h.UptimeRatio = float64(okSpan) / float64(totalSpan)
	}
	return h, nil
}
