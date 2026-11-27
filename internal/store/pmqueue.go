package store

import (
	"context"
	"database/sql"
	"time"
)

// Polymarket 策略执行提案队列（用于人工逐笔确认 HITL）：
// 策略触发信号时生成待审批提案（带过期时间），用户在 Web 点击批准后经核心风控执行。
const pmProposalSchema = `
CREATE TABLE IF NOT EXISTS pm_proposal (
  id INTEGER PRIMARY KEY,
  created_at INT NOT NULL,
  expires_at INT NOT NULL,
  strategy TEXT NOT NULL,
  slug TEXT NOT NULL,
  cond TEXT NOT NULL,
  outcome INT NOT NULL,
  p REAL NOT NULL,
  ask REAL NOT NULL,
  edge REAL NOT NULL,
  suggested_usd REAL NOT NULL,
  k_src TEXT NOT NULL,
  tau INT NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending',
  account_id INT NOT NULL DEFAULT 0,
  order_id INT NOT NULL DEFAULT 0,
  reason TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS pm_proposal_status ON pm_proposal(status, created_at);
`

func (s *Store) migratePMProposal() error {
	_, err := s.db.Exec(pmProposalSchema)
	return err
}

// PMProposal 是一条待人工确认的下单提案。
type PMProposal struct {
	ID           int64   `json:"id"`
	CreatedAt    int64   `json:"createdAt"` // Unix 毫秒
	ExpiresAt    int64   `json:"expiresAt"` // Unix 毫秒
	Strategy     string  `json:"strategy"`
	Slug         string  `json:"slug"`
	Cond         string  `json:"conditionId"`
	Outcome      int     `json:"outcome"`
	P            float64 `json:"p"`
	Ask          float64 `json:"ask"`
	Edge         float64 `json:"edge"`
	SuggestedUSD float64 `json:"suggestedUsd"`
	KSrc         string  `json:"kSrc"`
	Tau          int64   `json:"tau"`
	Status       string  `json:"status"` // pending | approved | rejected | expired
	AccountID    int64   `json:"accountId"`
	OrderID      int64   `json:"orderId,omitempty"`
	Reason       string  `json:"reason,omitempty"`
}

// AddPMProposal 新增一条待审批提案（若同一市场同一结果已有未过期 pending 提案则跳过并返回 0）。
func (s *Store) AddPMProposal(ctx context.Context, p PMProposal, now time.Time) (int64, error) {
	nowMs := now.UnixMilli()
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pm_proposal WHERE cond=? AND outcome=? AND status='pending' AND expires_at>?`,
		p.Cond, p.Outcome, nowMs).Scan(&n); err != nil {
		return 0, err
	}
	if n > 0 {
		return 0, nil
	}
	r, err := s.db.ExecContext(ctx, `INSERT INTO pm_proposal(created_at,expires_at,strategy,slug,cond,outcome,p,ask,edge,suggested_usd,k_src,tau,status,account_id)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,'pending',?)`,
		p.CreatedAt, p.ExpiresAt, p.Strategy, p.Slug, p.Cond, p.Outcome, p.P, p.Ask, p.Edge, p.SuggestedUSD, p.KSrc, p.Tau, p.AccountID)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

// PMProposals 返回最近的提案列表（先自动把已过期的 pending 标为 expired）。
func (s *Store) PMProposals(ctx context.Context, limit int, now time.Time) ([]PMProposal, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE pm_proposal SET status='expired', reason='已过确认时限' WHERE status='pending' AND expires_at<=?`, now.UnixMilli())
	rows, err := s.db.QueryContext(ctx, `SELECT id,created_at,expires_at,strategy,slug,cond,outcome,p,ask,edge,suggested_usd,k_src,tau,status,account_id,order_id,reason
		FROM pm_proposal ORDER BY CASE WHEN status='pending' THEN 0 ELSE 1 END, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PMProposal{}
	for rows.Next() {
		var x PMProposal
		if err := rows.Scan(&x.ID, &x.CreatedAt, &x.ExpiresAt, &x.Strategy, &x.Slug, &x.Cond, &x.Outcome, &x.P, &x.Ask, &x.Edge,
			&x.SuggestedUSD, &x.KSrc, &x.Tau, &x.Status, &x.AccountID, &x.OrderID, &x.Reason); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// PMProposalByID 按 ID 读取提案。
func (s *Store) PMProposalByID(ctx context.Context, id int64) (PMProposal, error) {
	var x PMProposal
	err := s.db.QueryRowContext(ctx, `SELECT id,created_at,expires_at,strategy,slug,cond,outcome,p,ask,edge,suggested_usd,k_src,tau,status,account_id,order_id,reason
		FROM pm_proposal WHERE id=?`, id).
		Scan(&x.ID, &x.CreatedAt, &x.ExpiresAt, &x.Strategy, &x.Slug, &x.Cond, &x.Outcome, &x.P, &x.Ask, &x.Edge,
			&x.SuggestedUSD, &x.KSrc, &x.Tau, &x.Status, &x.AccountID, &x.OrderID, &x.Reason)
	return x, err
}

// ResolvePMProposal 更新待审批提案的最终状态（仅限 pending）。
func (s *Store) ResolvePMProposal(ctx context.Context, id int64, status string, orderID int64, reason string) error {
	r, err := s.db.ExecContext(ctx, `UPDATE pm_proposal SET status=?, order_id=?, reason=? WHERE id=? AND status='pending'`,
		status, orderID, reason, id)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
