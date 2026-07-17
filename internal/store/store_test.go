package store

import (
	"testing"
)

func TestPutAndGet(t *testing.T) {
	s := NewMemoryStore()
	pkg := Package{
		Name:           "test-patch",
		Version:        "1.0.0",
		Description:    "A test patch",
		Author:         "test",
		Size:           1024,
		ChecksumSHA256: "abc123",
		Tags:           []string{"test", "ai"},
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
	if got.Status != "active" {
		t.Errorf("expected default status active, got %s", got.Status)
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
	_ = s.Put(Package{Name: "alpha-patch", Version: "1.0.0", Description: "first"})
	_ = s.Put(Package{Name: "beta-patch", Version: "1.0.0", Description: "second"})

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
	_ = s.Put(Package{Name: "p1", Version: "1.0.0", Description: "machine learning model"})
	_ = s.Put(Package{Name: "p2", Version: "1.0.0", Description: "data processor"})

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
	_ = s.Put(Package{Name: "p1", Version: "1.0.0", Description: "desc", Tags: []string{"vision", "gpu"}})
	_ = s.Put(Package{Name: "p2", Version: "1.0.0", Description: "desc", Tags: []string{"audio"}})

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
	_ = s.Put(Package{Name: "p1", Version: "1.0.0", Description: "desc"})
	_ = s.Put(Package{Name: "p2", Version: "1.0.0", Description: "desc"})

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
	_ = s.Put(Package{Name: "del-me", Version: "1.0.0", Description: "delete test"})

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
	_ = s.Put(Package{Name: "b", Version: "1.0.0", Description: "beta"})
	_ = s.Put(Package{Name: "a", Version: "1.0.0", Description: "alpha"})

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
	_ = s.Put(Package{Name: "test", Version: "1.0.0", Description: "original"})
	_ = s.Put(Package{Name: "test", Version: "1.0.0", Description: "updated"})

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

func TestVersions(t *testing.T) {
	s := NewMemoryStore()
	_ = s.Put(Package{Name: "multi", Version: "1.0.0", Description: "v1"})
	_ = s.Put(Package{Name: "multi", Version: "2.0.0", Description: "v2"})
	_ = s.Put(Package{Name: "other", Version: "1.0.0", Description: "other"})

	versions, err := s.Versions("multi")
	if err != nil {
		t.Fatalf("Versions failed: %v", err)
	}
	if len(versions) != 2 {
		t.Errorf("expected 2 versions, got %d", len(versions))
	}
	if versions[0].Version != "2.0.0" {
		t.Errorf("expected first version 2.0.0 (descending), got %s", versions[0].Version)
	}
}

func TestVersionsNotFound(t *testing.T) {
	s := NewMemoryStore()
	versions, err := s.Versions("nonexistent")
	if err != nil {
		t.Fatalf("Versions failed: %v", err)
	}
	if len(versions) != 0 {
		t.Errorf("expected 0 versions, got %d", len(versions))
	}
}

func TestIncrementDownloads(t *testing.T) {
	s := NewMemoryStore()
	_ = s.Put(Package{Name: "popular", Version: "1.0.0", Description: "desc"})

	count, err := s.IncrementDownloads("popular", "1.0.0")
	if err != nil {
		t.Fatalf("IncrementDownloads failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected count 1, got %d", count)
	}

	count, err = s.IncrementDownloads("popular", "1.0.0")
	if err != nil {
		t.Fatalf("IncrementDownloads failed: %v", err)
	}
	if count != 2 {
		t.Errorf("expected count 2, got %d", count)
	}
}

func TestIncrementDownloadsNotFound(t *testing.T) {
	s := NewMemoryStore()
	_, err := s.IncrementDownloads("missing", "1.0.0")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestSetStatus(t *testing.T) {
	s := NewMemoryStore()
	_ = s.Put(Package{Name: "test", Version: "1.0.0", Description: "desc"})

	if err := s.SetStatus("test", "1.0.0", "deprecated"); err != nil {
		t.Fatalf("SetStatus failed: %v", err)
	}

	pkg, _ := s.Get("test", "1.0.0")
	if pkg.Status != "deprecated" {
		t.Errorf("expected status deprecated, got %s", pkg.Status)
	}
}

func TestSetStatusNotFound(t *testing.T) {
	s := NewMemoryStore()
	err := s.SetStatus("missing", "1.0.0", "deprecated")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestFileStore(t *testing.T) {
	path := t.TempDir() + "/patches.json"
	fs := NewFileStore(path)

	_ = fs.Put(Package{Name: "persist", Version: "1.0.0", Description: "survives restart"})

	// Create new FileStore loading from same path
	fs2 := NewFileStore(path)
	pkg, err := fs2.Get("persist", "1.0.0")
	if err != nil {
		t.Fatalf("Get from reloaded store failed: %v", err)
	}
	if pkg.Description != "survives restart" {
		t.Errorf("expected 'survives restart', got '%s'", pkg.Description)
	}
}

func TestFileStoreDelete(t *testing.T) {
	path := t.TempDir() + "/patches.json"
	fs := NewFileStore(path)

	_ = fs.Put(Package{Name: "tmp", Version: "1.0.0", Description: "temp"})
	_ = fs.Delete("tmp", "1.0.0")

	fs2 := NewFileStore(path)
	all, _ := fs2.List()
	if len(all) != 0 {
		t.Errorf("expected 0 packages after delete, got %d", len(all))
	}
}
