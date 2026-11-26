package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	ds "market-thermometer/internal/datasource"
)

// 预测市场（Polymarket）：事件、市场、价格历史、关注列表。我们自己的数据，数据源可替换。
const pmSchema = `
CREATE TABLE IF NOT EXISTS pm_event (
  id TEXT PRIMARY KEY, slug TEXT NOT NULL, title TEXT NOT NULL, series TEXT NOT NULL DEFAULT '', tags TEXT NOT NULL DEFAULT '[]',
  start_t INT NOT NULL DEFAULT 0, end_t INT NOT NULL DEFAULT 0, closed INT NOT NULL DEFAULT 0, neg_risk INT NOT NULL DEFAULT 0,
  volume REAL NOT NULL DEFAULT 0, volume24h REAL NOT NULL DEFAULT 0, liquidity REAL NOT NULL DEFAULT 0, updated_at TEXT NOT NULL
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS pm_event_series ON pm_event(series, end_t);
CREATE UNIQUE INDEX IF NOT EXISTS pm_event_slug ON pm_event(slug);
CREATE TABLE IF NOT EXISTS pm_market (
  condition_id TEXT PRIMARY KEY, event_id TEXT NOT NULL, data TEXT NOT NULL,
  start_t INT NOT NULL DEFAULT 0, end_t INT NOT NULL DEFAULT 0, closed INT NOT NULL DEFAULT 0, winner INT NOT NULL DEFAULT -1
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS pm_market_event ON pm_market(event_id);
CREATE TABLE IF NOT EXISTS pm_price (token TEXT NOT NULL, t INT NOT NULL, p REAL NOT NULL, PRIMARY KEY (token, t)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS pm_watch (slug TEXT PRIMARY KEY, added_at TEXT NOT NULL) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS pm_series_sync (series TEXT PRIMARY KEY, since INT NOT NULL, through INT NOT NULL) WITHOUT ROWID;
`

