package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ForwardedRequest tracks a request the portal sent us that we forwarded
// to EbookDB. No AutoMonitor — EbookDB has no monitoring feature.
type ForwardedRequest struct {
	RequestID  string
	ExternalID string
	Status     string
	LastPolled time.Time
	ErrorText  string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type RequestStats struct {
	Total       int `json:"total"`
	Active      int `json:"active"`
	Failed      int `json:"failed"`
	Imported    int `json:"imported"`
	WithErrors  int `json:"with_errors"`
	Unsubmitted int `json:"unsubmitted"`
}

var ErrNotFound = errors.New("not found")

func (s *Store) UpsertForwardedRequest(ctx context.Context, r ForwardedRequest) error {
	if r.RequestID == "" {
		return fmt.Errorf("request_id required")
	}
	if r.UpdatedAt.IsZero() {
		r.UpdatedAt = time.Now()
	}
	var (
		extPtr     *string
		errPtr     *string
		lastPolled *time.Time
	)
	if r.ExternalID != "" {
		v := r.ExternalID
		extPtr = &v
	}
	if r.ErrorText != "" {
		v := r.ErrorText
		errPtr = &v
	}
	if !r.LastPolled.IsZero() {
		v := r.LastPolled
		lastPolled = &v
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO forwarded_request (request_id, external_id, status, last_polled, error_text, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (request_id) DO UPDATE SET
			external_id = COALESCE(EXCLUDED.external_id, forwarded_request.external_id),
			-- Terminal guard: once a request is imported/failed (the set
			-- ListNonTerminal treats as terminal) a duplicate/late/replayed
			-- event must not resurrect it (event delivery is at-least-once).
			status      = CASE
			                WHEN forwarded_request.status IN ('imported','failed')
			                THEN forwarded_request.status
			                ELSE EXCLUDED.status
			              END,
			last_polled = COALESCE(EXCLUDED.last_polled, forwarded_request.last_polled),
			error_text  = COALESCE(EXCLUDED.error_text, forwarded_request.error_text),
			updated_at  = EXCLUDED.updated_at
	`, r.RequestID, extPtr, r.Status, lastPolled, errPtr, r.UpdatedAt)
	if err != nil {
		return fmt.Errorf("upsert forwarded_request: %w", err)
	}
	return nil
}

// MarkPolled records a successful upstream poll on an existing row: it
// advances status (honoring the terminal guard so a concurrent at-least-once
// replay can't be regressed), stamps last_polled/updated_at, and CLEARS
// error_text. Clearing matters: a prior transient failure set error_text and
// the success-path upsert can't unset it (COALESCE keeps the old value), so
// without this it sticks forever and RequestStats.WithErrors over-counts
// permanently. No-ops if the row is gone.
func (s *Store) MarkPolled(ctx context.Context, requestID, externalID, status string, when time.Time) error {
	if requestID == "" {
		return fmt.Errorf("request_id required")
	}
	if when.IsZero() {
		when = time.Now()
	}
	var extPtr *string
	if externalID != "" {
		v := externalID
		extPtr = &v
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE forwarded_request SET
			external_id = COALESCE($2, external_id),
			status      = CASE
			                 WHEN status IN ('imported','failed') THEN status
			                 ELSE $3
			               END,
			last_polled = $4,
			error_text  = NULL,
			updated_at  = $4
		WHERE request_id = $1
	`, requestID, extPtr, status, when)
	if err != nil {
		return fmt.Errorf("mark polled: %w", err)
	}
	return nil
}

func (s *Store) GetForwardedRequest(ctx context.Context, requestID string) (ForwardedRequest, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT request_id, COALESCE(external_id,''), status,
		       COALESCE(last_polled, '0001-01-01 00:00:00'::timestamptz),
		       COALESCE(error_text,''), created_at, updated_at
		FROM forwarded_request WHERE request_id = $1
	`, requestID)
	var r ForwardedRequest
	if err := row.Scan(&r.RequestID, &r.ExternalID, &r.Status, &r.LastPolled,
		&r.ErrorText, &r.CreatedAt, &r.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ForwardedRequest{}, ErrNotFound
		}
		return ForwardedRequest{}, fmt.Errorf("get forwarded_request: %w", err)
	}
	return r, nil
}

func (s *Store) ListNonTerminal(ctx context.Context, limit int) ([]ForwardedRequest, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT request_id, COALESCE(external_id,''), status,
		       COALESCE(last_polled, '0001-01-01 00:00:00'::timestamptz),
		       COALESCE(error_text,''), created_at, updated_at
		FROM forwarded_request
		WHERE status NOT IN ('imported','failed')
		ORDER BY COALESCE(last_polled, '0001-01-01 00:00:00'::timestamptz) ASC,
		         request_id ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list non-terminal: %w", err)
	}
	defer rows.Close()

	var out []ForwardedRequest
	for rows.Next() {
		var r ForwardedRequest
		if err := rows.Scan(&r.RequestID, &r.ExternalID, &r.Status, &r.LastPolled,
			&r.ErrorText, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, r)
	}
	return out, nil
}

// ListRequests returns a page of forwarded_request rows, newest first, with
// optional status filter and search across request_id / external_id. limit is
// clamped to [1, 200]. Returns total row count matching the same filter for
// pagination UX.
func (s *Store) ListRequests(ctx context.Context, status, search string, limit, offset int) ([]ForwardedRequest, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	conds := []string{"TRUE"}
	args := []any{}
	idx := 1
	if status != "" {
		conds = append(conds, fmt.Sprintf("status = $%d", idx))
		args = append(args, status)
		idx++
	}
	if search != "" {
		conds = append(conds, fmt.Sprintf("(request_id ILIKE $%d OR external_id ILIKE $%d)", idx, idx))
		args = append(args, "%"+search+"%")
		idx++
	}
	where := strings.Join(conds, " AND ")

	var total int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM forwarded_request WHERE `+where, args...,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count requests: %w", err)
	}

	args = append(args, limit, offset)
	rows, err := s.pool.Query(ctx, `
		SELECT request_id, COALESCE(external_id,''), status,
		       COALESCE(last_polled, '0001-01-01 00:00:00'::timestamptz),
		       COALESCE(error_text,''), created_at, updated_at
		FROM forwarded_request
		WHERE `+where+`
		ORDER BY created_at DESC
		LIMIT $`+fmt.Sprint(idx)+` OFFSET $`+fmt.Sprint(idx+1), args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list requests: %w", err)
	}
	defer rows.Close()

	var out []ForwardedRequest
	for rows.Next() {
		var r ForwardedRequest
		if err := rows.Scan(&r.RequestID, &r.ExternalID, &r.Status, &r.LastPolled,
			&r.ErrorText, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan: %w", err)
		}
		out = append(out, r)
	}
	return out, total, nil
}

// ListStuck returns non-terminal rows that haven't been polled in `threshold`
// (or were never polled and were created longer than `threshold` ago). Used
// by the admin "stuck requests" tile to surface rows that need attention.
func (s *Store) ListStuck(ctx context.Context, threshold time.Duration, limit int) ([]ForwardedRequest, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	cutoff := time.Now().Add(-threshold)
	rows, err := s.pool.Query(ctx, `
		SELECT request_id, COALESCE(external_id,''), status,
		       COALESCE(last_polled, '0001-01-01 00:00:00'::timestamptz),
		       COALESCE(error_text,''), created_at, updated_at
		FROM forwarded_request
		WHERE status NOT IN ('imported','failed')
		  AND (
		    (last_polled IS NOT NULL AND last_polled < $1)
		    OR (last_polled IS NULL AND created_at < $1)
		  )
		ORDER BY created_at ASC
		LIMIT $2`, cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("list stuck: %w", err)
	}
	defer rows.Close()
	var out []ForwardedRequest
	for rows.Next() {
		var r ForwardedRequest
		if err := rows.Scan(&r.RequestID, &r.ExternalID, &r.Status, &r.LastPolled,
			&r.ErrorText, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, r)
	}
	return out, nil
}

// RetryRequest force-resets a row back to 'acknowledged' (preserving
// external_id) so the reconciler picks it up on the next tick. Clears
// error_text and last_polled. No-op (returns ErrNotFound) if the row is
// gone or has no external_id (nothing for the reconciler to poll against).
func (s *Store) RetryRequest(ctx context.Context, requestID string) error {
	if requestID == "" {
		return fmt.Errorf("request_id required")
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE forwarded_request
		SET status = 'acknowledged',
		    error_text = NULL,
		    last_polled = NULL,
		    updated_at = now()
		WHERE request_id = $1
		  AND external_id IS NOT NULL
		  AND external_id <> ''`, requestID)
	if err != nil {
		return fmt.Errorf("retry: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkFailedManually drives a row to terminal 'failed' with an operator-
// supplied reason so the admin can clear out rows the reconciler can't make
// progress on. Idempotent on already-failed rows.
func (s *Store) MarkFailedManually(ctx context.Context, requestID, reason string) error {
	if requestID == "" {
		return fmt.Errorf("request_id required")
	}
	if reason == "" {
		reason = "marked failed by admin"
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE forwarded_request
		SET status = 'failed',
		    error_text = $2,
		    updated_at = now()
		WHERE request_id = $1`, requestID, reason)
	if err != nil {
		return fmt.Errorf("mark failed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) RequestStats(ctx context.Context) (RequestStats, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT
			COUNT(*)::int,
			COUNT(*) FILTER (WHERE status NOT IN ('imported','failed'))::int,
			COUNT(*) FILTER (WHERE status = 'failed')::int,
			COUNT(*) FILTER (WHERE status = 'imported')::int,
			COUNT(*) FILTER (WHERE COALESCE(error_text,'') <> '')::int,
			COUNT(*) FILTER (WHERE COALESCE(external_id,'') = '' AND status NOT IN ('imported','failed'))::int
		FROM forwarded_request
	`)
	var stats RequestStats
	if err := row.Scan(&stats.Total, &stats.Active, &stats.Failed, &stats.Imported, &stats.WithErrors, &stats.Unsubmitted); err != nil {
		return RequestStats{}, fmt.Errorf("request stats: %w", err)
	}
	return stats, nil
}
