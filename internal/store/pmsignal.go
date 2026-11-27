package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	ds "market-thermometer/internal/datasource"
)

// 策略信号（影子模式：只记录、不下单）与短期涨跌盘起始价查询。
const pmSignalSchema = `
CREATE TABLE IF NOT EXISTS pm_signal (
  id INTEGER PRIMARY KEY, t INT NOT NULL, strategy TEXT NOT NULL, slug TEXT NOT NULL, cond TEXT NOT NULL, outcome INT NOT NULL,
  p REAL NOT NULL, ask REAL NOT NULL, ask_size REAL NOT NULL, fee_rate REAL NOT NULL, edge REAL NOT NULL,
  k REAL NOT NULL, k_src TEXT NOT NULL, sd_bp REAL NOT NULL, tau INT NOT NULL, spot REAL NOT NULL, basis REAL NOT NULL
);
CREATE INDEX IF NOT EXISTS pm_signal_t ON pm_signal(t);
CREATE INDEX IF NOT EXISTS pm_signal_cond ON pm_signal(cond, t);
CREATE INDEX IF NOT EXISTS pm_market_start ON pm_market(start_t);
`

func (s *Store) migratePMSignal() error {
	_, err := s.db.Exec(pmSignalSchema)
	return err
}

// PMSignal 是一条策略信号。
type PMSignal struct {
	ID       int64   `json:"id"`
	T        int64   `json:"t"` // Unix 毫秒
	Strategy string  `json:"strategy"`
	Slug     string  `json:"slug"`
	Cond     string  `json:"conditionId"`
	Outcome  int     `json:"outcome"`
	P        float64 `json:"p"`   // 模型给该结果的概率
	Ask      float64 `json:"ask"` // 该结果的卖一
	AskSize  float64 `json:"askSize"`
	FeeRate  float64 `json:"feeRate"`
	Edge     float64 `json:"edge"` // p − ask − 每股手续费
	K        float64 `json:"k"`
	KSrc     string  `json:"kSrc"` // exact / spot / bar
	SdBp     float64 `json:"sdBp"`
	Tau      int64   `json:"tau"`
	Spot     float64 `json:"spot"`
	Basis    float64 `json:"basis"`
	Winner   int     `json:"winner"` // 读出时填：市场结果（−1 未出）
}

