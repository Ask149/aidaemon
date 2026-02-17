package store

import (
	"database/sql"
	"fmt"
	"log"
	"time"
)

// migrateCronJobs creates the cron_jobs and cron_runs tables.
func (s *SQLiteStore) migrateCronJobs() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS cron_jobs (
			id           TEXT PRIMARY KEY,
			label        TEXT NOT NULL,
			cron_expr    TEXT NOT NULL,
			mode         TEXT NOT NULL,
			payload      TEXT NOT NULL,
			channel_type TEXT NOT NULL,
			channel_meta TEXT NOT NULL DEFAULT '{}',
			enabled      INTEGER NOT NULL DEFAULT 1,
			last_run_at  INTEGER,
			next_run_at  INTEGER,
			created_at   INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_cron_jobs_next_run
			ON cron_jobs(enabled, next_run_at);

		CREATE TABLE IF NOT EXISTS cron_runs (
			id          TEXT PRIMARY KEY,
			job_id      TEXT NOT NULL REFERENCES cron_jobs(id) ON DELETE CASCADE,
			started_at  INTEGER NOT NULL,
			finished_at INTEGER,
			status      TEXT NOT NULL,
			output      TEXT NOT NULL DEFAULT ''
		);
		CREATE INDEX IF NOT EXISTS idx_cron_runs_job
			ON cron_runs(job_id, started_at DESC);
	`)
	if err != nil {
		return fmt.Errorf("migrate cron jobs: %w", err)
	}
	log.Printf("[store] cron_jobs table ready")
	return nil
}

// CreateCronJob inserts a new cron job.
func (s *SQLiteStore) CreateCronJob(job CronJob) error {
	enabled := 0
	if job.Enabled {
		enabled = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO cron_jobs (id, label, cron_expr, mode, payload, channel_type, channel_meta, enabled, last_run_at, next_run_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		job.ID,
		job.Label,
		job.CronExpr,
		job.Mode,
		job.Payload,
		job.ChannelType,
		job.ChannelMeta,
		enabled,
		nilIfTime(job.LastRunAt),
		nilIfTime(job.NextRunAt),
		job.CreatedAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("create cron job: %w", err)
	}
	log.Printf("[store] created cron job %s: %s", job.ID, job.Label)
	return nil
}

// GetCronJob returns a cron job by ID, or nil if not found.
func (s *SQLiteStore) GetCronJob(id string) (*CronJob, error) {
	var job CronJob
	var enabled int
	var lastRunAt, nextRunAt sql.NullInt64
	var createdAt int64

	err := s.db.QueryRow(`
		SELECT id, label, cron_expr, mode, payload, channel_type, channel_meta, enabled, last_run_at, next_run_at, created_at
		FROM cron_jobs WHERE id = ?
	`, id).Scan(
		&job.ID, &job.Label, &job.CronExpr, &job.Mode, &job.Payload,
		&job.ChannelType, &job.ChannelMeta, &enabled,
		&lastRunAt, &nextRunAt, &createdAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get cron job: %w", err)
	}

	job.Enabled = enabled == 1
	job.CreatedAt = time.Unix(createdAt, 0)
	if lastRunAt.Valid {
		t := time.Unix(lastRunAt.Int64, 0)
		job.LastRunAt = &t
	}
	if nextRunAt.Valid {
		t := time.Unix(nextRunAt.Int64, 0)
		job.NextRunAt = &t
	}

	return &job, nil
}

// ListCronJobs returns all cron jobs, newest first.
func (s *SQLiteStore) ListCronJobs() ([]CronJob, error) {
	rows, err := s.db.Query(`
		SELECT id, label, cron_expr, mode, payload, channel_type, channel_meta, enabled, last_run_at, next_run_at, created_at
		FROM cron_jobs
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list cron jobs: %w", err)
	}
	defer rows.Close()

	var jobs []CronJob
	for rows.Next() {
		var job CronJob
		var enabled int
		var lastRunAt, nextRunAt sql.NullInt64
		var createdAt int64

		err := rows.Scan(
			&job.ID, &job.Label, &job.CronExpr, &job.Mode, &job.Payload,
			&job.ChannelType, &job.ChannelMeta, &enabled,
			&lastRunAt, &nextRunAt, &createdAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan cron job: %w", err)
		}

		job.Enabled = enabled == 1
		job.CreatedAt = time.Unix(createdAt, 0)
		if lastRunAt.Valid {
			t := time.Unix(lastRunAt.Int64, 0)
			job.LastRunAt = &t
		}
		if nextRunAt.Valid {
			t := time.Unix(nextRunAt.Int64, 0)
			job.NextRunAt = &t
		}

		jobs = append(jobs, job)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cron jobs: %w", err)
	}
	if jobs == nil {
		jobs = []CronJob{}
	}
	return jobs, nil
}

