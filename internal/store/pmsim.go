package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	ds "market-thermometer/internal/datasource"
	"market-thermometer/internal/pmsim"
)

// 预测市场模拟盘：账户只记初始资金与风控参数；委托（含撮合结果）是唯一的事实记录，持仓与资金由回放得到。
const pmSimSchema = `
CREATE TABLE IF NOT EXISTS pm_sim_account (
  id INTEGER PRIMARY KEY, name TEXT NOT NULL, cash REAL NOT NULL, params_json TEXT NOT NULL,
  note TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS pm_sim_order (
  id INTEGER PRIMARY KEY, account_id INTEGER NOT NULL, t INT NOT NULL, slug TEXT NOT NULL, cond TEXT NOT NULL, token TEXT NOT NULL,
  outcome INT NOT NULL, outcome_name TEXT NOT NULL, question TEXT NOT NULL, side TEXT NOT NULL,
  req_shares REAL NOT NULL DEFAULT 0, req_amount REAL NOT NULL DEFAULT 0, limit_p REAL NOT NULL DEFAULT 0, source TEXT NOT NULL,
  status TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '', shares REAL NOT NULL DEFAULT 0, gross REAL NOT NULL DEFAULT 0,
  fee REAL NOT NULL DEFAULT 0, levels TEXT NOT NULL DEFAULT '[]', book_t INT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS pm_sim_order_account ON pm_sim_order(account_id, t);
CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT);
`

func (s *Store) migratePMSim() error {
	_, err := s.db.Exec(pmSimSchema)
	return err
}

// PMSimAccount 是一个预测市场模拟账户。
type PMSimAccount struct {
	ID        int64        `json:"id"`
	Name      string       `json:"name"`
	Cash      float64      `json:"cash"` // 初始资金（USDC）
	Params    pmsim.Params `json:"params"`
	Note      string       `json:"note"`
	CreatedAt string       `json:"createdAt"`
}

func scanPMSimAccount(sc interface{ Scan(...any) error }) (PMSimAccount, error) {
	var a PMSimAccount
	var params string
	if err := sc.Scan(&a.ID, &a.Name, &a.Cash, &params, &a.Note, &a.CreatedAt); err != nil {
		return a, err
	}
	a.Params = pmsim.DefaultParams()
	_ = json.Unmarshal([]byte(params), &a.Params)
	return a, nil
}

func (s *Store) PMSimAccounts(ctx context.Context) ([]PMSimAccount, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, cash, params_json, note, created_at FROM pm_sim_account ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PMSimAccount{}
	for rows.Next() {
		a, err := scanPMSimAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// PMSimAccountByID 读取账户；没有时返回 sql.ErrNoRows。
func (s *Store) PMSimAccountByID(ctx context.Context, id int64) (PMSimAccount, error) {
	return scanPMSimAccount(s.db.QueryRowContext(ctx, `SELECT id, name, cash, params_json, note, created_at FROM pm_sim_account WHERE id=?`, id))
}

func (s *Store) CreatePMSimAccount(ctx context.Context, a PMSimAccount, now time.Time) (int64, error) {
	js, _ := json.Marshal(a.Params)
	r, err := s.db.ExecContext(ctx, `INSERT INTO pm_sim_account(name, cash, params_json, note, created_at) VALUES(?,?,?,?,?)`,
		a.Name, a.Cash, string(js), a.Note, ts(now))
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

// SetPMSimParams 更新账户风控参数（含账户熔断）。
func (s *Store) SetPMSimParams(ctx context.Context, id int64, p pmsim.Params) error {
	js, _ := json.Marshal(p)
	r, err := s.db.ExecContext(ctx, `UPDATE pm_sim_account SET params_json=? WHERE id=?`, string(js), id)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) DeletePMSimAccount(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM pm_sim_order WHERE account_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM pm_sim_account WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// AddPMSimOrder 写入一笔委托（含撮合结果），返回 ID。
func (s *Store) AddPMSimOrder(ctx context.Context, o pmsim.Order) (int64, error) {
	lv, _ := json.Marshal(o.Fill.Levels)
	var bookT int64
	if !o.BookTime.IsZero() {
		bookT = o.BookTime.UnixMilli()
	}
	r, err := s.db.ExecContext(ctx, `INSERT INTO pm_sim_order(account_id,t,slug,cond,token,outcome,outcome_name,question,side,req_shares,req_amount,limit_p,
		source,status,reason,shares,gross,fee,levels,book_t) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		o.Account, o.Time.UnixMilli(), o.Slug, o.Cond, o.Token, o.Outcome, o.OutcomeName, o.Question, string(o.Side), o.ReqShares, o.ReqAmount, o.Limit,
		o.Source, o.Status, o.Reason, o.Fill.Shares, o.Fill.Gross, o.Fill.Fee, string(lv), bookT)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

// PMSimOrders 返回账户的委托（按时间升序）。
func (s *Store) PMSimOrders(ctx context.Context, account int64) ([]pmsim.Order, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,account_id,t,slug,cond,token,outcome,outcome_name,question,side,req_shares,req_amount,limit_p,
		source,status,reason,shares,gross,fee,levels,book_t FROM pm_sim_order WHERE account_id=? ORDER BY t, id`, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []pmsim.Order{}
	for rows.Next() {
		var o pmsim.Order
		var t, bookT int64
		var side, levels string
		if err := rows.Scan(&o.ID, &o.Account, &t, &o.Slug, &o.Cond, &o.Token, &o.Outcome, &o.OutcomeName, &o.Question, &side, &o.ReqShares, &o.ReqAmount,
			&o.Limit, &o.Source, &o.Status, &o.Reason, &o.Fill.Shares, &o.Fill.Gross, &o.Fill.Fee, &levels, &bookT); err != nil {
			return nil, err
		}
		o.Side, o.Time = pmsim.Side(side), time.UnixMilli(t)
		if bookT > 0 {
			o.BookTime = time.UnixMilli(bookT)
		}
		_ = json.Unmarshal([]byte(levels), &o.Fill.Levels)
		out = append(out, o)
	}
	return out, rows.Err()
}

// PMSimMarkets 读取委托涉及的市场（回放结算、估值用）。
func (s *Store) PMSimMarkets(ctx context.Context, conds []string) (map[string]pmsim.Market, error) {
	out := map[string]pmsim.Market{}
	if len(conds) == 0 {
		return out, nil
	}
	args := make([]any, len(conds))
	for i, c := range conds {
		args[i] = c
	}
	rows, err := s.db.QueryContext(ctx, `SELECT m.condition_id, m.data, m.winner, e.slug FROM pm_market m JOIN pm_event e ON e.id=m.event_id
		WHERE m.condition_id IN (?`+strings.Repeat(",?", len(conds)-1)+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var cond, js, slug string
		var winner int
		if err := rows.Scan(&cond, &js, &winner, &slug); err != nil {
			return nil, err
		}
		var m ds.PMMarket
		if err := json.Unmarshal([]byte(js), &m); err != nil {
			return nil, err
		}
		out[cond] = pmsim.Market{Cond: cond, Slug: slug, Question: m.Question, Outcomes: m.Outcomes, Tokens: m.Tokens, End: m.End, Winner: winner}
	}
	return out, rows.Err()
}

// ---- 全局开关（meta 表） ----

func (s *Store) Meta(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

func (s *Store) SetMeta(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}
