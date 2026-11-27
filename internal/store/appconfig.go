package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"market-thermometer/internal/config"
)

// app_config 保存数据库化的全部应用配置与管理员口令哈希，使服务可以脱离 config.yaml 零配置启动。
func (s *Store) migrateAppConfig() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS app_config (
		key        TEXT PRIMARY KEY,
		value_json TEXT NOT NULL,
		source     TEXT NOT NULL DEFAULT 'db',
		updated_at TEXT NOT NULL
	)`)
	return err
}

// LoadAppConfig 读取持久化在数据库中的主配置；若尚无记录则 exists=false。
func (s *Store) LoadAppConfig(ctx context.Context) (cfg *config.Config, source, updatedAt string, exists bool, err error) {
	var raw string
	err = s.db.QueryRowContext(ctx, `SELECT value_json, source, updated_at FROM app_config WHERE key='main'`).Scan(&raw, &source, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", "", false, nil
	}
	if err != nil {
		return nil, "", "", false, err
	}
	cfg = &config.Config{}
	if err := json.Unmarshal([]byte(raw), cfg); err != nil {
		return nil, "", "", false, err
	}
	if err := cfg.Normalize(); err != nil {
		return nil, "", "", false, err
	}
	if hash, herr := s.LoadAdminPasswordHash(ctx); herr == nil && hash != "" {
		cfg.AdminPasswordHash = hash
	}
	return cfg, source, updatedAt, true, nil
}

// SaveAppConfig 将完整配置持久化到数据库（不含密码哈希，密码哈希由 SaveAdminPasswordHash 单独存储）。
func (s *Store) SaveAppConfig(ctx context.Context, cfg *config.Config, source string, now time.Time) error {
	if err := cfg.Normalize(); err != nil {
		return err
	}
	if source == "" {
		source = "db"
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO app_config(key, value_json, source, updated_at) VALUES('main', ?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json, source=excluded.source, updated_at=excluded.updated_at`,
		string(b), source, ts(now))
	return err
}

// LoadAdminPasswordHash 读取库内保存的管理员 bcrypt 口令哈希；未设置时返回空字符串。
func (s *Store) LoadAdminPasswordHash(ctx context.Context) (string, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value_json FROM app_config WHERE key='auth'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var doc struct {
		PasswordHash string `json:"passwordHash"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return "", err
	}
	return doc.PasswordHash, nil
}

// SaveAdminPasswordHash 保存管理员 bcrypt 口令哈希到数据库。
func (s *Store) SaveAdminPasswordHash(ctx context.Context, hash string, now time.Time) error {
	b, err := json.Marshal(map[string]string{"passwordHash": hash})
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO app_config(key, value_json, source, updated_at) VALUES('auth', ?, 'db', ?)
		ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json, updated_at=excluded.updated_at`,
		string(b), ts(now))
	return err
}
