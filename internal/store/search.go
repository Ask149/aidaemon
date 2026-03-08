package store

import (
	"fmt"
	"log"
	"strings"
	"time"
)

// SearchResult represents a single full-text search hit.
type SearchResult struct {
	ChatID    string    `json:"chat_id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Snippet   string    `json:"snippet"`
	CreatedAt time.Time `json:"created_at"`
}

// migrateFTS creates the FTS5 virtual table and sync triggers.
// Idempotent — safe to call on every startup.
func (s *SQLiteStore) migrateFTS() error {
	_, err := s.db.Exec(`
		CREATE VIRTUAL TABLE IF NOT EXISTS conversations_fts USING fts5(
			content,
			content='conversations',
			content_rowid='id'
		)
	`)
	if err != nil {
		return fmt.Errorf("create FTS table: %w", err)
	}

	// Backfill: rebuild the FTS index if the table was just created but
	// conversations already has data (upgrade from pre-FTS database).
	// The FTS5 'rebuild' command repopulates the index from the content
	// table. It's idempotent — safe even if the index is already current.
	var justCreated bool
	if err := s.db.QueryRow(`
		SELECT NOT EXISTS(
			SELECT 1 FROM sqlite_master
			WHERE type='trigger' AND name='conversations_ai'
		)
	`).Scan(&justCreated); err != nil {
		return fmt.Errorf("check FTS trigger existence: %w", err)
	}

	// Now create the triggers (IF NOT EXISTS — idempotent).
	_, err = s.db.Exec(`
		CREATE TRIGGER IF NOT EXISTS conversations_ai AFTER INSERT ON conversations BEGIN
			INSERT INTO conversations_fts(rowid, content) VALUES (new.id, new.content);
		END;

		CREATE TRIGGER IF NOT EXISTS conversations_ad AFTER DELETE ON conversations BEGIN
			INSERT INTO conversations_fts(conversations_fts, rowid, content) VALUES('delete', old.id, old.content);
		END;

		CREATE TRIGGER IF NOT EXISTS conversations_au AFTER UPDATE ON conversations BEGIN
			INSERT INTO conversations_fts(conversations_fts, rowid, content) VALUES('delete', old.id, old.content);
			INSERT INTO conversations_fts(rowid, content) VALUES (new.id, new.content);
		END
	`)
	if err != nil {
		return fmt.Errorf("create FTS triggers: %w", err)
	}

	if justCreated {
		var convCount int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM conversations").Scan(&convCount); err != nil {
			return fmt.Errorf("count conversations for backfill: %w", err)
		}
		if convCount > 0 {
			_, err := s.db.Exec("INSERT INTO conversations_fts(conversations_fts) VALUES('rebuild')")
			if err != nil {
				return fmt.Errorf("rebuild FTS index: %w", err)
			}
			log.Printf("[store] FTS backfill: rebuilt index for %d existing messages", convCount)
		}
	}

	log.Printf("[store] FTS table ready")
	return nil
}

// Search performs a full-text search over conversation history.
// The query uses FTS5 MATCH syntax (words, "phrases", AND/OR/NOT, prefix*).
// Returns results ordered by relevance, limited to `limit`.
// Returns empty results for empty queries.
func (s *SQLiteStore) Search(query string, limit int) ([]SearchResult, error) {
	if strings.TrimSpace(query) == "" {
		return []SearchResult{}, nil
	}
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	rows, err := s.db.Query(`
		SELECT c.chat_id, c.role, c.content,
		       snippet(conversations_fts, 0, '»', '«', '...', 32) as snip,
		       c.created_at
		FROM conversations_fts f
		JOIN conversations c ON c.id = f.rowid
		WHERE conversations_fts MATCH ?
		ORDER BY rank
		LIMIT ?
	`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("FTS search: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		var ts int64
		if err := rows.Scan(&r.ChatID, &r.Role, &r.Content, &r.Snippet, &ts); err != nil {
			return nil, fmt.Errorf("scan search result: %w", err)
		}
		r.CreatedAt = time.Unix(ts, 0)
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate search results: %w", err)
	}
	if results == nil {
		results = []SearchResult{}
	}
	return results, nil
}
