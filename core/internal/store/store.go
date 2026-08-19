// Package store persists replaceable FlowIR snapshots in a repository-local SQLite database.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"

	"codeflow/core/internal/flowir"
	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

func Open(dir string) (*Store, error) {
	db, err := sql.Open("sqlite", filepath.Join(dir, "state.db"))
	if err != nil {
		return nil, err
	}
	if _, err = db.Exec("PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; CREATE TABLE IF NOT EXISTS snapshots (flow_id TEXT PRIMARY KEY, content BLOB NOT NULL, published_at TEXT NOT NULL, runtime_status TEXT NOT NULL); CREATE TABLE IF NOT EXISTS debt_reviews (flow_id TEXT NOT NULL, debt_id TEXT NOT NULL, question TEXT NOT NULL, reason TEXT NOT NULL, state TEXT NOT NULL CHECK(state IN ('open','accepted','resolved')), reviewed_at TEXT NOT NULL, PRIMARY KEY(flow_id,debt_id));"); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize flow storage: %w", err)
	}
	return &Store{db: db}, nil
}
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Publish(ctx context.Context, document flowir.Document, publishedAt, runtimeStatus string) error {
	if err := flowir.Validate(document); err != nil {
		return err
	}
	content, err := flowir.CanonicalJSON(document)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO snapshots(flow_id,content,published_at,runtime_status) VALUES(?,?,?,?) ON CONFLICT(flow_id) DO UPDATE SET content=excluded.content,published_at=excluded.published_at,runtime_status=excluded.runtime_status`, document.Current.ID, content, publishedAt, runtimeStatus); err != nil {
		return err
	}
	args := []any{publishedAt, document.Current.ID}
	query := "UPDATE debt_reviews SET state='resolved',reviewed_at=? WHERE flow_id=?"
	if len(document.Unknowns) > 0 {
		query += " AND debt_id NOT IN ("
		for i, debt := range document.Unknowns {
			if i > 0 {
				query += ","
			}
			query += "?"
			args = append(args, debt.ID)
		}
		query += ")"
	}
	if _, err = tx.ExecContext(ctx, query, args...); err != nil {
		return err
	}
	for _, debt := range document.Unknowns {
		if _, err = tx.ExecContext(ctx, `INSERT INTO debt_reviews(flow_id,debt_id,question,reason,state,reviewed_at) VALUES(?,?,?,?,?,?) ON CONFLICT(flow_id,debt_id) DO UPDATE SET question=excluded.question,reason=excluded.reason,state=CASE WHEN debt_reviews.state='resolved' THEN 'open' ELSE debt_reviews.state END,reviewed_at=CASE WHEN debt_reviews.state='resolved' THEN excluded.reviewed_at ELSE debt_reviews.reviewed_at END`, document.Current.ID, debt.ID, debt.Question, debt.Reason, "open", publishedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) Get(ctx context.Context, flowID string) (flowir.Document, string, string, error) {
	var content []byte
	var at, status string
	err := s.db.QueryRowContext(ctx, "SELECT content,published_at,runtime_status FROM snapshots WHERE flow_id=?", flowID).Scan(&content, &at, &status)
	if err != nil {
		return flowir.Document{}, "", "", err
	}
	var document flowir.Document
	if err := json.Unmarshal(content, &document); err != nil {
		return flowir.Document{}, "", "", err
	}
	return document, at, status, nil
}

// SetStatus changes publication metadata only; the deterministic FlowIR bytes
// remain the last verified snapshot.
func (s *Store) SetStatus(ctx context.Context, flowID, status string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE snapshots SET runtime_status=? WHERE flow_id=?", status, flowID)
	return err
}

type DebtReview struct {
	FlowID     string `json:"flow_id"`
	DebtID     string `json:"debt_id"`
	Question   string `json:"question"`
	Reason     string `json:"reason"`
	State      string `json:"state"`
	ReviewedAt string `json:"reviewed_at"`
}

func (s *Store) DebtReviews(ctx context.Context, flowID string) ([]DebtReview, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT flow_id,debt_id,question,reason,state,reviewed_at FROM debt_reviews WHERE flow_id=? ORDER BY debt_id`, flowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DebtReview
	for rows.Next() {
		var review DebtReview
		if err := rows.Scan(&review.FlowID, &review.DebtID, &review.Question, &review.Reason, &review.State, &review.ReviewedAt); err != nil {
			return nil, err
		}
		out = append(out, review)
	}
	return out, rows.Err()
}

func (s *Store) ReviewDebt(ctx context.Context, flowID, debtID, state, reviewedAt string) error {
	if state != "open" && state != "accepted" {
		return fmt.Errorf("debt review state must be open or accepted")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE debt_reviews SET state=?,reviewed_at=? WHERE flow_id=? AND debt_id=? AND state!='resolved'`, state, reviewedAt, flowID, debtID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return sql.ErrNoRows
	}
	return nil
}
