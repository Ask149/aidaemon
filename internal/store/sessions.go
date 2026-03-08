package store

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"
)

// migrateSessions creates the sessions table and index if they don't exist.
func (s *SQLiteStore) migrateSessions() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS sessions (
			id              TEXT PRIMARY KEY,
			channel         TEXT NOT NULL,
			title           TEXT,
			status          TEXT NOT NULL,
			summary         TEXT,
			token_estimate  INTEGER NOT NULL DEFAULT 0,
			created_at      INTEGER NOT NULL,
			closed_at       INTEGER,
			last_activity   INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_sessions_channel_status
			ON sessions(channel, status);
		CREATE INDEX IF NOT EXISTS idx_sessions_last_activity
			ON sessions(last_activity DESC);
	`)
	if err != nil {
		return fmt.Errorf("migrate sessions: %w", err)
	}
	log.Printf("[store] sessions table ready")
	return nil
}

// CreateSession creates a new session.
func (s *SQLiteStore) CreateSession(sess Session) error {
	_, err := s.db.Exec(`
		INSERT INTO sessions (id, channel, title, status, summary, token_estimate, created_at, closed_at, last_activity)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		sess.ID,
		sess.Channel,
		nilIfEmpty(sess.Title),
		sess.Status,
		nilIfEmpty(sess.Summary),
		sess.TokenEstimate,
		sess.CreatedAt.Unix(),
		nilIfTime(sess.ClosedAt),
		sess.LastActivity.Unix(),
	)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	log.Printf("[store] created session %s for channel %s", sess.ID, sess.Channel)
	return nil
}

// GetSession returns a session by ID.
func (s *SQLiteStore) GetSession(id string) (*Session, error) {
	var sess Session
	var createdAt, lastActivity int64
	var closedAt sql.NullInt64
	var title, summary sql.NullString

	err := s.db.QueryRow(`
		SELECT id, channel, title, status, summary, token_estimate, created_at, closed_at, last_activity
		FROM sessions
		WHERE id = ?
	`, id).Scan(
		&sess.ID,
		&sess.Channel,
		&title,
		&sess.Status,
		&summary,
		&sess.TokenEstimate,
		&createdAt,
		&closedAt,
		&lastActivity,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	sess.CreatedAt = time.Unix(createdAt, 0)
	sess.LastActivity = time.Unix(lastActivity, 0)
	if closedAt.Valid {
		t := time.Unix(closedAt.Int64, 0)
		sess.ClosedAt = &t
	}
	if title.Valid {
		sess.Title = title.String
	}
	if summary.Valid {
		sess.Summary = summary.String
	}

	return &sess, nil
}

// ActiveSession returns the active session for a channel, or nil if none.
func (s *SQLiteStore) ActiveSession(channel string) (*Session, error) {
	var sess Session
	var createdAt, lastActivity int64
	var closedAt sql.NullInt64
	var title, summary sql.NullString

	err := s.db.QueryRow(`
		SELECT id, channel, title, status, summary, token_estimate, created_at, closed_at, last_activity
		FROM sessions
		WHERE channel = ? AND status = 'active'
		ORDER BY last_activity DESC
		LIMIT 1
	`, channel).Scan(
		&sess.ID,
		&sess.Channel,
		&title,
		&sess.Status,
		&summary,
		&sess.TokenEstimate,
		&createdAt,
		&closedAt,
		&lastActivity,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("active session: %w", err)
	}

	sess.CreatedAt = time.Unix(createdAt, 0)
	sess.LastActivity = time.Unix(lastActivity, 0)
	if closedAt.Valid {
		t := time.Unix(closedAt.Int64, 0)
		sess.ClosedAt = &t
	}
	if title.Valid {
		sess.Title = title.String
	}
	if summary.Valid {
		sess.Summary = summary.String
	}

	return &sess, nil
}

// UpdateSession updates session metadata.
func (s *SQLiteStore) UpdateSession(sess Session) error {
	_, err := s.db.Exec(`
		UPDATE sessions
		SET title = ?, status = ?, summary = ?, token_estimate = ?, closed_at = ?, last_activity = ?
		WHERE id = ?
	`,
		nilIfEmpty(sess.Title),
		sess.Status,
		nilIfEmpty(sess.Summary),
		sess.TokenEstimate,
		nilIfTime(sess.ClosedAt),
		sess.LastActivity.Unix(),
		sess.ID,
	)
	if err != nil {
		return fmt.Errorf("update session: %w", err)
	}
	log.Printf("[store] updated session %s", sess.ID)
	return nil
}

// ListAllSessions returns all sessions, optionally filtered by channel, newest first.
func (s *SQLiteStore) ListAllSessions(channel string) ([]Session, error) {
	query := `
		SELECT 
			s.id, s.channel, s.title, s.status, s.summary, s.token_estimate,
			s.created_at, s.closed_at, s.last_activity,
			COUNT(c.id) as message_count
		FROM sessions s
		LEFT JOIN conversations c ON c.chat_id = s.id
	`
	args := []interface{}{}

	if channel != "" {
		query += " WHERE s.channel = ?"
		args = append(args, channel)
	}

	query += `
		GROUP BY s.id
		ORDER BY s.last_activity DESC
	`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var sess Session
		var createdAt, lastActivity int64
		var closedAt sql.NullInt64
		var title, summary sql.NullString

		err := rows.Scan(
			&sess.ID,
			&sess.Channel,
			&title,
			&sess.Status,
			&summary,
			&sess.TokenEstimate,
			&createdAt,
			&closedAt,
			&lastActivity,
			&sess.MessageCount,
		)
		if err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}

		sess.CreatedAt = time.Unix(createdAt, 0)
		sess.LastActivity = time.Unix(lastActivity, 0)
		if closedAt.Valid {
			t := time.Unix(closedAt.Int64, 0)
			sess.ClosedAt = &t
		}
		if title.Valid {
			sess.Title = title.String
		}
		if summary.Valid {
			sess.Summary = summary.String
		}

		sessions = append(sessions, sess)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sessions: %w", err)
	}

	if sessions == nil {
		sessions = []Session{}
	}

	return sessions, nil
}

