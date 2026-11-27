package store

import (
	"context"
	"encoding/json"
	"time"
)

// 审计日志：记录关键操作（参数生效、熔断开关、模拟/实盘委托与拒单、推送与插件配置变更），只追加不删改。
const auditSchema = `
CREATE TABLE IF NOT EXISTS audit_log (
  id INTEGER PRIMARY KEY,
  at TEXT NOT NULL,
  actor TEXT NOT NULL,
  action TEXT NOT NULL,
  target TEXT NOT NULL DEFAULT '',
  detail_json TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS audit_log_at ON audit_log(at);
CREATE INDEX IF NOT EXISTS audit_log_action ON audit_log(action, at);
`

func (s *Store) migrateAudit() error {
	_, err := s.db.Exec(auditSchema)
	return err
}

// AuditEntry 是一条审计记录。
type AuditEntry struct {
	ID     int64          `json:"id"`
	At     string         `json:"at"`
	Actor  string         `json:"actor"`  // web | cli | strategy | system
	Action string         `json:"action"` // 如 pm.halt / pm.order / params.save / plugin.config
	Target string         `json:"target"` // 关联对象（账户 ID、参数版本、插件 ID 等）
	Detail map[string]any `json:"detail"`
}

// AddAudit 追加一条审计记录。
func (s *Store) AddAudit(ctx context.Context, actor, action, target string, detail map[string]any, now time.Time) error {
	if actor == "" {
		actor = "system"
	}
	if detail == nil {
		detail = map[string]any{}
	}
	js, err := json.Marshal(detail)
	if err != nil {
		js = []byte("{}")
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO audit_log(at, actor, action, target, detail_json) VALUES(?,?,?,?,?)`,
		ts(now), actor, action, target, string(js))
	return err
}

// RecentAudits 返回最近的审计记录（按 ID 倒序）；action 非空时按 action 前缀或精确过滤。
func (s *Store) RecentAudits(ctx context.Context, action string, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var (
		q    string
		args []any
	)
	if action != "" {
		q = `SELECT id, at, actor, action, target, detail_json FROM audit_log WHERE action=? OR action LIKE ? ORDER BY id DESC LIMIT ?`
		args = []any{action, action + ".%", limit}
	} else {
		q = `SELECT id, at, actor, action, target, detail_json FROM audit_log ORDER BY id DESC LIMIT ?`
		args = []any{limit}
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		var js string
		if err := rows.Scan(&e.ID, &e.At, &e.Actor, &e.Action, &e.Target, &js); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(js), &e.Detail)
		out = append(out, e)
	}
	return out, rows.Err()
}
