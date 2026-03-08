package store

import (
	"fmt"
	"log"
	"time"
)

// migrateFacts creates the facts table and indexes.
func (s *SQLiteStore) migrateFacts() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS facts (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			key        TEXT    NOT NULL,
			value      TEXT    NOT NULL,
			category   TEXT    NOT NULL DEFAULT 'general',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_facts_key ON facts(key);
		CREATE INDEX IF NOT EXISTS idx_facts_category ON facts(category);
	`)
	if err != nil {
		return fmt.Errorf("migrate facts: %w", err)
	}
	log.Printf("[store] facts table ready")
	return nil
}

// AddFact upserts a fact. If a fact with the given key exists, its value,
// category, and updated_at are updated. Otherwise a new fact is inserted.
func (s *SQLiteStore) AddFact(key, value, category string) error {
	if category == "" {
		category = "general"
	}
	now := time.Now().Unix()

	_, err := s.db.Exec(`
		INSERT INTO facts (key, value, category, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			value = excluded.value,
			category = excluded.category,
			updated_at = excluded.updated_at
	`, key, value, category, now, now)
	if err != nil {
		return fmt.Errorf("upsert fact: %w", err)
	}
	return nil
}

// GetFacts returns facts filtered by optional query (substring match on
// key or value) and category. Both filters are optional — omitting both
// returns all facts.
func (s *SQLiteStore) GetFacts(query, category string) ([]Fact, error) {
	q := "SELECT id, key, value, category, created_at, updated_at FROM facts WHERE 1=1"
	var args []interface{}

	if query != "" {
		q += " AND (key LIKE ? OR value LIKE ?)"
		pattern := "%" + query + "%"
		args = append(args, pattern, pattern)
	}
	if category != "" {
		q += " AND category = ?"
		args = append(args, category)
	}
	q += " ORDER BY updated_at DESC"

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("query facts: %w", err)
	}
	defer rows.Close()

	var facts []Fact
	for rows.Next() {
		var f Fact
		var createdAt, updatedAt int64
		if err := rows.Scan(&f.ID, &f.Key, &f.Value, &f.Category, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan fact: %w", err)
		}
		f.CreatedAt = time.Unix(createdAt, 0)
		f.UpdatedAt = time.Unix(updatedAt, 0)
		facts = append(facts, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate facts: %w", err)
	}
	if facts == nil {
		facts = []Fact{}
	}
	return facts, nil
}

// DeleteFact removes a fact by key.
func (s *SQLiteStore) DeleteFact(key string) error {
	_, err := s.db.Exec("DELETE FROM facts WHERE key = ?", key)
	if err != nil {
		return fmt.Errorf("delete fact: %w", err)
	}
	return nil
}
