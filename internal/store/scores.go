package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"time"

	"market-thermometer/internal/engine"
)

// score_daily 是衍生数据，可随时从 raw_metric + 参数重算；结构变化时直接重建。
const scoreSchemaVersion = "3"

const scoreSchema = `
CREATE TABLE score_daily (
  market TEXT NOT NULL, trade_date TEXT NOT NULL, params_version TEXT NOT NULL,
  close REAL, erp REAL, v REAL, valuation_tier TEXT,
  heat REAL, leverage REAL, breadth REAL, rsi_d REAL, rsi_w REAL, r_mix REAL,
  t1 REAL, t2 REAL, t3 REAL, t4 REAL, t_base REAL, t_pct REAL, t_s REAL,
  n_low INT, n_high INT, degraded INT, state TEXT,
  PRIMARY KEY (market, params_version, trade_date)
) WITHOUT ROWID;
`

func (s *Store) migrateScores() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT)`); err != nil {
		return err
	}
	var v string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key='score_schema'`).Scan(&v)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if v == scoreSchemaVersion {
		return nil
	}
	_, err = s.db.Exec(`DROP TABLE IF EXISTS score_daily;` + scoreSchema +
		`INSERT INTO meta(key,value) VALUES('score_schema','` + scoreSchemaVersion + `')
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value;`)
	return err
}

// nf 把 NaN 转为 SQL NULL。
func nf(x float64) any {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return nil
	}
	return x
}

// fn 把 SQL NULL 读回为 NaN。
func fn(x sql.NullFloat64) float64 {
	if !x.Valid {
		return math.NaN()
	}
	return x.Float64
}

// ReplaceScores 用新结果整体替换某市场某参数版本的得分。
func (s *Store) ReplaceScores(ctx context.Context, mkt, version string, days []engine.Day) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM score_daily WHERE market=? AND params_version=?`, mkt, version); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO score_daily(market,trade_date,params_version,
		close,erp,v,valuation_tier,heat,leverage,breadth,rsi_d,rsi_w,r_mix,t1,t2,t3,t4,t_base,t_pct,t_s,n_low,n_high,degraded)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, d := range days {
		if _, err := stmt.ExecContext(ctx, mkt, d.Date, version,
			nf(d.Close), nf(d.ERP), nf(d.V), d.Tier, nf(d.Heat), nf(d.Leverage), nf(d.Breadth), nf(d.RSID), nf(d.RSIW), nf(d.RMix),
			nf(d.T[0]), nf(d.T[1]), nf(d.T[2]), nf(d.T[3]), nf(d.TBase), nf(d.TPct), nf(d.TS),
			d.NLow, d.NHigh, d.Degraded); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Scores 读取 [from, to] 的得分（日期为空表示不限）。
func (s *Store) Scores(ctx context.Context, mkt, version, from, to string) ([]engine.Day, error) {
	if to == "" {
		to = "9999-12-31"
	}
	rows, err := s.db.QueryContext(ctx, `SELECT trade_date,close,erp,v,COALESCE(valuation_tier,''),heat,leverage,breadth,rsi_d,rsi_w,r_mix,
		t1,t2,t3,t4,t_base,t_pct,t_s,n_low,n_high,degraded
		FROM score_daily WHERE market=? AND params_version=? AND trade_date>=? AND trade_date<=? ORDER BY trade_date`,
		mkt, version, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []engine.Day
	for rows.Next() {
		var d engine.Day
		var f [17]sql.NullFloat64
		if err := rows.Scan(&d.Date, &f[0], &f[1], &f[2], &d.Tier, &f[3], &f[4], &f[15], &f[5], &f[6], &f[7],
			&f[8], &f[9], &f[10], &f[11], &f[12], &f[13], &f[14], &d.NLow, &d.NHigh, &d.Degraded); err != nil {
			return nil, err
		}
		d.Close, d.ERP, d.V = fn(f[0]), fn(f[1]), fn(f[2])
		d.Heat, d.Leverage, d.RSID, d.RSIW, d.RMix = fn(f[3]), fn(f[4]), fn(f[5]), fn(f[6]), fn(f[7])
		d.Breadth = fn(f[15])
		d.T = [4]float64{fn(f[8]), fn(f[9]), fn(f[10]), fn(f[11])}
		d.TBase, d.TPct, d.TS = fn(f[12]), fn(f[13]), fn(f[14])
		out = append(out, d)
	}
	return out, rows.Err()
}

// ---- param_set ----

// ActiveParams 返回生效的参数集；没有时写入默认参数并设为生效。
// 读出的参数以默认值为底再覆盖，因此新增参数项对旧参数集自动取默认值；
// 合并后内容变化（版本号变化）时，以新版本号另存并设为生效。
func (s *Store) ActiveParams(ctx context.Context, now time.Time) (engine.Params, error) {
	var version, name, js string
	err := s.db.QueryRowContext(ctx, `SELECT version, COALESCE(name,''), params_json FROM param_set WHERE is_active=1 LIMIT 1`).
		Scan(&version, &name, &js)
	if errors.Is(err, sql.ErrNoRows) {
		p := engine.DefaultParams()
		return p, s.SaveParams(ctx, "默认参数", p, true, now)
	}
	if err != nil {
		return engine.Params{}, err
	}
	p := engine.DefaultParams()
	if err := json.Unmarshal([]byte(js), &p); err != nil {
		return engine.Params{}, err
	}
	if p.Version() != version {
		// 程序升级新增了参数项：以合并后的新版本替换旧记录（旧版本已无法复现）
		if err := s.SaveParams(ctx, name, p, true, now); err != nil {
			return p, err
		}
		_, err := s.db.ExecContext(ctx, `DELETE FROM param_set WHERE version=?`, version)
		return p, err
	}
	return p, nil
}

// ParamSet 是一个已保存的参数集。
type ParamSet struct {
	Version   string        `json:"version"`
	Name      string        `json:"name"`
	Active    bool          `json:"active"`
	CreatedAt string        `json:"createdAt"`
	Params    engine.Params `json:"params"`
}

// SaveParams 保存参数集（按内容哈希去重）；activate=true 时设为唯一生效。
func (s *Store) SaveParams(ctx context.Context, name string, p engine.Params, activate bool, now time.Time) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if activate {
		if _, err := tx.ExecContext(ctx, `UPDATE param_set SET is_active=0`); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO param_set(version,name,params_json,is_active,created_at) VALUES(?,?,?,?,?)
		ON CONFLICT(version) DO UPDATE SET name=excluded.name, is_active=MAX(param_set.is_active, excluded.is_active)`,
		p.Version(), name, string(b), activate, ts(now)); err != nil {
		return err
	}
	return tx.Commit()
}

