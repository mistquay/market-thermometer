package store

import (
	"context"
	"database/sql"
	"time"
)

// 研究笔记：可绑定任意标的（stock:600519 / fund:510300 / portfolio:1 / pm:slug / general），用于沉淀投研复盘。
const notesSchema = `
CREATE TABLE IF NOT EXISTS research_note (
  id INTEGER PRIMARY KEY,
  target TEXT NOT NULL DEFAULT 'general',
  title TEXT NOT NULL,
  content TEXT NOT NULL DEFAULT '',
  tags TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS research_note_target ON research_note(target, updated_at);
`

func (s *Store) migrateNotes() error {
	_, err := s.db.Exec(notesSchema)
	return err
}

// ResearchNote 是一条研究笔记。
type ResearchNote struct {
	ID        int64  `json:"id"`
	Target    string `json:"target"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	Tags      string `json:"tags"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

func (s *Store) Notes(ctx context.Context, target string, limit int) ([]ResearchNote, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var (
		q    string
		args []any
	)
	if target != "" {
		q = `SELECT id, target, title, content, tags, created_at, updated_at FROM research_note WHERE target=? ORDER BY updated_at DESC, id DESC LIMIT ?`
		args = []any{target, limit}
	} else {
		q = `SELECT id, target, title, content, tags, created_at, updated_at FROM research_note ORDER BY updated_at DESC, id DESC LIMIT ?`
		args = []any{limit}
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ResearchNote{}
	for rows.Next() {
		var n ResearchNote
		if err := rows.Scan(&n.ID, &n.Target, &n.Title, &n.Content, &n.Tags, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) SaveNote(ctx context.Context, n ResearchNote, now time.Time) (int64, error) {
	t := ts(now)
	if n.Target == "" {
		n.Target = "general"
	}
	if n.ID == 0 {
		r, err := s.db.ExecContext(ctx, `INSERT INTO research_note(target,title,content,tags,created_at,updated_at) VALUES(?,?,?,?,?,?)`,
			n.Target, n.Title, n.Content, n.Tags, t, t)
		if err != nil {
			return 0, err
		}
		return r.LastInsertId()
	}
	r, err := s.db.ExecContext(ctx, `UPDATE research_note SET target=?, title=?, content=?, tags=?, updated_at=? WHERE id=?`,
		n.Target, n.Title, n.Content, n.Tags, t, n.ID)
	if err != nil {
		return 0, err
	}
	if cnt, _ := r.RowsAffected(); cnt == 0 {
		return 0, sql.ErrNoRows
	}
	return n.ID, nil
}

func (s *Store) DeleteNote(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM research_note WHERE id=?`, id)
	return err
}
