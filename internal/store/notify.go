package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// 推送：渠道（notify_channel，建表见 schema）、推送记录（去重用）、开关等设置项（存 meta 表）。
const notifySchema = `
CREATE TABLE IF NOT EXISTS notify_log (
  key TEXT PRIMARY KEY, title TEXT NOT NULL, result TEXT NOT NULL, sent_at TEXT NOT NULL
) WITHOUT ROWID;
`

func (s *Store) migrateNotify() error {
	_, err := s.db.Exec(notifySchema)
	return err
}

// NotifyChannel 是一个推送渠道。Config 为渠道插件声明的字段（含 webhook、secret 等敏感值，对外输出前必须打码）。
type NotifyChannel struct {
	ID      int64             `json:"id"`
	Kind    string            `json:"kind"`
	Name    string            `json:"name"`
	Config  map[string]string `json:"config"`
	Enabled bool              `json:"enabled"`
}

// NotifyChannels 返回全部渠道（按 ID 升序）。
func (s *Store) NotifyChannels(ctx context.Context) ([]NotifyChannel, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, kind, COALESCE(name,''), config_json, enabled FROM notify_channel ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NotifyChannel{}
	for rows.Next() {
		var c NotifyChannel
		var js string
		if err := rows.Scan(&c.ID, &c.Kind, &c.Name, &js, &c.Enabled); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(js), &c.Config); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// NotifyChannel 按 ID 读取渠道；不存在时返回 sql.ErrNoRows。
func (s *Store) NotifyChannel(ctx context.Context, id int64) (NotifyChannel, error) {
	c := NotifyChannel{ID: id}
	var js string
	err := s.db.QueryRowContext(ctx, `SELECT kind, COALESCE(name,''), config_json, enabled FROM notify_channel WHERE id=?`, id).
		Scan(&c.Kind, &c.Name, &js, &c.Enabled)
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal([]byte(js), &c.Config)
}

// SaveNotifyChannel 新增（ID 为 0）或更新渠道，返回 ID。
func (s *Store) SaveNotifyChannel(ctx context.Context, c NotifyChannel) (int64, error) {
	js, err := json.Marshal(c.Config)
	if err != nil {
		return 0, err
	}
	if c.ID == 0 {
		r, err := s.db.ExecContext(ctx, `INSERT INTO notify_channel(kind,name,config_json,enabled) VALUES(?,?,?,?)`,
			c.Kind, c.Name, string(js), c.Enabled)
		if err != nil {
			return 0, err
		}
		return r.LastInsertId()
	}
	r, err := s.db.ExecContext(ctx, `UPDATE notify_channel SET kind=?, name=?, config_json=?, enabled=? WHERE id=?`,
		c.Kind, c.Name, string(js), c.Enabled, c.ID)
	if err != nil {
		return 0, err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return 0, sql.ErrNoRows
	}
	return c.ID, nil
}

func (s *Store) DeleteNotifyChannel(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM notify_channel WHERE id=?`, id)
	return err
}

// NotifyLog 是一条推送记录。Key 唯一标识一条消息（如 event:CN:2026-11-25:BUY_DIP），用于去重。
type NotifyLog struct {
	Key    string `json:"key"`
	Title  string `json:"title"`
	Result string `json:"result"` // 各渠道发送结果
	SentAt string `json:"sentAt"`
}

// NotifySent 判断该消息是否已推送过。
func (s *Store) NotifySent(ctx context.Context, key string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notify_log WHERE key=?`, key).Scan(&n)
	return n > 0, err
}

// LogNotify 记录一条已推送的消息（重复写入时覆盖）。
func (s *Store) LogNotify(ctx context.Context, l NotifyLog, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO notify_log(key,title,result,sent_at) VALUES(?,?,?,?)
		ON CONFLICT(key) DO UPDATE SET title=excluded.title, result=excluded.result, sent_at=excluded.sent_at`,
		l.Key, l.Title, l.Result, ts(now))
	return err
}

// RecentNotifyLogs 返回最近的推送记录。
func (s *Store) RecentNotifyLogs(ctx context.Context, limit int) ([]NotifyLog, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, title, result, sent_at FROM notify_log ORDER BY sent_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NotifyLog{}
	for rows.Next() {
		var l NotifyLog
		if err := rows.Scan(&l.Key, &l.Title, &l.Result, &l.SentAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Setting 读取设置项（存 meta 表）；不存在时返回 ""。
func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetSetting 写入设置项。
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}
