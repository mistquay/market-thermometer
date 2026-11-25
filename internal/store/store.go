// Package store 是 SQLite 持久化层（纯 Go 驱动，免 CGO）。
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	ds "market-thermometer/internal/datasource"
)

const schema = `
CREATE TABLE IF NOT EXISTS raw_metric (
  metric     TEXT NOT NULL,
  trade_date TEXT NOT NULL,
  source     TEXT NOT NULL,
  value      REAL NOT NULL,
  fetched_at TEXT NOT NULL,
  PRIMARY KEY (metric, trade_date, source)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS signal_event (
  id INTEGER PRIMARY KEY, market TEXT, trade_date TEXT, params_version TEXT,
  event TEXT, payload_json TEXT, notified_at TEXT
);

CREATE TABLE IF NOT EXISTS job_run (
  id INTEGER PRIMARY KEY, job TEXT NOT NULL, trade_date TEXT, status TEXT NOT NULL,
  progress REAL, detail TEXT, started_at TEXT NOT NULL, finished_at TEXT
);

CREATE TABLE IF NOT EXISTS param_set (
  version TEXT PRIMARY KEY, name TEXT, params_json TEXT NOT NULL, is_active INT DEFAULT 0, created_at TEXT
);
CREATE TABLE IF NOT EXISTS notify_channel (
  id INTEGER PRIMARY KEY, kind TEXT NOT NULL, name TEXT, config_json TEXT NOT NULL, enabled INT DEFAULT 1
);
CREATE TABLE IF NOT EXISTS web_session (
  token_hash TEXT PRIMARY KEY, created_at TEXT NOT NULL, expires_at TEXT NOT NULL
);
`

type Store struct{ db *sql.DB }

// Open 打开（必要时创建）数据库并执行建表。
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite 单写者；本项目的读写量用一个连接足够
	st := &Store{db: db}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	if err := st.migrateScores(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate scores: %w", err)
	}
	if err := st.migrateBreadth(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate breadth: %w", err)
	}
	if err := st.migrateFunds(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate funds: %w", err)
	}
	return st, nil
}

func (s *Store) Close() error { return s.db.Close() }

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// ---- raw_metric ----

// UpsertPoints 写入某指标某来源的数据点，已存在的覆盖。返回写入条数。
func (s *Store) UpsertPoints(ctx context.Context, metric ds.MetricID, source string, pts []ds.Point, fetchedAt time.Time) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO raw_metric(metric,trade_date,source,value,fetched_at) VALUES(?,?,?,?,?)
		ON CONFLICT(metric,trade_date,source) DO UPDATE SET value=excluded.value, fetched_at=excluded.fetched_at`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	f := ts(fetchedAt)
	for _, p := range pts {
		if _, err := stmt.ExecContext(ctx, string(metric), p.Date, source, p.Value, f); err != nil {
			return 0, err
		}
	}
	return len(pts), tx.Commit()
}

// Coverage 描述某指标某来源在库中的覆盖情况。
type Coverage struct {
	Metric    ds.MetricID `json:"metric"`
	Source    string      `json:"source"`
	First     string      `json:"first"`
	Last      string      `json:"last"`
	Count     int         `json:"count"`
	FetchedAt string      `json:"fetchedAt"`
}

func (s *Store) Coverage(ctx context.Context) ([]Coverage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT metric, source, MIN(trade_date), MAX(trade_date), COUNT(*), MAX(fetched_at)
		FROM raw_metric GROUP BY metric, source ORDER BY metric, source`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Coverage
	for rows.Next() {
		var c Coverage
		if err := rows.Scan(&c.Metric, &c.Source, &c.First, &c.Last, &c.Count, &c.FetchedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// LastDate 返回某指标（任一来源）在库中的最新日期；无数据时返回 ""。
func (s *Store) LastDate(ctx context.Context, metric ds.MetricID) (string, error) {
	var d sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT MAX(trade_date) FROM raw_metric WHERE metric=?`, string(metric)).Scan(&d)
	return d.String, err
}

// Series 返回某指标某来源在 [from, to] 的数据点（日期为空表示不限）。
func (s *Store) Series(ctx context.Context, metric ds.MetricID, source, from, to string) ([]ds.Point, error) {
	if to == "" {
		to = "9999-12-31"
	}
	rows, err := s.db.QueryContext(ctx, `SELECT trade_date, value FROM raw_metric
		WHERE metric=? AND source=? AND trade_date>=? AND trade_date<=? ORDER BY trade_date`,
		string(metric), source, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ds.Point
	for rows.Next() {
		var p ds.Point
		if err := rows.Scan(&p.Date, &p.Value); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ---- job_run ----

type JobRun struct {
	ID         int64   `json:"id"`
	Job        string  `json:"job"`
	Status     string  `json:"status"`
	Progress   float64 `json:"progress"`
	Detail     string  `json:"detail"`
	StartedAt  string  `json:"startedAt"`
	FinishedAt string  `json:"finishedAt"`
}

func (s *Store) JobStart(ctx context.Context, job string, now time.Time) (int64, error) {
	r, err := s.db.ExecContext(ctx, `INSERT INTO job_run(job,status,progress,started_at) VALUES(?, 'running', 0, ?)`, job, ts(now))
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

func (s *Store) JobFinish(ctx context.Context, id int64, status, detail string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE job_run SET status=?, progress=1, detail=?, finished_at=? WHERE id=?`,
		status, detail, ts(now), id)
	return err
}

// FailStaleJobs 把上次进程退出时遗留的 running 任务标记为中断。
func (s *Store) FailStaleJobs(ctx context.Context, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE job_run SET status='interrupted', finished_at=? WHERE status='running'`, ts(now))
	return err
}

func (s *Store) RecentJobs(ctx context.Context, limit int) ([]JobRun, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, job, status, COALESCE(progress,0), COALESCE(detail,''), started_at, COALESCE(finished_at,'')
		FROM job_run ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []JobRun
	for rows.Next() {
		var j JobRun
		if err := rows.Scan(&j.ID, &j.Job, &j.Status, &j.Progress, &j.Detail, &j.StartedAt, &j.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// ---- web_session ----

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// CreateSession 保存会话（只存 token 的哈希）。
func (s *Store) CreateSession(ctx context.Context, token string, now time.Time, ttl time.Duration) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO web_session(token_hash, created_at, expires_at) VALUES(?,?,?)`,
		hashToken(token), ts(now), ts(now.Add(ttl)))
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM web_session WHERE expires_at < ?`, ts(now))
	return err
}

// ValidSession 判断会话是否存在且未过期。
func (s *Store) ValidSession(ctx context.Context, token string, now time.Time) (bool, error) {
	var exp string
	err := s.db.QueryRowContext(ctx, `SELECT expires_at FROM web_session WHERE token_hash=?`, hashToken(token)).Scan(&exp)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return exp > ts(now), nil
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM web_session WHERE token_hash=?`, hashToken(token))
	return err
}
