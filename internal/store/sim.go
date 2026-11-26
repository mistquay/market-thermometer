package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"market-thermometer/internal/sim"
)

// 模拟交易：账户只记初始资金与交易参数；委托是唯一的事实记录（持仓与资金由回放得到）。
// 委托只新增与更新状态，不删除（删除账户时一并删除）。
const simSchema = `
CREATE TABLE IF NOT EXISTS sim_account (
  id INTEGER PRIMARY KEY, name TEXT NOT NULL, cash REAL NOT NULL, start_date TEXT NOT NULL,
  params_json TEXT NOT NULL, note TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS sim_order (
  id INTEGER PRIMARY KEY, account_id INTEGER NOT NULL, code TEXT NOT NULL, kind TEXT NOT NULL, side TEXT NOT NULL,
  qty REAL NOT NULL, placed_date TEXT NOT NULL, source TEXT NOT NULL, note TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL, fill_date TEXT NOT NULL DEFAULT '', fill_price REAL NOT NULL DEFAULT 0,
  amount REAL NOT NULL DEFAULT 0, fee REAL NOT NULL DEFAULT 0, reason TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS sim_order_account ON sim_order(account_id, id);
`

func (s *Store) migrateSim() error {
	_, err := s.db.Exec(simSchema)
	return err
}

// SimAccount 是一个模拟账户。
type SimAccount struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	Cash      float64    `json:"cash"`  // 初始资金
	Start     string     `json:"start"` // 起始日：只回放不早于它的交易日
	Params    sim.Params `json:"params"`
	Note      string     `json:"note"`
	CreatedAt string     `json:"createdAt"`
}

// SimAccounts 返回全部模拟账户。
func (s *Store) SimAccounts(ctx context.Context) ([]SimAccount, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, cash, start_date, params_json, note, created_at FROM sim_account ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SimAccount{}
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

type scanner interface{ Scan(dest ...any) error }

func scanAccount(r scanner) (SimAccount, error) {
	var a SimAccount
	var js string
	if err := r.Scan(&a.ID, &a.Name, &a.Cash, &a.Start, &js, &a.Note, &a.CreatedAt); err != nil {
		return a, err
	}
	a.Params = sim.DefaultParams()
	return a, json.Unmarshal([]byte(js), &a.Params)
}

// SimAccount 读取一个账户；不存在时返回 sql.ErrNoRows。
func (s *Store) SimAccount(ctx context.Context, id int64) (SimAccount, error) {
	return scanAccount(s.db.QueryRowContext(ctx, `SELECT id, name, cash, start_date, params_json, note, created_at FROM sim_account WHERE id=?`, id))
}

// CreateSimAccount 新建账户，返回 ID。
func (s *Store) CreateSimAccount(ctx context.Context, a SimAccount, now time.Time) (int64, error) {
	js, err := json.Marshal(a.Params)
	if err != nil {
		return 0, err
	}
	r, err := s.db.ExecContext(ctx, `INSERT INTO sim_account(name,cash,start_date,params_json,note,created_at) VALUES(?,?,?,?,?,?)`,
		a.Name, a.Cash, a.Start, string(js), a.Note, ts(now))
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

// DeleteSimAccount 删除账户及其全部委托。
func (s *Store) DeleteSimAccount(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM sim_order WHERE account_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sim_account WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// SimOrders 返回账户的全部委托（按 ID 升序）。
func (s *Store) SimOrders(ctx context.Context, accountID int64) ([]sim.Order, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, code, kind, side, qty, placed_date, source, note, status, fill_date, fill_price, amount, fee, reason
		FROM sim_order WHERE account_id=? ORDER BY id`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []sim.Order{}
	for rows.Next() {
		var o sim.Order
		if err := rows.Scan(&o.ID, &o.Code, &o.Kind, &o.Side, &o.Qty, &o.PlacedDate, &o.Source, &o.Note, &o.Status,
			&o.FillDate, &o.FillPrice, &o.Amount, &o.Fee, &o.Reason); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// InsertSimOrders 在一个事务里新增一批委托（状态为待成交），返回分配的 ID。
func (s *Store) InsertSimOrders(ctx context.Context, accountID int64, orders []sim.Order, now time.Time) ([]int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	ids := make([]int64, 0, len(orders))
	for _, o := range orders {
		r, err := tx.ExecContext(ctx, `INSERT INTO sim_order(account_id,code,kind,side,qty,placed_date,source,note,status,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?)`, accountID, o.Code, o.Kind, o.Side, o.Qty, o.PlacedDate, o.Source, o.Note, sim.StatusPending, ts(now), ts(now))
		if err != nil {
			return nil, err
		}
		id, err := r.LastInsertId()
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, tx.Commit()
}

// UpdateSimOrders 保存撮合结果（只更新仍为待成交的委托，已有结果不会被改写）。
func (s *Store) UpdateSimOrders(ctx context.Context, orders []sim.Order, now time.Time) error {
	if len(orders) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	st, err := tx.PrepareContext(ctx, `UPDATE sim_order SET status=?, fill_date=?, fill_price=?, amount=?, fee=?, reason=?, updated_at=?
		WHERE id=? AND status='pending'`)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, o := range orders {
		if _, err := st.ExecContext(ctx, o.Status, o.FillDate, o.FillPrice, o.Amount, o.Fee, o.Reason, ts(now), o.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// CancelSimOrder 撤销一笔待成交委托；已有结果的委托不能撤销（返回 sql.ErrNoRows）。
func (s *Store) CancelSimOrder(ctx context.Context, accountID, orderID int64, now time.Time) error {
	r, err := s.db.ExecContext(ctx, `UPDATE sim_order SET status=?, reason='用户撤单', updated_at=? WHERE id=? AND account_id=? AND status='pending'`,
		sim.StatusCanceled, ts(now), orderID, accountID)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
