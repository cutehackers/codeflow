// Package store persists replaceable FlowIR snapshots in a repository-local SQLite database.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"codeflow/core/internal/flowir"
	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

type Publication struct {
	PublishedAt string `json:"published_at"`
	Status      string `json:"status"`
}

func Open(dir string) (*Store, error) {
	db, err := sql.Open("sqlite", filepath.Join(dir, "state.db"))
	if err != nil {
		return nil, err
	}
	var autoVacuum int
	if err = db.QueryRow("PRAGMA auto_vacuum").Scan(&autoVacuum); err != nil {
		db.Close()
		return nil, fmt.Errorf("inspect flow storage: %w", err)
	}
	if autoVacuum != 2 {
		if _, err = db.Exec("PRAGMA auto_vacuum=INCREMENTAL; VACUUM;"); err != nil {
			db.Close()
			return nil, fmt.Errorf("enable incremental flow storage cleanup: %w", err)
		}
	}
	if _, err = db.Exec("PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; CREATE TABLE IF NOT EXISTS snapshots (flow_id TEXT PRIMARY KEY, content BLOB NOT NULL, published_at TEXT NOT NULL, runtime_status TEXT NOT NULL); CREATE TABLE IF NOT EXISTS workspace_state (singleton INTEGER PRIMARY KEY CHECK(singleton=1), flow_ids BLOB NOT NULL, published_at TEXT NOT NULL, runtime_status TEXT NOT NULL); CREATE TABLE IF NOT EXISTS debt_reviews (flow_id TEXT NOT NULL, debt_id TEXT NOT NULL, question TEXT NOT NULL, reason TEXT NOT NULL, state TEXT NOT NULL CHECK(state IN ('open','accepted','resolved')), reviewed_at TEXT NOT NULL, PRIMARY KEY(flow_id,debt_id));"); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize flow storage: %w", err)
	}
	return &Store{db: db}, nil
}
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Publish(ctx context.Context, document flowir.Document, publishedAt, runtimeStatus string) error {
	return s.PublishBatch(ctx, []flowir.Document{document}, publishedAt, runtimeStatus)
}

// PublishBatch validates and canonicalizes every FlowIR document before one
// SQLite transaction replaces the active workspace. Readers therefore see the
// complete previous or complete new flow set, never a partially written batch.
func (s *Store) PublishBatch(ctx context.Context, documents []flowir.Document, publishedAt, runtimeStatus string) error {
	if len(documents) == 0 {
		return fmt.Errorf("workspace publication requires at least one flow")
	}
	contents := make([][]byte, len(documents))
	flowIDs := make([]string, len(documents))
	seen := map[string]bool{}
	first := documents[0].Basis
	for i, document := range documents {
		if err := flowir.Validate(document); err != nil {
			return err
		}
		if !flowir.SameBasis(document.Basis, first) {
			return fmt.Errorf("workspace flows must share one exact basis")
		}
		if seen[document.Current.ID] {
			return fmt.Errorf("workspace contains duplicate flow %s", document.Current.ID)
		}
		seen[document.Current.ID] = true
		content, err := flowir.CanonicalJSON(document)
		if err != nil {
			return err
		}
		contents[i] = content
		flowIDs[i] = document.Current.ID
	}
	encodedIDs, err := json.Marshal(flowIDs)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, document := range documents {
		if _, err = tx.ExecContext(ctx, `INSERT INTO snapshots(flow_id,content,published_at,runtime_status) VALUES(?,?,?,?) ON CONFLICT(flow_id) DO UPDATE SET content=excluded.content,published_at=excluded.published_at,runtime_status=excluded.runtime_status`, document.Current.ID, contents[i], publishedAt, runtimeStatus); err != nil {
			return err
		}
		if err = publishDebt(ctx, tx, document, publishedAt); err != nil {
			return err
		}
	}
	// Resolved debt is useful as a short audit trail, not an unbounded event
	// ledger. Active/open records are never removed by this retention rule.
	if _, err = tx.ExecContext(ctx, `DELETE FROM debt_reviews WHERE state='resolved' AND rowid IN (SELECT rowid FROM debt_reviews WHERE state='resolved' ORDER BY reviewed_at DESC LIMIT -1 OFFSET 200)`); err != nil {
		return err
	}
	activeArgs := make([]any, len(flowIDs))
	for i, flowID := range flowIDs {
		activeArgs[i] = flowID
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM snapshots WHERE flow_id NOT IN (`+strings.TrimSuffix(strings.Repeat("?,", len(flowIDs)), ",")+`)`, activeArgs...); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO workspace_state(singleton,flow_ids,published_at,runtime_status) VALUES(1,?,?,?) ON CONFLICT(singleton) DO UPDATE SET flow_ids=excluded.flow_ids,published_at=excluded.published_at,runtime_status=excluded.runtime_status`, encodedIDs, publishedAt, runtimeStatus); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	// Publication is already durable. Cleanup is opportunistic and may be
	// retried on the next publish without changing snapshot correctness.
	_, _ = s.db.ExecContext(ctx, "PRAGMA wal_checkpoint(PASSIVE); PRAGMA incremental_vacuum(256);")
	return nil
}

