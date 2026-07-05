package store

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type Package struct {
	Name             string   `json:"name"`
	Version          string   `json:"version"`
	Description      string   `json:"description,omitempty"`
	Author           string   `json:"author,omitempty"`
	License          string   `json:"license,omitempty"`
	SourceRepository string   `json:"source_repository,omitempty"`
	SourceIssues     string   `json:"source_issues,omitempty"`
	DownloadURL      string   `json:"download_url,omitempty"`
	Size             int64    `json:"size,omitempty"`
	SHA256           string   `json:"sha256,omitempty"`
	Status           string   `json:"status,omitempty"`
	Downloads        int64    `json:"downloads,omitempty"`
	CreatedAt        string   `json:"created_at,omitempty"`
	UpdatedAt        string   `json:"updated_at,omitempty"`
	Tags             []string `json:"tags,omitempty"`
	Manifest         string   `json:"manifest,omitempty"`
}

type Store interface {
	Search(query string) ([]Package, error)
	Get(name, version string) (Package, error)
	Put(pkg Package) error
	Delete(name, version string) error
	List() ([]Package, error)
	Versions(name string) ([]Package, error)
	IncrementDownloads(name, version string) (int64, error)
	SetStatus(name, version, status string) error
}

func key(name, version string) string {
	return name + "@" + version
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
		if pkg.Status == "" {
			pkg.Status = "active"
		}
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

func (s *MemoryStore) Versions(name string) ([]Package, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []Package
	for _, pkg := range s.data {
		if pkg.Name == name {
			results = append(results, pkg)
		}
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Version > results[j].Version
	})
	return results, nil
}

func (s *MemoryStore) IncrementDownloads(name, version string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	k := key(name, version)
	pkg, ok := s.data[k]
	if !ok {
		return 0, ErrNotFound
	}
	pkg.Downloads++
	s.data[k] = pkg
	return pkg.Downloads, nil
}

func (s *MemoryStore) SetStatus(name, version, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	k := key(name, version)
	pkg, ok := s.data[k]
	if !ok {
		return ErrNotFound
	}
	pkg.Status = status
	pkg.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	s.data[k] = pkg
	return nil
}

type FileStore struct {
	MemoryStore
	path string
}

func NewFileStore(path string) *FileStore {
	fs := &FileStore{
		MemoryStore: MemoryStore{
			data: make(map[string]Package),
		},
		path: path,
	}
	fs.load()
	return fs
}

func (fs *FileStore) load() {
	data, err := os.ReadFile(fs.path)
	if err != nil {
		return
	}
	var pkgs []Package
	if err := json.Unmarshal(data, &pkgs); err != nil {
		return
	}
	for _, pkg := range pkgs {
		fs.data[key(pkg.Name, pkg.Version)] = pkg
	}
}

func (fs *FileStore) save() error {
	fs.mu.RLock()
	pkgs := make([]Package, 0, len(fs.data))
	for _, pkg := range fs.data {
		pkgs = append(pkgs, pkg)
	}
	fs.mu.RUnlock()

	sort.Slice(pkgs, func(i, j int) bool {
		if pkgs[i].Name != pkgs[j].Name {
			return pkgs[i].Name < pkgs[j].Name
		}
		return pkgs[i].Version < pkgs[j].Version
	})

	data, err := json.MarshalIndent(pkgs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(fs.path, data, 0644)
}

func (fs *FileStore) Put(pkg Package) error {
	if err := fs.MemoryStore.Put(pkg); err != nil {
		return err
	}
	return fs.save()
}

func (fs *FileStore) Delete(name, version string) error {
	if err := fs.MemoryStore.Delete(name, version); err != nil {
		return err
	}
	return fs.save()
}

func (fs *FileStore) SetStatus(name, version, status string) error {
	if err := fs.MemoryStore.SetStatus(name, version, status); err != nil {
		return err
	}
	return fs.save()
}
