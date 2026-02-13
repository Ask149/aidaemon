// Package store provides SQLite-backed conversation persistence.
//
// Uses WAL mode for concurrent read/write safety.
// Pure Go SQLite (modernc.org/sqlite) — no CGO required.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Message is a single conversation message.
type Message struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// Store wraps a SQLite database for conversation history.
type Store struct {
	db    *sql.DB
	limit int // max messages per chat
}

// New opens (or creates) a SQLite database at path.
// limit is the max number of messages kept per conversation.
func New(path string, limit int) (*Store, error) {
	// Ensure directory exists.
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// WAL mode: readers don't block writers, crash-safe.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}

	// Foreign keys on (good practice).
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	s := &Store{db: db, limit: limit}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return s, nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// migrate creates tables if they don't exist.
func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS conversations (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id    TEXT    NOT NULL,
			role       TEXT    NOT NULL,
			content    TEXT    NOT NULL,
			created_at INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_conv_chat
			ON conversations(chat_id, created_at);
	`)
	return err
}

// GetHistory returns the last N messages for a chat, ordered oldest→newest.
func (s *Store) GetHistory(chatID string) ([]Message, error) {
	rows, err := s.db.Query(`
		SELECT role, content, created_at FROM conversations
		WHERE chat_id = ?
		ORDER BY created_at DESC
		LIMIT ?
	`, chatID, s.limit)
	if err != nil {
		return nil, fmt.Errorf("query history: %w", err)
	}
	defer rows.Close()

	var msgs []Message
	for rows.Next() {
		var m Message
		var ts int64
		if err := rows.Scan(&m.Role, &m.Content, &ts); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		m.CreatedAt = time.Unix(ts, 0)
		msgs = append(msgs, m)
	}

	// Reverse to get oldest→newest order.
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}

	return msgs, nil
}

// AddMessage appends a message and trims old ones beyond the limit.
func (s *Store) AddMessage(chatID, role, content string) error {
	now := time.Now().Unix()

	if _, err := s.db.Exec(`
		INSERT INTO conversations (chat_id, role, content, created_at)
		VALUES (?, ?, ?, ?)
	`, chatID, role, content, now); err != nil {
		return fmt.Errorf("insert message: %w", err)
	}

	// Trim: keep only the newest `limit` messages for this chat.
	if _, err := s.db.Exec(`
		DELETE FROM conversations
		WHERE chat_id = ? AND id NOT IN (
			SELECT id FROM conversations
			WHERE chat_id = ?
			ORDER BY created_at DESC
			LIMIT ?
		)
	`, chatID, chatID, s.limit); err != nil {
		return fmt.Errorf("trim messages: %w", err)
	}

	return nil
}

// ClearChat deletes all messages for a chat.
func (s *Store) ClearChat(chatID string) error {
	_, err := s.db.Exec("DELETE FROM conversations WHERE chat_id = ?", chatID)
	return err
}

// MessageCount returns how many messages are stored for a chat.
func (s *Store) MessageCount(chatID string) (int, error) {
	var count int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM conversations WHERE chat_id = ?", chatID,
	).Scan(&count)
	return count, err
}
