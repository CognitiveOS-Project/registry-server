package store

import (
	"sort"
	"strings"
	"sync"
	"time"
)

type Package struct {
	Name             string   `json:"name"`
	Version          string   `json:"version"`
	Description      string   `json:"description"`
	Author           string   `json:"author"`
	SourceRepository string   `json:"source_repository,omitempty"`
	SourceIssues     string   `json:"source_issues,omitempty"`
	DownloadURL      string   `json:"download_url,omitempty"`
	Size             int64    `json:"size"`
	SHA256           string   `json:"sha256"`
	Downloads        int64    `json:"downloads"`
	CreatedAt        string   `json:"created_at"`
	UpdatedAt        string   `json:"updated_at"`
	Tags             []string `json:"tags"`
}

type Store interface {
	Search(query string) ([]Package, error)
	Get(name, version string) (Package, error)
	Put(pkg Package) error
	Delete(name, version string) error
	List() ([]Package, error)
}

type MemoryStore struct {
	mu   sync.RWMutex
	data map[string]Package
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		data: make(map[string]Package),
	}
}

func key(name, version string) string {
	return name + "@" + version
}

func (s *MemoryStore) Search(query string) ([]Package, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	q := strings.ToLower(query)
	var results []Package

	for _, pkg := range s.data {
		if strings.Contains(strings.ToLower(pkg.Name), q) ||
			strings.Contains(strings.ToLower(pkg.Description), q) {
			results = append(results, pkg)
			continue
		}
		for _, tag := range pkg.Tags {
			if strings.Contains(strings.ToLower(tag), q) {
				results = append(results, pkg)
				break
			}
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Name < results[j].Name
	})
	return results, nil
}

func (s *MemoryStore) Get(name, version string) (Package, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	pkg, ok := s.data[key(name, version)]
	if !ok {
		return Package{}, ErrNotFound
	}
	return pkg, nil
}

func (s *MemoryStore) Put(pkg Package) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC().Format(time.RFC3339)
	if _, exists := s.data[key(pkg.Name, pkg.Version)]; exists {
		existing := s.data[key(pkg.Name, pkg.Version)]
		pkg.CreatedAt = existing.CreatedAt
	} else {
		pkg.CreatedAt = now
	}
	pkg.UpdatedAt = now

	s.data[key(pkg.Name, pkg.Version)] = pkg
	return nil
}

func (s *MemoryStore) Delete(name, version string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	k := key(name, version)
	if _, ok := s.data[k]; !ok {
		return ErrNotFound
	}
	delete(s.data, k)
	return nil
}

func (s *MemoryStore) List() ([]Package, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Package, 0, len(s.data))
	for _, pkg := range s.data {
		result = append(result, pkg)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result, nil
}
