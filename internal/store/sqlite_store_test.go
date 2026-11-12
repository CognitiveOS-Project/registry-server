package store

import (
	"testing"
)

func TestSQLiteStorePutAndGet(t *testing.T) {
	path := t.TempDir() + "/test.db"
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer s.Close()

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
	if got.Description != "A test patch" {
		t.Errorf("expected description 'A test patch', got %s", got.Description)
	}
	if got.Status != "active" {
		t.Errorf("expected default status active, got %s", got.Status)
	}
}

func TestSQLiteStoreGetNotFound(t *testing.T) {
	s, err := NewSQLiteStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer s.Close()

	_, err = s.Get("nonexistent", "1.0.0")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestSQLiteStoreSearchByName(t *testing.T) {
	s := newTestSQLiteStore(t)
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

func TestSQLiteStoreDelete(t *testing.T) {
	s := newTestSQLiteStore(t)
	_ = s.Put(Package{Name: "del-me", Version: "1.0.0", Description: "delete test"})

	if err := s.Delete("del-me", "1.0.0"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	_, err := s.Get("del-me", "1.0.0")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestSQLiteStoreList(t *testing.T) {
	s := newTestSQLiteStore(t)
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

func TestSQLiteStoreVersions(t *testing.T) {
	s := newTestSQLiteStore(t)
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

func TestSQLiteStoreIncrementDownloads(t *testing.T) {
	s := newTestSQLiteStore(t)
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

func TestSQLiteStoreIncrementDownloadsNotFound(t *testing.T) {
	s := newTestSQLiteStore(t)
	_, err := s.IncrementDownloads("missing", "1.0.0")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestSQLiteStoreSetStatus(t *testing.T) {
	s := newTestSQLiteStore(t)
	_ = s.Put(Package{Name: "test", Version: "1.0.0", Description: "desc"})

	if err := s.SetStatus("test", "1.0.0", "deprecated"); err != nil {
		t.Fatalf("SetStatus failed: %v", err)
	}

	pkg, _ := s.Get("test", "1.0.0")
	if pkg.Status != "deprecated" {
		t.Errorf("expected status deprecated, got %s", pkg.Status)
	}
}

func TestSQLiteStoreSetStatusNotFound(t *testing.T) {
	s := newTestSQLiteStore(t)
	err := s.SetStatus("missing", "1.0.0", "deprecated")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func newTestSQLiteStore(t *testing.T) *SQLiteStore {
	s, err := NewSQLiteStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	return s
}