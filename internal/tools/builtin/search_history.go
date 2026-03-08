package builtin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Ask149/aidaemon/internal/store"
)

// SearchHistoryTool searches past conversation history using full-text search.
type SearchHistoryTool struct {
	Store *store.SQLiteStore
}

func (t *SearchHistoryTool) Name() string {
	return "search_history"
}

func (t *SearchHistoryTool) Description() string {
	return "Search past conversation history using full-text search. Use this to find previous discussions, decisions, or information from earlier sessions. Supports words, \"exact phrases\", AND/OR/NOT operators, and prefix* matching."
}

func (t *SearchHistoryTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Search query. Supports FTS5 syntax: words, \"exact phrases\", AND/OR/NOT, prefix*.",
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum results to return (default 10, max 50).",
			},
		},
		"required": []string{"query"},
	}
}

func (t *SearchHistoryTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	query, ok := args["query"].(string)
	if !ok || query == "" {
		return "", fmt.Errorf("query is required and must be a non-empty string")
	}

	limit := 10
	if v, ok := args["limit"].(float64); ok {
		limit = int(v)
	}

	results, err := t.Store.Search(query, limit)
	if err != nil {
		return "", fmt.Errorf("search: %w", err)
	}

	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal results: %w", err)
	}
	return string(data), nil
}
