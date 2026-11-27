package store

import (
	"context"
	"time"
)

// PMOrderMarker 是单个事件图表上叠加的模拟盘成交标记。
type PMOrderMarker struct {
	ID          int64   `json:"id"`
	AccountName string  `json:"accountName"`
	Time        string  `json:"time"`
	Outcome     int     `json:"outcome"`
	OutcomeName string  `json:"outcomeName"`
	Side        string  `json:"side"`
	Shares      float64 `json:"shares"`
	Gross       float64 `json:"gross"`
	AvgPrice    float64 `json:"avgPrice"`
}

// SimFillMarker 是单个 A 股证券图表上叠加的模拟盘成交标记。
type SimFillMarker struct {
	ID          int64   `json:"id"`
	AccountID   int64   `json:"accountId"`
	AccountName string  `json:"accountName"`
	Side        string  `json:"side"`
	FillDate    string  `json:"fillDate"`
	FillPrice   float64 `json:"fillPrice"`
	Qty         float64 `json:"qty"`
	Amount      float64 `json:"amount"`
	Note        string  `json:"note,omitempty"`
}

// PMSignalsBySlug 返回某个 Polymarket 事件的全部影子信号（按时间升序，最多 200 条）。
func (s *Store) PMSignalsBySlug(ctx context.Context, slug string) ([]PMSignal, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT s.id, s.t, s.strategy, s.slug, s.cond, s.outcome,
		s.p, s.ask, s.ask_size, s.fee_rate, s.edge, s.k, s.k_src, s.sd_bp, s.tau, s.spot, s.basis,
		COALESCE(m.winner, -1)
		FROM pm_signal s
		LEFT JOIN pm_market m ON m.cond = s.cond
		WHERE s.slug=? ORDER BY s.t ASC LIMIT 200`, slug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PMSignal{}
	for rows.Next() {
		var r PMSignal
		if err := rows.Scan(&r.ID, &r.T, &r.Strategy, &r.Slug, &r.Cond, &r.Outcome,
			&r.P, &r.Ask, &r.AskSize, &r.FeeRate, &r.Edge, &r.K, &r.KSrc, &r.SdBp, &r.Tau, &r.Spot, &r.Basis,
			&r.Winner); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PMOrdersBySlug 返回某个 Polymarket 事件跨模拟账户的已成交订单标记。
func (s *Store) PMOrdersBySlug(ctx context.Context, slug string) ([]PMOrderMarker, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT o.id, COALESCE(a.name, ''), o.t, o.outcome, o.outcome_name, o.side, o.shares, o.gross
		FROM pm_sim_order o LEFT JOIN pm_sim_account a ON a.id = o.account_id
		WHERE o.slug=? AND o.status IN ('filled', 'partial') AND o.shares > 0
		ORDER BY o.id ASC LIMIT 200`, slug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PMOrderMarker{}
	for rows.Next() {
		var m PMOrderMarker
		var tVal int64
		if err := rows.Scan(&m.ID, &m.AccountName, &tVal, &m.Outcome, &m.OutcomeName, &m.Side, &m.Shares, &m.Gross); err != nil {
			return nil, err
		}
		if tVal > 1e12 {
			m.Time = time.UnixMilli(tVal).UTC().Format(time.RFC3339)
		} else {
			m.Time = time.Unix(tVal, 0).UTC().Format(time.RFC3339)
		}
		if m.Shares > 0 {
			m.AvgPrice = m.Gross / m.Shares
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SimFillsByCode 返回某只 A 股 / 场内基金跨模拟账户的已成交记录（按成交日升序）。
func (s *Store) SimFillsByCode(ctx context.Context, code string) ([]SimFillMarker, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT o.id, o.account_id, COALESCE(a.name, ''), o.side, o.fill_date, o.fill_price, o.qty, o.amount, COALESCE(o.note, '')
		FROM sim_order o LEFT JOIN sim_account a ON a.id = o.account_id
		WHERE o.code=? AND o.status='filled' AND o.fill_date<>''
		ORDER BY o.fill_date ASC, o.id ASC LIMIT 200`, code)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SimFillMarker{}
	for rows.Next() {
		var m SimFillMarker
		if err := rows.Scan(&m.ID, &m.AccountID, &m.AccountName, &m.Side, &m.FillDate, &m.FillPrice, &m.Qty, &m.Amount, &m.Note); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
