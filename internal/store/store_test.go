package store

import (
	"testing"
)

func TestPutAndGet(t *testing.T) {
	s := NewMemoryStore()
	pkg := Package{
		Name:        "test-patch",
		Version:     "1.0.0",
		Description: "A test patch",
		Author:      "test",
		Size:        1024,
		SHA256:      "abc123",
		Tags:        []string{"test", "ai"},
	}

	if err := s.Put(pkg); err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	got, err := s.Get("test-patch", "1.0.0")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if got.Name != "test-patch" {
		t.Errorf("expected name test-patch, got %s", got.Name)
	}
	if got.Version != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %s", got.Version)
	}
	if got.CreatedAt == "" {
		t.Error("expected CreatedAt to be set")
	}
	if got.UpdatedAt == "" {
		t.Error("expected UpdatedAt to be set")
	}
}

func TestGetNotFound(t *testing.T) {
	s := NewMemoryStore()
	_, err := s.Get("nonexistent", "1.0.0")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestSearchByName(t *testing.T) {
	s := NewMemoryStore()
	s.Put(Package{Name: "alpha-patch", Version: "1.0.0", Description: "first"})
	s.Put(Package{Name: "beta-patch", Version: "1.0.0", Description: "second"})

	results, err := s.Search("alpha")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

func TestSearchByDescription(t *testing.T) {
	s := NewMemoryStore()
	s.Put(Package{Name: "p1", Version: "1.0.0", Description: "machine learning model"})
	s.Put(Package{Name: "p2", Version: "1.0.0", Description: "data processor"})

	results, err := s.Search("machine")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

func TestSearchByTag(t *testing.T) {
	s := NewMemoryStore()
	s.Put(Package{Name: "p1", Version: "1.0.0", Description: "desc", Tags: []string{"vision", "gpu"}})
	s.Put(Package{Name: "p2", Version: "1.0.0", Description: "desc", Tags: []string{"audio"}})

	results, err := s.Search("vision")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	s := NewMemoryStore()
	s.Put(Package{Name: "p1", Version: "1.0.0", Description: "desc"})
	s.Put(Package{Name: "p2", Version: "1.0.0", Description: "desc"})

	results, err := s.Search("")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}

func TestDelete(t *testing.T) {
	s := NewMemoryStore()
	s.Put(Package{Name: "del-me", Version: "1.0.0", Description: "delete test"})

	if err := s.Delete("del-me", "1.0.0"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	_, err := s.Get("del-me", "1.0.0")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestDeleteNotFound(t *testing.T) {
	s := NewMemoryStore()
	err := s.Delete("missing", "1.0.0")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestList(t *testing.T) {
	s := NewMemoryStore()
	s.Put(Package{Name: "b", Version: "1.0.0", Description: "beta"})
	s.Put(Package{Name: "a", Version: "1.0.0", Description: "alpha"})

	all, err := s.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("expected 2 packages, got %d", len(all))
	}
	if all[0].Name != "a" {
		t.Errorf("expected first to be 'a', got %s", all[0].Name)
	}
}

func TestPutUpdatesExisting(t *testing.T) {
	s := NewMemoryStore()
	s.Put(Package{Name: "test", Version: "1.0.0", Description: "original"})
	s.Put(Package{Name: "test", Version: "1.0.0", Description: "updated"})

	got, err := s.Get("test", "1.0.0")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.Description != "updated" {
		t.Errorf("expected description 'updated', got %s", got.Description)
	}
	if got.CreatedAt == "" {
		t.Error("expected CreatedAt to be retained")
	}
}
