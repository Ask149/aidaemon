package store

import (
	"path/filepath"
	"testing"
)

func TestFact_AddAndRecall(t *testing.T) {
	dir := t.TempDir()
	s, err := New(filepath.Join(dir, "test.db"), 100)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	// Add a fact.
	if err := s.AddFact("aws_account_id", "123456789012", "credentials"); err != nil {
		t.Fatalf("add fact: %v", err)
	}

	// Recall all facts.
	facts, err := s.GetFacts("", "")
	if err != nil {
		t.Fatalf("get facts: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(facts))
	}
	if facts[0].Key != "aws_account_id" {
		t.Errorf("key = %s, want aws_account_id", facts[0].Key)
	}
	if facts[0].Value != "123456789012" {
		t.Errorf("value = %s, want 123456789012", facts[0].Value)
	}
	if facts[0].Category != "credentials" {
		t.Errorf("category = %s, want credentials", facts[0].Category)
	}
}

func TestFact_Upsert(t *testing.T) {
	dir := t.TempDir()
	s, err := New(filepath.Join(dir, "test.db"), 100)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	s.AddFact("version", "1.24", "preferences")
	s.AddFact("version", "1.25", "preferences") // upsert

	facts, err := s.GetFacts("", "")
	if err != nil {
		t.Fatalf("get facts: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact after upsert, got %d", len(facts))
	}
	if facts[0].Value != "1.25" {
		t.Errorf("value = %s, want 1.25", facts[0].Value)
	}
}

func TestFact_FilterByCategory(t *testing.T) {
	dir := t.TempDir()
	s, err := New(filepath.Join(dir, "test.db"), 100)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	s.AddFact("aws_id", "123", "credentials")
	s.AddFact("go_version", "1.25", "preferences")
	s.AddFact("db_host", "localhost", "credentials")

	facts, err := s.GetFacts("", "credentials")
	if err != nil {
		t.Fatalf("get facts: %v", err)
	}
	if len(facts) != 2 {
		t.Fatalf("expected 2 credential facts, got %d", len(facts))
	}
}

func TestFact_FilterByQuery(t *testing.T) {
	dir := t.TempDir()
	s, err := New(filepath.Join(dir, "test.db"), 100)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	s.AddFact("aws_account_id", "123456789012", "credentials")
	s.AddFact("go_version", "1.25", "preferences")

	facts, err := s.GetFacts("aws", "")
	if err != nil {
		t.Fatalf("get facts: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact matching 'aws', got %d", len(facts))
	}
}

func TestFact_DeleteFact(t *testing.T) {
	dir := t.TempDir()
	s, err := New(filepath.Join(dir, "test.db"), 100)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	s.AddFact("temp", "value", "general")
	if err := s.DeleteFact("temp"); err != nil {
		t.Fatalf("delete fact: %v", err)
	}

	facts, err := s.GetFacts("", "")
	if err != nil {
		t.Fatalf("get facts: %v", err)
	}
	if len(facts) != 0 {
		t.Fatalf("expected 0 facts after delete, got %d", len(facts))
	}
}

func TestFact_DefaultCategory(t *testing.T) {
	dir := t.TempDir()
	s, err := New(filepath.Join(dir, "test.db"), 100)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer s.Close()

	s.AddFact("key1", "val1", "") // empty category should default to "general"

	facts, err := s.GetFacts("", "")
	if err != nil {
		t.Fatalf("get facts: %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(facts))
	}
	if facts[0].Category != "general" {
		t.Errorf("category = %s, want general", facts[0].Category)
	}
}
