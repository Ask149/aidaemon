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
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// SessionInfo describes a chat session.
type SessionInfo struct {
	ChatID       string    `json:"chat_id"`
	MessageCount int       `json:"message_count"`
	LastActivity time.Time `json:"last_activity"`
}

// Message is a single conversation message.
type Message struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// MessageWithID is a Message with its database row ID.
type MessageWithID struct {
	ID        int64     `json:"id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// Conversation defines the interface for conversation persistence.
// Any backend (SQLite, PostgreSQL, in-memory) can implement this.
type Conversation interface {
	// GetHistory returns the last N messages for a chat, ordered oldest→newest.
	GetHistory(chatID string) ([]Message, error)

	// AddMessage appends a message and trims old ones beyond the limit.
	AddMessage(chatID, role, content string) error

	// ClearChat deletes all messages for a chat.
	ClearChat(chatID string) error

	// MessageCount returns how many messages are stored for a chat.
	MessageCount(chatID string) (int, error)

	// GetOldestN returns the oldest N messages for a chat (for compaction).
	GetOldestN(chatID string, n int) ([]MessageWithID, error)

	// ReplaceMessages deletes messages with the given IDs and inserts a
	// replacement message (typically a summary).
	ReplaceMessages(chatID string, deleteIDs []int64, role, content string) error

	// Limit returns the configured max messages per chat.
	Limit() int

	// ListSessions returns info about all chat sessions, ordered by most recent activity.
	ListSessions() ([]SessionInfo, error)

	// Close closes the underlying storage.
	Close() error
}

// Store is an alias for SQLiteStore for backward compatibility.
// Existing code using *store.Store continues to work unchanged.
type Store = SQLiteStore

// SQLiteStore wraps a SQLite database for conversation history.
type SQLiteStore struct {
	db    *sql.DB
	limit int // max messages per chat
}

// Compile-time check: *SQLiteStore implements Conversation.
var _ Conversation = (*SQLiteStore)(nil)

// New opens (or creates) a SQLite database at path.
// limit is the max number of messages kept per conversation.
func New(path string, limit int) (*SQLiteStore, error) {
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

	s := &SQLiteStore{db: db, limit: limit}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return s, nil
}

// Close closes the database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// migrate creates tables if they don't exist.
func (s *SQLiteStore) migrate() error {
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
func (s *SQLiteStore) GetHistory(chatID string) ([]Message, error) {
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
func (s *SQLiteStore) AddMessage(chatID, role, content string) error {
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
func (s *SQLiteStore) ClearChat(chatID string) error {
	_, err := s.db.Exec("DELETE FROM conversations WHERE chat_id = ?", chatID)
	return err
}

// MessageCount returns how many messages are stored for a chat.
func (s *SQLiteStore) MessageCount(chatID string) (int, error) {
	var count int
	err := s.db.QueryRow(
		"SELECT COUNT(*) FROM conversations WHERE chat_id = ?", chatID,
	).Scan(&count)
	return count, err
}

// Limit returns the configured max messages per chat.
func (s *SQLiteStore) Limit() int {
	return s.limit
}

// ListSessions returns info about all chat sessions, ordered by most recent activity.
func (s *SQLiteStore) ListSessions() ([]SessionInfo, error) {
	rows, err := s.db.Query(`
		SELECT chat_id, COUNT(*) as msg_count, MAX(created_at) as last_active
		FROM conversations
		GROUP BY chat_id
		ORDER BY last_active DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []SessionInfo
	for rows.Next() {
		var si SessionInfo
		var ts int64
		if err := rows.Scan(&si.ChatID, &si.MessageCount, &ts); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		si.LastActivity = time.Unix(ts, 0)
		sessions = append(sessions, si)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sessions: %w", err)
	}
	if sessions == nil {
		sessions = []SessionInfo{}
	}
	return sessions, nil
}

// GetOldestN returns the oldest N messages for a chat (for compaction).
func (s *SQLiteStore) GetOldestN(chatID string, n int) ([]MessageWithID, error) {
	rows, err := s.db.Query(`
		SELECT id, role, content, created_at FROM conversations
		WHERE chat_id = ?
		ORDER BY created_at ASC
		LIMIT ?
	`, chatID, n)
	if err != nil {
		return nil, fmt.Errorf("query oldest: %w", err)
	}
	defer rows.Close()

	var msgs []MessageWithID
	for rows.Next() {
		var m MessageWithID
		var ts int64
		if err := rows.Scan(&m.ID, &m.Role, &m.Content, &ts); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		m.CreatedAt = time.Unix(ts, 0)
		msgs = append(msgs, m)
	}
	return msgs, nil
}

// ReplaceMessages deletes messages with the given IDs and inserts a
// replacement message (typically a summary). The replacement gets the
// timestamp of the oldest deleted message so it sorts correctly.
func (s *SQLiteStore) ReplaceMessages(chatID string, deleteIDs []int64, role, content string) error {
	if len(deleteIDs) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Get the earliest timestamp from the messages being deleted.
	var minTS int64
	err = tx.QueryRow(`
		SELECT MIN(created_at) FROM conversations
		WHERE chat_id = ? AND id IN (`+placeholders(len(deleteIDs))+`)
	`, append([]interface{}{chatID}, int64sToInterfaces(deleteIDs)...)...).Scan(&minTS)
	if err != nil {
		return fmt.Errorf("get min timestamp: %w", err)
	}

	// Delete the old messages.
	_, err = tx.Exec(`
		DELETE FROM conversations
		WHERE chat_id = ? AND id IN (`+placeholders(len(deleteIDs))+`)
	`, append([]interface{}{chatID}, int64sToInterfaces(deleteIDs)...)...)
	if err != nil {
		return fmt.Errorf("delete old messages: %w", err)
	}

	// Insert the summary with the earliest timestamp.
	_, err = tx.Exec(`
		INSERT INTO conversations (chat_id, role, content, created_at)
		VALUES (?, ?, ?, ?)
	`, chatID, role, content, minTS)
	if err != nil {
		return fmt.Errorf("insert summary: %w", err)
	}

	return tx.Commit()
}

// placeholders returns "?,?,?" for n items.
func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?,", n-1) + "?"
}

// int64sToInterfaces converts []int64 to []interface{} for query args.
func int64sToInterfaces(ids []int64) []interface{} {
	result := make([]interface{}, len(ids))
	for i, id := range ids {
		result[i] = id
	}
	return result
}

// MigrateChatIDs adds a channel prefix to bare chat IDs.
// Idempotent — skips IDs that already have a prefix.
func (s *SQLiteStore) MigrateChatIDs(prefix string) error {
	_, err := s.db.Exec(`
		UPDATE conversations
		SET chat_id = ? || ':' || chat_id
		WHERE chat_id NOT LIKE '%:%'
	`, prefix)
	return err
}