// ListParams 列出全部参数集，生效的排在最前。
func (s *Store) ListParams(ctx context.Context) ([]ParamSet, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT version, COALESCE(name,''), is_active, COALESCE(created_at,''), params_json
		FROM param_set ORDER BY is_active DESC, created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ParamSet
	for rows.Next() {
		var ps ParamSet
		var js string
		if err := rows.Scan(&ps.Version, &ps.Name, &ps.Active, &ps.CreatedAt, &js); err != nil {
			return nil, err
		}
		ps.Params = engine.DefaultParams()
		if err := json.Unmarshal([]byte(js), &ps.Params); err != nil {
			return nil, err
		}
		out = append(out, ps)
	}
	return out, rows.Err()
}

// ReplaceEvents 用新结果整体替换某市场某参数版本的事件。
func (s *Store) ReplaceEvents(ctx context.Context, mkt, version string, evs []engine.Event) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM signal_event WHERE market=? AND params_version=?`, mkt, version); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO signal_event(market,trade_date,params_version,event,payload_json) VALUES(?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, e := range evs {
		b, _ := json.Marshal(eventJSON(e))
		if _, err := stmt.ExecContext(ctx, mkt, e.Date, version, e.Type, string(b)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// eventJSON：NaN 不能编码为 JSON，转为 null。
func eventJSON(e engine.Event) map[string]any {
	return map[string]any{"date": e.Date, "type": e.Type, "reason": e.Reason, "observe": e.Observe,
		"close": nf(e.Close), "tS": nf(e.TS), "v": nf(e.V), "tier": e.Tier}
}

// Events 读取事件（按日期升序），payload 为原始 JSON。
func (s *Store) Events(ctx context.Context, mkt, version string) ([]json.RawMessage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT payload_json FROM signal_event WHERE market=? AND params_version=? ORDER BY trade_date, id`, mkt, version)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var js string
		if err := rows.Scan(&js); err != nil {
			return nil, err
		}
		out = append(out, json.RawMessage(js))
	}
	return out, rows.Err()
}

// UpdateStates 写入每日状态机阶段。
func (s *Store) UpdateStates(ctx context.Context, mkt, version string, dates []string, phases []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `UPDATE score_daily SET state=? WHERE market=? AND params_version=? AND trade_date=?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for i, d := range dates {
		if _, err := stmt.ExecContext(ctx, phases[i], mkt, version, d); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PruneDerived 删除该市场非当前版本的得分与事件（派生数据随时可重算，只保留生效版本）。
func (s *Store) PruneDerived(ctx context.Context, mkt, keepVersion string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM score_daily WHERE market=? AND params_version<>?`, mkt, keepVersion); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM signal_event WHERE market=? AND params_version<>?`, mkt, keepVersion)
	return err
}

// LatestPhase 返回最新交易日的状态机阶段。
func (s *Store) LatestPhase(ctx context.Context, mkt, version string) (date, phase string, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT trade_date, COALESCE(state,'') FROM score_daily WHERE market=? AND params_version=?
		ORDER BY trade_date DESC LIMIT 1`, mkt, version).Scan(&date, &phase)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	return date, phase, err
}