func publishDebt(ctx context.Context, tx *sql.Tx, document flowir.Document, publishedAt string) error {
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
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return err
	}
	for _, debt := range document.Unknowns {
		if _, err := tx.ExecContext(ctx, `INSERT INTO debt_reviews(flow_id,debt_id,question,reason,state,reviewed_at) VALUES(?,?,?,?,?,?) ON CONFLICT(flow_id,debt_id) DO UPDATE SET question=excluded.question,reason=excluded.reason,state=CASE WHEN debt_reviews.state='resolved' THEN 'open' ELSE debt_reviews.state END,reviewed_at=CASE WHEN debt_reviews.state='resolved' THEN excluded.reviewed_at ELSE debt_reviews.reviewed_at END`, document.Current.ID, debt.ID, debt.Question, debt.Reason, "open", publishedAt); err != nil {
			return err
		}
	}
	return nil
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

// GetBatch reads the active flow IDs and every corresponding snapshot through
// one read transaction, preserving the publication boundary for API and UI.
func (s *Store) GetBatch(ctx context.Context) ([]flowir.Document, string, string, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, "", "", err
	}
	defer tx.Rollback()
	var encoded []byte
	var at, status string
	if err = tx.QueryRowContext(ctx, `SELECT flow_ids,published_at,runtime_status FROM workspace_state WHERE singleton=1`).Scan(&encoded, &at, &status); err != nil {
		return nil, "", "", err
	}
	var flowIDs []string
	if err = json.Unmarshal(encoded, &flowIDs); err != nil || len(flowIDs) == 0 {
		return nil, "", "", fmt.Errorf("stored workspace flow list is invalid")
	}
	documents := make([]flowir.Document, 0, len(flowIDs))
	for _, flowID := range flowIDs {
		var content []byte
		if err = tx.QueryRowContext(ctx, `SELECT content FROM snapshots WHERE flow_id=?`, flowID).Scan(&content); err != nil {
			return nil, "", "", err
		}
		var document flowir.Document
		if err = json.Unmarshal(content, &document); err != nil {
			return nil, "", "", err
		}
		documents = append(documents, document)
	}
	if err = tx.Commit(); err != nil {
		return nil, "", "", err
	}
	return documents, at, status, nil
}

// Publication reads only the small runtime envelope needed by a browser to
// notice atomic snapshot replacement. It deliberately avoids decoding FlowIR.
func (s *Store) Publication(ctx context.Context) (Publication, error) {
	var result Publication
	err := s.db.QueryRowContext(ctx, `SELECT published_at,runtime_status FROM workspace_state WHERE singleton=1`).Scan(&result.PublishedAt, &result.Status)
	return result, err
}

// SetStatus changes publication metadata only; the deterministic FlowIR bytes
// remain the last verified snapshot.
func (s *Store) SetStatus(ctx context.Context, flowID, status string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE snapshots SET runtime_status=? WHERE flow_id=?", status, flowID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE workspace_state SET runtime_status=? WHERE singleton=1", status); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SetBatchStatus(ctx context.Context, status string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var encoded []byte
	if err = tx.QueryRowContext(ctx, `SELECT flow_ids FROM workspace_state WHERE singleton=1`).Scan(&encoded); err != nil {
		return err
	}
	var flowIDs []string
	if err = json.Unmarshal(encoded, &flowIDs); err != nil || len(flowIDs) == 0 {
		return fmt.Errorf("stored workspace flow list is invalid")
	}
	for _, flowID := range flowIDs {
		if _, err = tx.ExecContext(ctx, "UPDATE snapshots SET runtime_status=? WHERE flow_id=?", status, flowID); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "UPDATE workspace_state SET runtime_status=? WHERE singleton=1", status); err != nil {
		return err
	}
	return tx.Commit()
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