func (s *Store) migratePM() error {
	_, err := s.db.Exec(pmSchema)
	return err
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// SavePMEvents 写入事件及其市场（覆盖）。市场的完整字段以 JSON 存放。
func (s *Store) SavePMEvents(ctx context.Context, evs []ds.PMEvent, now time.Time) error {
	if len(evs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	es, err := tx.PrepareContext(ctx, `INSERT INTO pm_event(id,slug,title,series,tags,start_t,end_t,closed,neg_risk,volume,volume24h,liquidity,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET slug=excluded.slug, title=excluded.title, series=excluded.series,
		tags=excluded.tags, start_t=excluded.start_t, end_t=excluded.end_t, closed=excluded.closed, neg_risk=excluded.neg_risk,
		volume=excluded.volume, volume24h=excluded.volume24h, liquidity=excluded.liquidity, updated_at=excluded.updated_at`)
	if err != nil {
		return err
	}
	defer es.Close()
	ms, err := tx.PrepareContext(ctx, `INSERT INTO pm_market(condition_id,event_id,data,start_t,end_t,closed,winner) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(condition_id) DO UPDATE SET event_id=excluded.event_id, data=excluded.data, start_t=excluded.start_t,
		end_t=excluded.end_t, closed=excluded.closed, winner=excluded.winner`)
	if err != nil {
		return err
	}
	defer ms.Close()
	for _, e := range evs {
		tags, _ := json.Marshal(e.Tags)
		if _, err := es.ExecContext(ctx, e.ID, e.Slug, e.Title, e.Series, string(tags), e.Start.Unix(), e.End.Unix(), b2i(e.Closed), b2i(e.NegRisk),
			e.Volume, e.Volume24h, e.Liquidity, ts(now)); err != nil {
			return err
		}
		for _, m := range e.Markets {
			if m.ConditionID == "" {
				continue
			}
			js, _ := json.Marshal(m)
			if _, err := ms.ExecContext(ctx, m.ConditionID, e.ID, string(js), m.Start.Unix(), m.End.Unix(), b2i(m.Closed), m.Winner); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// PMEventQuery 是本地事件查询条件。
type PMEventQuery struct {
	Series string
	Slug   string
	Closed *bool
	From   int64 // 结束时间下限（Unix 秒），0 为不限
	To     int64 // 结束时间上限（不含），0 为不限
	Asc    bool  // 按结束时间升序（默认倒序）
	Limit  int
}

// PMEvents 读取本地事件（含市场），按结束时间倒序。
func (s *Store) PMEvents(ctx context.Context, q PMEventQuery) ([]ds.PMEvent, error) {
	where, args := "1=1", []any{}
	if q.Series != "" {
		where += " AND series=?"
		args = append(args, q.Series)
	}
	if q.Slug != "" {
		where += " AND slug=?"
		args = append(args, q.Slug)
	}
	if q.Closed != nil {
		where += " AND closed=?"
		args = append(args, b2i(*q.Closed))
	}
	if q.From > 0 {
		where += " AND end_t>=?"
		args = append(args, q.From)
	}
	if q.To > 0 {
		where += " AND end_t<?"
		args = append(args, q.To)
	}
	if q.Limit <= 0 {
		q.Limit = 1000
	}
	order := "DESC"
	if q.Asc {
		order = "ASC"
	}
	args = append(args, q.Limit)
	rows, err := s.db.QueryContext(ctx, `SELECT id,slug,title,series,tags,start_t,end_t,closed,neg_risk,volume,volume24h,liquidity
		FROM pm_event WHERE `+where+` ORDER BY end_t `+order+` LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	var out []ds.PMEvent
	for rows.Next() {
		var e ds.PMEvent
		var tags string
		var st, en int64
		if err := rows.Scan(&e.ID, &e.Slug, &e.Title, &e.Series, &tags, &st, &en, &e.Closed, &e.NegRisk, &e.Volume, &e.Volume24h, &e.Liquidity); err != nil {
			rows.Close()
			return nil, err
		}
		_ = json.Unmarshal([]byte(tags), &e.Tags)
		e.Start, e.End = time.Unix(st, 0).UTC(), time.Unix(en, 0).UTC()
		out = append(out, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Markets, err = s.pmMarkets(ctx, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) pmMarkets(ctx context.Context, eventID string) ([]ds.PMMarket, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT data FROM pm_market WHERE event_id=? ORDER BY start_t, condition_id`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ds.PMMarket{}
	for rows.Next() {
		var js string
		if err := rows.Scan(&js); err != nil {
			return nil, err
		}
		var m ds.PMMarket
		if err := json.Unmarshal([]byte(js), &m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// PMSeriesSync 返回系列已连续同步的已结算事件区间 [since, through]（结束时间，Unix 秒）；没有记录时都为 0。
// 只在一轮分页完整成功后才更新，中途失败不会留下“看似已同步”的空洞。
func (s *Store) PMSeriesSync(ctx context.Context, series string) (since, through int64, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT since, through FROM pm_series_sync WHERE series=?`, series).Scan(&since, &through)
	if err == sql.ErrNoRows {
		return 0, 0, nil
	}
	return since, through, err
}

// SetPMSeriesSync 记录系列已连续同步的区间。
func (s *Store) SetPMSeriesSync(ctx context.Context, series string, since, through int64) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO pm_series_sync(series,since,through) VALUES(?,?,?)
		ON CONFLICT(series) DO UPDATE SET since=excluded.since, through=excluded.through`, series, since, through)
	return err
}

// PMSeriesStats 是一个系列的本地覆盖情况。
type PMSeriesStats struct {
	Series   string `json:"series"`
	Events   int    `json:"events"`
	Closed   int    `json:"closed"`
	First    int64  `json:"first"` // 已结算事件的结束时间范围
	Last     int64  `json:"last"`
	WithHist int    `json:"withHistory"` // 有价格历史的已结算市场数
	Resolved int    `json:"resolved"`    // 已出结果的市场数
	FirstWin int    `json:"firstWins"`   // 其中第一个结果（Up / Yes）获胜的个数
}

// PMSeriesStatsAll 汇总本地各系列。
func (s *Store) PMSeriesStatsAll(ctx context.Context) ([]PMSeriesStats, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT e.series, COUNT(*), SUM(e.closed), COALESCE(MIN(CASE WHEN e.closed=1 THEN e.end_t END),0), COALESCE(MAX(CASE WHEN e.closed=1 THEN e.end_t END),0),
		SUM(EXISTS(SELECT 1 FROM pm_market m JOIN pm_price p ON p.token=json_extract(m.data,'$.tokens[0]') WHERE m.event_id=e.id))
		FROM pm_event e WHERE e.series<>'' GROUP BY e.series ORDER BY e.series`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PMSeriesStats{}
	for rows.Next() {
		var x PMSeriesStats
		if err := rows.Scan(&x.Series, &x.Events, &x.Closed, &x.First, &x.Last, &x.WithHist); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	wins, err := s.db.QueryContext(ctx, `SELECT e.series, SUM(m.winner>=0), SUM(m.winner=0) FROM pm_market m JOIN pm_event e ON e.id=m.event_id
		WHERE e.series<>'' AND m.closed=1 GROUP BY e.series`)
	if err != nil {
		return nil, err
	}
	defer wins.Close()
	idx := map[string]int{}
	for i, x := range out {
		idx[x.Series] = i
	}
	for wins.Next() {
		var series string
		var res, first int
		if err := wins.Scan(&series, &res, &first); err != nil {
			return nil, err
		}
		if i, ok := idx[series]; ok {
			out[i].Resolved, out[i].FirstWin = res, first
		}
	}
	return out, wins.Err()
}

// SavePMPrices 写入代币价格历史（覆盖）。
func (s *Store) SavePMPrices(ctx context.Context, token string, pts []ds.PricePoint) error {
	if len(pts) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	st, err := tx.PrepareContext(ctx, `INSERT INTO pm_price(token,t,p) VALUES(?,?,?) ON CONFLICT(token,t) DO UPDATE SET p=excluded.p`)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, p := range pts {
		if _, err := st.ExecContext(ctx, token, p.T, p.P); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PMPrices 读取代币价格历史（升序）。
func (s *Store) PMPrices(ctx context.Context, token string, from, to int64) ([]ds.PricePoint, error) {
	if to == 0 {
		to = 1 << 62
	}
	rows, err := s.db.QueryContext(ctx, `SELECT t, p FROM pm_price WHERE token=? AND t>=? AND t<=? ORDER BY t`, token, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ds.PricePoint{}
	for rows.Next() {
		var p ds.PricePoint
		if err := rows.Scan(&p.T, &p.P); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// HasPMPrices 判断代币是否已有价格历史。
func (s *Store) HasPMPrices(ctx context.Context, token string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT 1 FROM pm_price WHERE token=? LIMIT 1)`, token).Scan(&n)
	return n > 0, err
}

// PMUnresolved 返回系列中已关闭但还没有赢家的事件 slug（结束时间 >= from，按结束时间倒序，最多 limit 个）。
func (s *Store) PMUnresolved(ctx context.Context, series string, from int64, limit int) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT e.slug FROM pm_event e JOIN pm_market m ON m.event_id=e.id
		WHERE e.series=? AND e.end_t>=? AND m.closed=1 AND m.winner<0 ORDER BY e.end_t DESC LIMIT ?`, series, from, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var x string
		if err := rows.Scan(&x); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// ---- 关注列表 ----

func (s *Store) WatchPM(ctx context.Context, slug string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO pm_watch(slug,added_at) VALUES(?,?) ON CONFLICT(slug) DO NOTHING`, slug, ts(now))
	return err
}

func (s *Store) UnwatchPM(ctx context.Context, slug string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM pm_watch WHERE slug=?`, slug)
	return err
}

// PMWatched 返回关注的事件 slug（按加入时间）。
func (s *Store) PMWatched(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT slug FROM pm_watch ORDER BY added_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// PMEventBySlug 读取一个本地事件；没有时返回 sql.ErrNoRows。
func (s *Store) PMEventBySlug(ctx context.Context, slug string) (ds.PMEvent, error) {
	evs, err := s.PMEvents(ctx, PMEventQuery{Slug: slug, Limit: 1})
	if err != nil {
		return ds.PMEvent{}, err
	}
	if len(evs) == 0 {
		return ds.PMEvent{}, sql.ErrNoRows
	}
	return evs[0], nil
}
