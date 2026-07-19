package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type S3Store struct {
	client     *s3.Client
	bucket     string
	prefix     string
	mu         sync.RWMutex
	cache      map[string]Package
	loaded     bool
	loadedAt   time.Time
}

type S3Config struct {
	Endpoint  string
	Bucket    string
	AccessKey string
	SecretKey string
	Region    string
	Prefix    string
}

func NewS3Store(cfg S3Config) (*S3Store, error) {
	if cfg.Bucket == "" {
		cfg.Bucket = "cognitiveos-registry"
	}
	if cfg.Region == "" {
		cfg.Region = "auto"
	}
	if cfg.Prefix == "" {
		cfg.Prefix = "packages"
	}

	awsCfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion(cfg.Region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.AccessKey, cfg.SecretKey, "",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
			o.UsePathStyle = true
		}
	})

	store := &S3Store{
		client: client,
		bucket: cfg.Bucket,
		prefix: cfg.Prefix,
		cache:  make(map[string]Package),
	}

	if err := store.refresh(); err != nil {
		log.Printf("S3: initial load failed (will retry): %v", err)
	}

	go store.refreshLoop()

	return store, nil
}

func (s *S3Store) objectKey(name, version string) string {
	return fmt.Sprintf("%s/%s/%s.json", s.prefix, name, version)
}

func (s *S3Store) refresh() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(s.prefix + "/"),
	})
	if err != nil {
		return fmt.Errorf("S3 list: %w", err)
	}

	newCache := make(map[string]Package)
	for _, obj := range resp.Contents {
		if obj.Key == nil || strings.HasSuffix(*obj.Key, "/") {
			continue
		}
		pkg, err := s.getObject(ctx, *obj.Key)
		if err != nil {
			log.Printf("S3: failed to load %s: %v", *obj.Key, err)
			continue
		}
		newCache[key(pkg.Name, pkg.Version)] = pkg
	}

	s.mu.Lock()
	s.cache = newCache
	s.loaded = true
	s.loadedAt = time.Now()
	s.mu.Unlock()

	return nil
}

func (s *S3Store) getObject(ctx context.Context, objectKey string) (Package, error) {
	resp, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(objectKey),
	})
	if err != nil {
		return Package{}, fmt.Errorf("S3 get %s: %w", objectKey, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return Package{}, fmt.Errorf("S3 read %s: %w", objectKey, err)
	}

	var pkg Package
	if err := json.Unmarshal(data, &pkg); err != nil {
		return Package{}, fmt.Errorf("S3 unmarshal %s: %w", objectKey, err)
	}
	return pkg, nil
}

func (s *S3Store) putObject(ctx context.Context, objectKey string, pkg Package) error {
	data, err := json.MarshalIndent(pkg, "", "  ")
	if err != nil {
		return fmt.Errorf("S3 marshal: %w", err)
	}

	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(objectKey),
		Body:        bytes.NewReader(data),
		ContentType: aws.String("application/json"),
	})
	if err != nil {
		return fmt.Errorf("S3 put %s: %w", objectKey, err)
	}
	return nil
}

func (s *S3Store) deleteObject(ctx context.Context, objectKey string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(objectKey),
	})
	if err != nil {
		return fmt.Errorf("S3 delete %s: %w", objectKey, err)
	}
	return nil
}

func (s *S3Store) refreshLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		if err := s.refresh(); err != nil {
			log.Printf("S3: refresh failed: %v", err)
		}
	}
}

func (s *S3Store) Search(query string) ([]Package, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	q := strings.ToLower(query)
	var results []Package

	for _, pkg := range s.cache {
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

func (s *S3Store) SearchFiltered(query string, opts SearchOptions) ([]Package, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	q := strings.ToLower(query)
	var matched []Package

	for _, pkg := range s.cache {
		if q != "" {
			nameMatch := strings.Contains(strings.ToLower(pkg.Name), q)
			descMatch := strings.Contains(strings.ToLower(pkg.Description), q)
			tagMatch := false
			for _, tag := range pkg.Tags {
				if strings.Contains(strings.ToLower(tag), q) {
					tagMatch = true
					break
				}
			}
			if opts.Exact {
				if !nameMatch {
					continue
				}
			} else {
				if !nameMatch && !descMatch && !tagMatch {
					continue
				}
			}
		}

		if opts.License != "" && !strings.EqualFold(pkg.License, opts.License) {
			continue
		}
		if opts.Author != "" && !strings.Contains(strings.ToLower(pkg.Author), strings.ToLower(opts.Author)) {
			continue
		}
		if opts.Capability != "" {
			found := false
			for _, c := range pkg.Capabilities {
				if strings.EqualFold(c, opts.Capability) {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		if opts.MinRAM > 0 && pkg.Hardware != nil && pkg.Hardware.MinRAMMB < opts.MinRAM {
			continue
		}

		matched = append(matched, pkg)
	}

	sort.Slice(matched, func(i, j int) bool {
		return matched[i].Name < matched[j].Name
	})

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

func (s *S3Store) Get(name, version string) (Package, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	pkg, ok := s.cache[key(name, version)]
	if !ok {
		return Package{}, ErrNotFound
	}
	return pkg, nil
}

func (s *S3Store) Put(pkg Package) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	now := time.Now().UTC().Format(time.RFC3339)

	s.mu.RLock()
	existing, exists := s.cache[key(pkg.Name, pkg.Version)]
	s.mu.RUnlock()

	if exists {
		pkg.CreatedAt = existing.CreatedAt
	} else {
		pkg.CreatedAt = now
		if pkg.Status == "" {
			pkg.Status = "active"
		}
	}
	pkg.UpdatedAt = now

	if err := s.putObject(ctx, s.objectKey(pkg.Name, pkg.Version), pkg); err != nil {
		return err
	}

	s.mu.Lock()
	s.cache[key(pkg.Name, pkg.Version)] = pkg
	s.mu.Unlock()

	return nil
}

func (s *S3Store) Delete(name, version string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	s.mu.RLock()
	_, exists := s.cache[key(name, version)]
	s.mu.RUnlock()

	if !exists {
		return ErrNotFound
	}

	if err := s.deleteObject(ctx, s.objectKey(name, version)); err != nil {
		return err
	}

	s.mu.Lock()
	delete(s.cache, key(name, version))
	s.mu.Unlock()

	return nil
}

func (s *S3Store) List() ([]Package, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Package, 0, len(s.cache))
	for _, pkg := range s.cache {
		result = append(result, pkg)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result, nil
}

func (s *S3Store) Versions(name string) ([]Package, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []Package
	for _, pkg := range s.cache {
		if pkg.Name == name {
			results = append(results, pkg)
		}
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Version > results[j].Version
	})
	return results, nil
}

func (s *S3Store) IncrementDownloads(name, version string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	k := key(name, version)
	pkg, ok := s.cache[k]
	if !ok {
		return 0, ErrNotFound
	}
	pkg.Downloads++
	pkg.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	s.cache[k] = pkg

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.putObject(ctx, s.objectKey(name, version), pkg)
	}()

	return pkg.Downloads, nil
}

func (s *S3Store) SetStatus(name, version, status string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	s.mu.Lock()
	defer s.mu.Unlock()

	k := key(name, version)
	pkg, ok := s.cache[k]
	if !ok {
		return ErrNotFound
	}
	pkg.Status = status
	pkg.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	s.cache[k] = pkg

	return s.putObject(ctx, s.objectKey(name, version), pkg)
}
