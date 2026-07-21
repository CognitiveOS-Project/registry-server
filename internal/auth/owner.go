package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type Owner struct {
	GitHubID   int64      `json:"github_id"`
	GitHubUser string     `json:"github_user"`
	AvatarURL  string     `json:"avatar_url"`
	Keys       []OwnerKey `json:"keys"`
}

type OwnerKey struct {
	Fingerprint string    `json:"fingerprint"`
	PublicKey   string    `json:"public_key"`
	DisplayName string    `json:"display_name"`
	AddedAt     time.Time `json:"added_at"`
	Status      string    `json:"status"` // "active", "revoked"
}

type OwnerStore interface {
	Save(owner *Owner) error
	GetByGitHubID(gitHubID int64) (*Owner, error)
	GetByKey(fingerprint string) (*Owner, *OwnerKey, error)
	AddKey(gitHubID int64, key OwnerKey) error
	RemoveKey(gitHubID int64, fingerprint string) error
	SetKeyStatus(gitHubID int64, fingerprint string, status string) error
}

type MemoryOwnerStore struct {
	mu     sync.RWMutex
	owners map[int64]*Owner
ByKey   map[string]*ownerKeyRef
}

func NewMemoryOwnerStore() *MemoryOwnerStore {
	return &MemoryOwnerStore{
		owners: make(map[int64]*Owner),
		ByKey:  make(map[string]*ownerKeyRef),
	}
}

func (s *MemoryOwnerStore) Save(owner *Owner) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.owners[owner.GitHubID] = owner

	for i := range owner.Keys {
		s.ByKey[owner.Keys[i].Fingerprint] = &ownerKeyRef{
			owner: owner,
			key:   &owner.Keys[i],
		}
	}

	return nil
}

func (s *MemoryOwnerStore) GetByGitHubID(gitHubID int64) (*Owner, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	owner, ok := s.owners[gitHubID]
	if !ok {
		return nil, fmt.Errorf("owner not found: %d", gitHubID)
	}
	return owner, nil
}

func (s *MemoryOwnerStore) GetByKey(fingerprint string) (*Owner, *OwnerKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ref, ok := s.ByKey[fingerprint]
	if !ok {
		return nil, nil, fmt.Errorf("key not linked to any owner: %s", fingerprint)
	}
	return ref.owner, ref.key, nil
}

func (s *MemoryOwnerStore) AddKey(gitHubID int64, key OwnerKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	owner, ok := s.owners[gitHubID]
	if !ok {
		return fmt.Errorf("owner not found: %d", gitHubID)
	}

	for _, k := range owner.Keys {
		if k.Fingerprint == key.Fingerprint {
			return fmt.Errorf("key already linked: %s", key.Fingerprint)
		}
	}

	key.AddedAt = time.Now().UTC()
	if key.Status == "" {
		key.Status = "active"
	}

	owner.Keys = append(owner.Keys, key)
	s.ByKey[key.Fingerprint] = &ownerKeyRef{
		owner: owner,
		key:   &owner.Keys[len(owner.Keys)-1],
	}

	return nil
}

func (s *MemoryOwnerStore) RemoveKey(gitHubID int64, fingerprint string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	owner, ok := s.owners[gitHubID]
	if !ok {
		return fmt.Errorf("owner not found: %d", gitHubID)
	}

	for i, k := range owner.Keys {
		if k.Fingerprint == fingerprint {
			owner.Keys = append(owner.Keys[:i], owner.Keys[i+1:]...)
			delete(s.ByKey, fingerprint)
			return nil
		}
	}

	return fmt.Errorf("key not found: %s", fingerprint)
}

func (s *MemoryOwnerStore) SetKeyStatus(gitHubID int64, fingerprint string, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ref, ok := s.ByKey[fingerprint]
	if !ok || ref.owner.GitHubID != gitHubID {
		return fmt.Errorf("key not found: %s", fingerprint)
	}

	ref.key.Status = status
	return nil
}

type S3OwnerStore struct {
	s3Client *s3.Client
	bucket   string
	prefix   string
	memory   *MemoryOwnerStore
}

func NewS3OwnerStore(s3Client *s3.Client, bucket, prefix string) *S3OwnerStore {
	if prefix == "" {
		prefix = "auth/owners"
	}
	return &S3OwnerStore{
		s3Client: s3Client,
		bucket:   bucket,
		prefix:   prefix,
		memory:   NewMemoryOwnerStore(),
	}
}

func (s *S3OwnerStore) Save(owner *Owner) error {
	if err := s.memory.Save(owner); err != nil {
		return err
	}

	if s.s3Client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		data, err := json.Marshal(owner)
		if err != nil {
			return fmt.Errorf("marshal owner: %w", err)
		}

		key := fmt.Sprintf("%s/%d/owner.json", s.prefix, owner.GitHubID)
		_, err = s.s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket:      aws.String(s.bucket),
			Key:         aws.String(key),
			Body:        strings.NewReader(string(data)),
			ContentType: aws.String("application/json"),
		})
		if err != nil {
			log.Printf("S3: failed to store owner %d: %v", owner.GitHubID, err)
		}
	}

	return nil
}

func (s *S3OwnerStore) GetByGitHubID(gitHubID int64) (*Owner, error) {
	if owner, err := s.memory.GetByGitHubID(gitHubID); err == nil {
		return owner, nil
	}

	if s.s3Client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		key := fmt.Sprintf("%s/%d/owner.json", s.prefix, gitHubID)
		resp, err := s.s3Client.GetObject(ctx, &s3.GetObjectInput{
			Bucket: aws.String(s.bucket),
			Key:    aws.String(key),
		})
		if err != nil {
			return nil, fmt.Errorf("owner not found: %d", gitHubID)
		}
		defer resp.Body.Close()

		var owner Owner
		if err := json.NewDecoder(resp.Body).Decode(&owner); err != nil {
			return nil, fmt.Errorf("decode owner: %w", err)
		}

		_ = s.memory.Save(&owner)
		return &owner, nil
	}

	return nil, fmt.Errorf("owner not found: %d", gitHubID)
}

func (s *S3OwnerStore) GetByKey(fingerprint string) (*Owner, *OwnerKey, error) {
	return s.memory.GetByKey(fingerprint)
}

func (s *S3OwnerStore) AddKey(gitHubID int64, key OwnerKey) error {
	owner, err := s.GetByGitHubID(gitHubID)
	if err != nil {
		owner = &Owner{GitHubID: gitHubID}
	}

	if err := s.memory.AddKey(gitHubID, key); err != nil {
		return err
	}

	return s.Save(owner)
}

func (s *S3OwnerStore) RemoveKey(gitHubID int64, fingerprint string) error {
	if err := s.memory.RemoveKey(gitHubID, fingerprint); err != nil {
		return err
	}

	owner, err := s.memory.GetByGitHubID(gitHubID)
	if err != nil {
		return err
	}

	return s.Save(owner)
}

func (s *S3OwnerStore) SetKeyStatus(gitHubID int64, fingerprint string, status string) error {
	if err := s.memory.SetKeyStatus(gitHubID, fingerprint, status); err != nil {
		return err
	}

	owner, err := s.memory.GetByGitHubID(gitHubID)
	if err != nil {
		return err
	}

	return s.Save(owner)
}