// MigrateExistingSessions creates session records for existing chat_id values in conversations table.
// This is a one-time migration for backward compatibility.
// Idempotent — skips chat_ids that already have sessions.
func (s *SQLiteStore) MigrateExistingSessions() error {
	// Find all chat_ids that don't have sessions yet.
	rows, err := s.db.Query(`
		SELECT DISTINCT c.chat_id, MIN(c.created_at) as first_msg, MAX(c.created_at) as last_msg
		FROM conversations c
		LEFT JOIN sessions s ON s.id = c.chat_id
		WHERE s.id IS NULL
		GROUP BY c.chat_id
	`)
	if err != nil {
		return fmt.Errorf("query existing chats: %w", err)
	}
	defer rows.Close()

	var toMigrate []struct {
		chatID   string
		firstMsg int64
		lastMsg  int64
	}

	for rows.Next() {
		var item struct {
			chatID   string
			firstMsg int64
			lastMsg  int64
		}
		if err := rows.Scan(&item.chatID, &item.firstMsg, &item.lastMsg); err != nil {
			return fmt.Errorf("scan chat_id: %w", err)
		}
		toMigrate = append(toMigrate, item)
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate chats: %w", err)
	}

	// Create sessions for each chat_id.
	for _, item := range toMigrate {
		// Extract channel from chat_id (format: "channel:session_id").
		channel := item.chatID
		if idx := strings.Index(item.chatID, ":"); idx != -1 {
			channel = item.chatID[:idx]
		}

		sess := Session{
			ID:            item.chatID,
			Channel:       channel,
			Status:        "active", // Assume active for existing chats.
			TokenEstimate: 0,
			CreatedAt:     time.Unix(item.firstMsg, 0),
			LastActivity:  time.Unix(item.lastMsg, 0),
		}

		if err := s.CreateSession(sess); err != nil {
			return fmt.Errorf("migrate session %s: %w", item.chatID, err)
		}
	}

	if len(toMigrate) > 0 {
		log.Printf("[store] migrated %d existing chat sessions", len(toMigrate))
	}

	return nil
}

// nilIfEmpty returns nil if s is empty, otherwise returns s.
func nilIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// nilIfTime returns nil if t is nil, otherwise returns t.Unix().
func nilIfTime(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return t.Unix()
}
