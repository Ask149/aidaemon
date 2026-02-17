package store

import (
	"database/sql"
	"fmt"
	"log"
	"time"
)

// migrateWebhookRuns creates the webhook_runs table.
func (s *SQLiteStore) migrateWebhookRuns() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS webhook_runs (
			id           TEXT PRIMARY KEY,
			prompt       TEXT NOT NULL,
			payload      TEXT,
			source       TEXT,
			channel_type TEXT NOT NULL,
			channel_meta TEXT NOT NULL,
			status       TEXT NOT NULL,
			output       TEXT,
			started_at   INTEGER NOT NULL,
			finished_at  INTEGER
		);
		CREATE INDEX IF NOT EXISTS idx_webhook_runs_started
			ON webhook_runs(started_at DESC);
		CREATE INDEX IF NOT EXISTS idx_webhook_runs_status
			ON webhook_runs(status);
	`)
	if err != nil {
		return fmt.Errorf("migrate webhook runs: %w", err)
	}
	log.Printf("[store] webhook_runs table ready")
	return nil
}

// CreateWebhookRun inserts a new webhook run record.
func (s *SQLiteStore) CreateWebhookRun(run WebhookRun) error {
	_, err := s.db.Exec(`
		INSERT INTO webhook_runs (id, prompt, payload, source, channel_type, channel_meta, status, output, started_at, finished_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		run.ID,
		run.Prompt,
		nilIfEmpty(run.Payload),
		nilIfEmpty(run.Source),
		run.ChannelType,
		run.ChannelMeta,
		run.Status,
		nilIfEmpty(run.Output),
		run.StartedAt.Unix(),
		nilIfTime(run.FinishedAt),
	)
	if err != nil {
		return fmt.Errorf("create webhook run: %w", err)
	}
	log.Printf("[store] created webhook run %s (source=%s)", run.ID, run.Source)
	return nil
}

// UpdateWebhookRun updates a webhook run's status, output, and finished time.
func (s *SQLiteStore) UpdateWebhookRun(id, status, output string, finishedAt time.Time) error {
	_, err := s.db.Exec(`
		UPDATE webhook_runs
		SET status = ?, output = ?, finished_at = ?
		WHERE id = ?
	`,
		status,
		output,
		finishedAt.Unix(),
		id,
	)
	if err != nil {
		return fmt.Errorf("update webhook run: %w", err)
	}
	return nil
}

// GetWebhookRun returns a webhook run by ID, or nil if not found.
func (s *SQLiteStore) GetWebhookRun(id string) (*WebhookRun, error) {
	var run WebhookRun
	var payload, source, output sql.NullString
	var startedAt int64
	var finishedAt sql.NullInt64

	err := s.db.QueryRow(`
		SELECT id, prompt, payload, source, channel_type, channel_meta, status, output, started_at, finished_at
		FROM webhook_runs WHERE id = ?
	`, id).Scan(
		&run.ID, &run.Prompt, &payload, &source,
		&run.ChannelType, &run.ChannelMeta, &run.Status, &output,
		&startedAt, &finishedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get webhook run: %w", err)
	}

	run.Payload = payload.String
	run.Source = source.String
	run.Output = output.String
	run.StartedAt = time.Unix(startedAt, 0)
	if finishedAt.Valid {
		t := time.Unix(finishedAt.Int64, 0)
		run.FinishedAt = &t
	}

	return &run, nil
}

// ListWebhookRuns returns recent webhook runs, newest first.
func (s *SQLiteStore) ListWebhookRuns(limit, offset int) ([]WebhookRun, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	rows, err := s.db.Query(`
		SELECT id, prompt, payload, source, channel_type, channel_meta, status, output, started_at, finished_at
		FROM webhook_runs
		ORDER BY started_at DESC
		LIMIT ? OFFSET ?
	`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list webhook runs: %w", err)
	}
	defer rows.Close()

	var runs []WebhookRun
	for rows.Next() {
		var run WebhookRun
		var payload, source, output sql.NullString
		var startedAt int64
		var finishedAt sql.NullInt64

		err := rows.Scan(
			&run.ID, &run.Prompt, &payload, &source,
			&run.ChannelType, &run.ChannelMeta, &run.Status, &output,
			&startedAt, &finishedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan webhook run: %w", err)
		}

		run.Payload = payload.String
		run.Source = source.String
		run.Output = output.String
		run.StartedAt = time.Unix(startedAt, 0)
		if finishedAt.Valid {
			t := time.Unix(finishedAt.Int64, 0)
			run.FinishedAt = &t
		}

		runs = append(runs, run)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate webhook runs: %w", err)
	}
	if runs == nil {
		runs = []WebhookRun{}
	}
	return runs, nil
}