// UpdateCronJob updates a cron job.
func (s *SQLiteStore) UpdateCronJob(job CronJob) error {
	enabled := 0
	if job.Enabled {
		enabled = 1
	}
	_, err := s.db.Exec(`
		UPDATE cron_jobs
		SET label = ?, cron_expr = ?, mode = ?, payload = ?, enabled = ?, last_run_at = ?, next_run_at = ?
		WHERE id = ?
	`,
		job.Label, job.CronExpr, job.Mode, job.Payload, enabled,
		nilIfTime(job.LastRunAt), nilIfTime(job.NextRunAt),
		job.ID,
	)
	if err != nil {
		return fmt.Errorf("update cron job: %w", err)
	}
	return nil
}

// DeleteCronJob removes a cron job and its run history.
func (s *SQLiteStore) DeleteCronJob(id string) error {
	// Runs are deleted by CASCADE, but be explicit.
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM cron_runs WHERE job_id = ?", id); err != nil {
		return fmt.Errorf("delete cron runs: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM cron_jobs WHERE id = ?", id); err != nil {
		return fmt.Errorf("delete cron job: %w", err)
	}
	return tx.Commit()
}

// DueCronJobs returns enabled jobs whose next_run_at <= now.
func (s *SQLiteStore) DueCronJobs(now time.Time) ([]CronJob, error) {
	rows, err := s.db.Query(`
		SELECT id, label, cron_expr, mode, payload, channel_type, channel_meta, enabled, last_run_at, next_run_at, created_at
		FROM cron_jobs
		WHERE enabled = 1 AND next_run_at IS NOT NULL AND next_run_at <= ?
		ORDER BY next_run_at ASC
	`, now.Unix())
	if err != nil {
		return nil, fmt.Errorf("due cron jobs: %w", err)
	}
	defer rows.Close()

	var jobs []CronJob
	for rows.Next() {
		var job CronJob
		var enabled int
		var lastRunAt, nextRunAt sql.NullInt64
		var createdAt int64

		err := rows.Scan(
			&job.ID, &job.Label, &job.CronExpr, &job.Mode, &job.Payload,
			&job.ChannelType, &job.ChannelMeta, &enabled,
			&lastRunAt, &nextRunAt, &createdAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan due cron job: %w", err)
		}

		job.Enabled = enabled == 1
		job.CreatedAt = time.Unix(createdAt, 0)
		if lastRunAt.Valid {
			t := time.Unix(lastRunAt.Int64, 0)
			job.LastRunAt = &t
		}
		if nextRunAt.Valid {
			t := time.Unix(nextRunAt.Int64, 0)
			job.NextRunAt = &t
		}

		jobs = append(jobs, job)
	}
	return jobs, nil
}

// CreateCronRun records a job execution.
func (s *SQLiteStore) CreateCronRun(run CronRun) error {
	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO cron_runs (id, job_id, started_at, finished_at, status, output)
		VALUES (?, ?, ?, ?, ?, ?)
	`,
		run.ID,
		run.JobID,
		run.StartedAt.Unix(),
		nilIfTime(run.FinishedAt),
		run.Status,
		run.Output,
	)
	if err != nil {
		return fmt.Errorf("create cron run: %w", err)
	}
	return nil
}

// PruneCronRuns keeps only the most recent N runs for a job.
func (s *SQLiteStore) PruneCronRuns(jobID string, keep int) error {
	_, err := s.db.Exec(`
		DELETE FROM cron_runs
		WHERE job_id = ? AND id NOT IN (
			SELECT id FROM cron_runs
			WHERE job_id = ?
			ORDER BY started_at DESC
			LIMIT ?
		)
	`, jobID, jobID, keep)
	if err != nil {
		return fmt.Errorf("prune cron runs: %w", err)
	}
	return nil
}
