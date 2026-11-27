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
	"strings"
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

type Store struct {
	db       *sql.DB
	tickDB   *sql.DB // 高频盘口/现货快照独立库（与主库分离，避免 A 股长同步阻塞盘口采集及膨胀主库备份）
	path     string
	tickPath string
}

// DefaultTickPath 由主库路径推导高频快照库路径（如 data/mt.db → data/mt-tick.db）。
func DefaultTickPath(path string) string {
	if path == ":memory:" || strings.HasPrefix(path, "file::memory:") {
		return ":memory:"
	}
	if strings.HasSuffix(strings.ToLower(path), ".db") {
		return path[:len(path)-3] + "-tick.db"
	}
	return path + "-tick.db"
}

// Open 打开（必要时创建）数据库并执行建表；高频盘口快照库自动使用 DefaultTickPath。
func Open(path string) (*Store, error) {
	return OpenWithTick(path, "")
}

// OpenWithTick 打开主库与独立的高频快照库（tickPath 为空时自动推导）。
func OpenWithTick(path, tickPath string) (*Store, error) {
	if tickPath == "" {
		tickPath = DefaultTickPath(path)
	}
	openDB := func(p string, pragmas string) (*sql.DB, error) {
		if p != ":memory:" && !strings.HasPrefix(p, "file::memory:") {
			if dir := filepath.Dir(p); dir != "." {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return nil, err
				}
			}
			p = "file:" + filepath.ToSlash(p) + "?" + pragmas
		}
		db, err := sql.Open("sqlite", p)
		if err != nil {
			return nil, err
		}
		db.SetMaxOpenConns(1) // SQLite 单写者
		return db, nil
	}
	db, err := openDB(path, "_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	tickDB, err := openDB(tickPath, "_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)")
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("open tick db: %w", err)
	}
	st := &Store{db: db, tickDB: tickDB, path: path, tickPath: tickPath}
	closeAll := func() {
		tickDB.Close()
		db.Close()
	}
	if _, err := db.Exec(schema); err != nil {
		closeAll()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	if err := st.migrateScores(); err != nil {
		closeAll()
		return nil, fmt.Errorf("migrate scores: %w", err)
	}
	if err := st.migrateBreadth(); err != nil {
		closeAll()
		return nil, fmt.Errorf("migrate breadth: %w", err)
	}
	if err := st.migrateFunds(); err != nil {
		closeAll()
		return nil, fmt.Errorf("migrate funds: %w", err)
	}
	if err := st.migrateNotify(); err != nil {
		closeAll()
		return nil, fmt.Errorf("migrate notify: %w", err)
	}
	if err := st.migrateSecurities(); err != nil {
		closeAll()
		return nil, fmt.Errorf("migrate securities: %w", err)
	}
	if err := st.migratePortfolio(); err != nil {
		closeAll()
		return nil, fmt.Errorf("migrate portfolio: %w", err)
	}
	if err := st.migrateExt(); err != nil {
		closeAll()
		return nil, fmt.Errorf("migrate ext: %w", err)
	}
	if err := st.migrateSim(); err != nil {
		closeAll()
		return nil, fmt.Errorf("migrate sim: %w", err)
	}
	if err := st.migrateCrypto(); err != nil {
		closeAll()
		return nil, fmt.Errorf("migrate crypto: %w", err)
	}
	if err := st.migratePM(); err != nil {
		closeAll()
		return nil, fmt.Errorf("migrate polymarket: %w", err)
	}
	if err := st.migratePMSim(); err != nil {
		closeAll()
		return nil, fmt.Errorf("migrate pm sim: %w", err)
	}
	if err := st.migratePMSignal(); err != nil {
		closeAll()
		return nil, fmt.Errorf("migrate pm signal: %w", err)
	}
	if err := st.migrateAudit(); err != nil {
		closeAll()
		return nil, fmt.Errorf("migrate audit: %w", err)
	}
	if err := st.migratePMProposal(); err != nil {
		closeAll()
		return nil, fmt.Errorf("migrate pm proposal: %w", err)
	}
	if err := st.migrateNotes(); err != nil {
		closeAll()
		return nil, fmt.Errorf("migrate notes: %w", err)
	}
	if err := st.migratePMWhale(); err != nil {
		closeAll()
		return nil, fmt.Errorf("migrate pm whale: %w", err)
	}
	if err := st.migrateStockWatch(); err != nil {
		closeAll()
		return nil, fmt.Errorf("migrate stock watch: %w", err)
	}
	if err := st.migrateAppConfig(); err != nil {
		closeAll()
		return nil, fmt.Errorf("migrate app config: %w", err)
	}
	if err := st.migrateLegacyPMBookToTick(); err != nil {
		closeAll()
		return nil, fmt.Errorf("migrate pm tick split: %w", err)
	}
	return st, nil
}

func (s *Store) Close() error {
	var e1, e2 error
	if s.tickDB != nil {
		e1 = s.tickDB.Close()
	}
	if s.db != nil {
		e2 = s.db.Close()
	}
	return errors.Join(e1, e2)
}

// Ping 检查主库与高频快照库的连通性。
func (s *Store) Ping(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return err
	}
	return s.tickDB.PingContext(ctx)
}

// Paths 返回主库与高频快照库路径（用于运维展示）。
func (s *Store) Paths() (mainPath, tickPath string) { return s.path, s.tickPath }

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

// LatestPoint 返回某指标在库中的最新数据点（按交易日降序）。
func (s *Store) LatestPoint(ctx context.Context, metric ds.MetricID) (ds.Point, bool, error) {
	var p ds.Point
	err := s.db.QueryRowContext(ctx, `SELECT trade_date, value FROM raw_metric WHERE metric=? ORDER BY trade_date DESC LIMIT 1`, string(metric)).Scan(&p.Date, &p.Value)
	if errors.Is(err, sql.ErrNoRows) {
		return p, false, nil
	}
	return p, err == nil, err
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