func (s *Store) AddPMSignals(ctx context.Context, sigs []PMSignal) error {
	if len(sigs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	st, err := tx.PrepareContext(ctx, `INSERT INTO pm_signal(t,strategy,slug,cond,outcome,p,ask,ask_size,fee_rate,edge,k,k_src,sd_bp,tau,spot,basis)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, x := range sigs {
		if _, err := st.ExecContext(ctx, x.T, x.Strategy, x.Slug, x.Cond, x.Outcome, x.P, x.Ask, x.AskSize, x.FeeRate, x.Edge, x.K, x.KSrc, x.SdBp, x.Tau, x.Spot, x.Basis); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PMSignals 读取 since（Unix 毫秒）之后的信号（新的在前，最多 limit 条），并带上市场结果。
func (s *Store) PMSignals(ctx context.Context, since int64, limit int) ([]PMSignal, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT g.id,g.t,g.strategy,g.slug,g.cond,g.outcome,g.p,g.ask,g.ask_size,g.fee_rate,g.edge,g.k,g.k_src,g.sd_bp,g.tau,g.spot,g.basis,
		COALESCE(m.winner,-1) FROM pm_signal g LEFT JOIN pm_market m ON m.condition_id=g.cond WHERE g.t>=? ORDER BY g.t DESC LIMIT ?`, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PMSignal{}
	for rows.Next() {
		var x PMSignal
		if err := rows.Scan(&x.ID, &x.T, &x.Strategy, &x.Slug, &x.Cond, &x.Outcome, &x.P, &x.Ask, &x.AskSize, &x.FeeRate, &x.Edge, &x.K, &x.KSrc,
			&x.SdBp, &x.Tau, &x.Spot, &x.Basis, &x.Winner); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// PMSignalEvalRow 是用于信号评估与历史盘口回放的完整信号记录（含市场元数据与结算结果）。
type PMSignalEvalRow struct {
	PMSignal
	Series  string
	Token0  string // 第一个结果代币（Up / Yes），用于查本地盘口
	EndT    int64  // Unix 秒
	FeeExp  float64
	MinSize float64
}

// PMSignalsForEval 按时间升序读取 [from, to]（Unix 毫秒，0 不限）内的信号及对应市场元数据（最多 limit 条）。
func (s *Store) PMSignalsForEval(ctx context.Context, from, to int64, limit int) ([]PMSignalEvalRow, error) {
	if to == 0 {
		to = 1 << 62
	}
	if limit <= 0 || limit > 50_000 {
		limit = 20_000
	}
	rows, err := s.db.QueryContext(ctx, `SELECT g.id,g.t,g.strategy,g.slug,g.cond,g.outcome,g.p,g.ask,g.ask_size,g.fee_rate,g.edge,g.k,g.k_src,g.sd_bp,g.tau,g.spot,g.basis,
		COALESCE(m.winner,-1), COALESCE(e.series,''), COALESCE(m.end_t,0), COALESCE(m.data,'')
		FROM pm_signal g
		LEFT JOIN pm_market m ON m.condition_id=g.cond
		LEFT JOIN pm_event e ON e.id=m.event_id
		WHERE g.t>=? AND g.t<=?
		ORDER BY g.t ASC, g.id ASC LIMIT ?`, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PMSignalEvalRow{}
	for rows.Next() {
		var x PMSignalEvalRow
		var mJSON string
		if err := rows.Scan(&x.ID, &x.T, &x.Strategy, &x.Slug, &x.Cond, &x.Outcome, &x.P, &x.Ask, &x.AskSize, &x.FeeRate, &x.Edge, &x.K, &x.KSrc,
			&x.SdBp, &x.Tau, &x.Spot, &x.Basis, &x.Winner, &x.Series, &x.EndT, &mJSON); err != nil {
			return nil, err
		}
		if mJSON != "" {
			var m ds.PMMarket
			if json.Unmarshal([]byte(mJSON), &m) == nil {
				if len(m.Tokens) > 0 {
					x.Token0 = m.Tokens[0]
				}
				x.FeeExp, x.MinSize = m.FeeExp, m.MinSize
				if x.FeeRate == 0 && m.FeeRate > 0 {
					x.FeeRate = m.FeeRate
				}
			}
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// PMStartPrice 返回某资产（btc / eth）在 start（Unix 秒）开始的涨跌盘起始价（只看新式 *-updown-* 事件，它们同一时刻的起始价相同）。
// 没有时返回 0。
func (s *Store) PMStartPrice(ctx context.Context, asset string, start int64) (float64, error) {
	var p float64
	err := s.db.QueryRowContext(ctx, `SELECT e.price_to_beat FROM pm_event e JOIN pm_market m ON m.event_id=e.id
		WHERE e.slug LIKE ? AND e.price_to_beat>0 AND m.start_t=? LIMIT 1`, asset+"-updown-%", start).Scan(&p)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return p, err
}

// PMStartPoint 是一个已知起始价的时刻。
type PMStartPoint struct {
	Start int64 // Unix 秒
	Price float64
}

// PMRecentStartPrices 返回某资产最近 limit 个不同时刻的起始价（新的在前），用于校准币安与 Chainlink 的基差。
func (s *Store) PMRecentStartPrices(ctx context.Context, asset string, limit int, before time.Time) ([]PMStartPoint, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT m.start_t, MIN(e.price_to_beat) FROM pm_event e JOIN pm_market m ON m.event_id=e.id
		WHERE e.slug LIKE ? AND e.price_to_beat>0 AND m.start_t<? GROUP BY m.start_t ORDER BY m.start_t DESC LIMIT ?`,
		asset+"-updown-%", before.Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PMStartPoint
	for rows.Next() {
		var x PMStartPoint
		if err := rows.Scan(&x.Start, &x.Price); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
