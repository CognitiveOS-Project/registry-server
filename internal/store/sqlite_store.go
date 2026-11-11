package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type SQLiteStore struct {
	db *sql.DB
	mu sync.Mutex
}

func NewSQLiteStore(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	if err := createTables(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("create tables: %w", err)
	}

	return &SQLiteStore{db: db}, nil
}

func createTables(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS packages (
		name TEXT NOT NULL,
		version TEXT NOT NULL,
		data TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		PRIMARY KEY (name, version)
	);
	CREATE INDEX IF NOT EXISTS idx_packages_name ON packages(name);
	CREATE INDEX IF NOT EXISTS idx_packages_status ON packages(data);
	`
	_, err := db.Exec(schema)
	return err
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) Search(query string) ([]Package, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	q := "%" + strings.ToLower(query) + "%"
	rows, err := s.db.Query(
		`SELECT data FROM packages WHERE LOWER(data) LIKE ? ORDER BY name`,
		q,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanPackages(rows)
}

func (s *SQLiteStore) SearchFiltered(query string, opts SearchOptions) ([]Package, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	where := "1=1"
	args := []interface{}{}

	if query != "" {
		q := "%" + strings.ToLower(query) + "%"
		where = `(LOWER(data) LIKE ? OR LOWER(data) LIKE ?)`
		args = append(args, q, q)
	}

	rows, err := s.db.Query(
		`SELECT data FROM packages WHERE `+where+` ORDER BY name`,
		args...,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	all, err := scanPackages(rows)
	if err != nil {
		return nil, 0, err
	}

	// Apply additional filters in Go (same logic as MemoryStore)
	var matched []Package
	for _, pkg := range all {
		if opts.License != "" && !strings.EqualFold(pkg.License, opts.License) {
			continue
		}
		if opts.Author != "" && !strings.Contains(strings.ToLower(pkg.Author), strings.ToLower(opts.Author)) {
			continue
		}
		if opts.Capability != "" {
			var m struct {
				Runtime struct {
					Capabilities []string `json:"capabilities"`
				} `json:"runtime"`
			}
			if err := json.Unmarshal([]byte(pkg.Manifest), &m); err == nil {
				found := false
				for _, c := range m.Runtime.Capabilities {
					if strings.EqualFold(c, opts.Capability) {
						found = true
						break
					}
				}
				if !found {
					continue
				}
			} else {
				continue
			}
		}
		if opts.MinRAM > 0 {
			if pkg.Hardware == nil || pkg.Hardware.MinRAMMB < opts.MinRAM {
				continue
			}
		}
		if opts.MinStorageMB > 0 {
			if pkg.Hardware == nil || pkg.Hardware.MinStorageMB < opts.MinStorageMB {
				continue
			}
		}
		matched = append(matched, pkg)
	}

	total := len(matched)

	if opts.Page < 1 {
		opts.Page = 1
	}
	if opts.PerPage < 1 {
		opts.PerPage = 20
	}

	start := (opts.Page - 1) * opts.PerPage
	if start >= len(matched) {
		return []Package{}, total, nil
	}
	end := start + opts.PerPage
	if end > len(matched) {
		end = len(matched)
	}

	return matched[start:end], total, nil
}

func (s *SQLiteStore) Get(name, version string) (Package, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var data string
	err := s.db.QueryRow(
		`SELECT data FROM packages WHERE name = ? AND version = ?`,
		name, version,
	).Scan(&data)
	if err != nil {
		if err == sql.ErrNoRows {
			return Package{}, ErrNotFound
		}
		return Package{}, err
	}

	return unmarshalPackage(data)
}

func (s *SQLiteStore) Put(pkg Package) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC().Format(time.RFC3339)
	data, err := json.Marshal(pkg)
	if err != nil {
		return fmt.Errorf("marshal package: %w", err)
	}

	_, err = s.db.Exec(`
		INSERT INTO packages (name, version, data, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(name, version) DO UPDATE SET
			data = excluded.data,
			updated_at = excluded.updated_at,
			created_at = COALESCE(
				(SELECT created_at FROM packages WHERE name = excluded.name AND version = excluded.version),
				excluded.created_at
			)
	`, pkg.Name, pkg.Version, string(data), now, now)
	if err != nil {
		return fmt.Errorf("upsert package: %w", err)
	}
	return nil
}

func (s *SQLiteStore) Delete(name, version string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	result, err := s.db.Exec(
		`DELETE FROM packages WHERE name = ? AND version = ?`,
		name, version,
	)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLiteStore) List() ([]Package, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(`SELECT data FROM packages ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanPackages(rows)
}

func (s *SQLiteStore) Versions(name string) ([]Package, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(
		`SELECT data FROM packages WHERE name = ? ORDER BY version DESC`,
		name,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanPackages(rows)
}

func (s *SQLiteStore) IncrementDownloads(name, version string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	pkg, err := s.Get(name, version)
	if err != nil {
		return 0, err
	}
	pkg.Downloads++
	if err := s.Put(pkg); err != nil {
		return 0, err
	}
	return pkg.Downloads, nil
}

func (s *SQLiteStore) SetStatus(name, version, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	pkg, err := s.Get(name, version)
	if err != nil {
		return err
	}
	pkg.Status = status
	pkg.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := s.Put(pkg); err != nil {
		return err
	}
	return nil
}

func scanPackages(rows *sql.Rows) ([]Package, error) {
	var results []Package
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		pkg, err := unmarshalPackage(data)
		if err != nil {
			return nil, err
		}
		results = append(results, pkg)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Name < results[j].Name
	})
	return results, nil
}

func unmarshalPackage(data string) (Package, error) {
	var pkg Package
	if err := json.Unmarshal([]byte(data), &pkg); err != nil {
		return Package{}, fmt.Errorf("unmarshal package: %w", err)
	}
	return pkg, nil
}
