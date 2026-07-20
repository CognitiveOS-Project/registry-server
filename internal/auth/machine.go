package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"golang.org/x/crypto/ssh"
)

type MachineProfile struct {
	MachineID   string          `json:"machine_id"`
	Hardware    HardwareProfile `json:"hardware"`
	Software    SoftwareProfile `json:"software"`
	Network     NetworkProfile  `json:"network"`
	Owner       OwnerProfile    `json:"owner"`
	SubmittedAt time.Time       `json:"submitted_at"`
}

type HardwareProfile struct {
	CPU      string `json:"cpu,omitempty"`
	Cores    int    `json:"cores,omitempty"`
	Arch     string `json:"arch,omitempty"`
	RAMMB    int    `json:"ram_mb,omitempty"`
	StorageMB int   `json:"storage_mb,omitempty"`
	GPU      string `json:"gpu,omitempty"`
	TPM      bool   `json:"tpm,omitempty"`
	MachineID string `json:"machine_id,omitempty"`
}

type SoftwareProfile struct {
	OS         string `json:"os,omitempty"`
	Kernel     string `json:"kernel,omitempty"`
	Distro     string `json:"distro,omitempty"`
	CPMVersion string `json:"cpm_version,omitempty"`
	Packages   int    `json:"packages,omitempty"`
	Services   int    `json:"services,omitempty"`
}

type NetworkProfile struct {
	IP string `json:"ip,omitempty"`
}

type OwnerProfile struct {
	PublicKey   string `json:"public_key"`
	Fingerprint string `json:"fingerprint"`
	Comment     string `json:"comment,omitempty"`
}

type MachineSignupStatus struct {
	MachineID   string    `json:"machine_id"`
	Status      string    `json:"status"` // approved, pending, rejected
	SubmittedAt time.Time `json:"submitted_at"`
	ReviewedAt  time.Time `json:"reviewed_at,omitempty"`
}

type MachineKeyStore interface {
	SaveProfile(profile *MachineProfile) error
	GetProfile(machineID string) (*MachineProfile, error)
	SaveStatus(status *MachineSignupStatus) error
	GetStatus(machineID string) (*MachineSignupStatus, error)
}

type MemoryMachineKeyStore struct {
	mu       sync.RWMutex
	profiles map[string]*MachineProfile
	statuses map[string]*MachineSignupStatus
}

func NewMemoryMachineKeyStore() *MemoryMachineKeyStore {
	return &MemoryMachineKeyStore{
		profiles: make(map[string]*MachineProfile),
		statuses: make(map[string]*MachineSignupStatus),
	}
}

func (s *MemoryMachineKeyStore) SaveProfile(profile *MachineProfile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.profiles[profile.MachineID] = profile
	return nil
}

func (s *MemoryMachineKeyStore) GetProfile(machineID string) (*MachineProfile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.profiles[machineID]
	if !ok {
		return nil, fmt.Errorf("machine not found: %s", machineID)
	}
	return p, nil
}

func (s *MemoryMachineKeyStore) SaveStatus(status *MachineSignupStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statuses[status.MachineID] = status
	return nil
}

func (s *MemoryMachineKeyStore) GetStatus(machineID string) (*MachineSignupStatus, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.statuses[machineID]
	if !ok {
		return nil, fmt.Errorf("machine not found: %s", machineID)
	}
	return st, nil
}

func MachineIDFromPublicKey(pubKey ssh.PublicKey) string {
	hash := sha256.Sum256(pubKey.Marshal())
	return base64.RawStdEncoding.EncodeToString(hash[:])
}

type S3MachineKeyStore struct {
	s3Client *s3.Client
	bucket   string
	prefix   string
	memory   *MemoryMachineKeyStore
}

func NewS3MachineKeyStore(s3Client *s3.Client, bucket, prefix string) *S3MachineKeyStore {
	if prefix == "" {
		prefix = "auth/machines"
	}
	return &S3MachineKeyStore{
		s3Client: s3Client,
		bucket:   bucket,
		prefix:   prefix,
		memory:   NewMemoryMachineKeyStore(),
	}
}

func (s *S3MachineKeyStore) SaveProfile(profile *MachineProfile) error {
	if err := s.memory.SaveProfile(profile); err != nil {
		return err
	}

	if s.s3Client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		data, err := json.Marshal(profile)
		if err != nil {
			return fmt.Errorf("marshal profile: %w", err)
		}

		key := s.prefix + "/" + profile.MachineID + "/profile.json"
		_, err = s.s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket:      aws.String(s.bucket),
			Key:         aws.String(key),
			Body:        strings.NewReader(string(data)),
			ContentType: aws.String("application/json"),
		})
		if err != nil {
			log.Printf("S3: failed to store profile for %s: %v", profile.MachineID, err)
		}
	}

	return nil
}

func (s *S3MachineKeyStore) GetProfile(machineID string) (*MachineProfile, error) {
	if p, err := s.memory.GetProfile(machineID); err == nil {
		return p, nil
	}

	if s.s3Client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		key := s.prefix + "/" + machineID + "/profile.json"
		resp, err := s.s3Client.GetObject(ctx, &s3.GetObjectInput{
			Bucket: aws.String(s.bucket),
			Key:    aws.String(key),
		})
		if err != nil {
			return nil, fmt.Errorf("machine not found: %s", machineID)
		}
		defer resp.Body.Close()

		var profile MachineProfile
		if err := json.NewDecoder(resp.Body).Decode(&profile); err != nil {
			return nil, fmt.Errorf("decode profile: %w", err)
		}

		_ = s.memory.SaveProfile(&profile)
		return &profile, nil
	}

	return nil, fmt.Errorf("machine not found: %s", machineID)
}

func (s *S3MachineKeyStore) SaveStatus(status *MachineSignupStatus) error {
	if err := s.memory.SaveStatus(status); err != nil {
		return err
	}

	if s.s3Client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		data, err := json.Marshal(status)
		if err != nil {
			return fmt.Errorf("marshal status: %w", err)
		}

		key := s.prefix + "/" + status.MachineID + "/status.json"
		_, err = s.s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket:      aws.String(s.bucket),
			Key:         aws.String(key),
			Body:        strings.NewReader(string(data)),
			ContentType: aws.String("application/json"),
		})
		if err != nil {
			log.Printf("S3: failed to store status for %s: %v", status.MachineID, err)
		}
	}

	return nil
}

func (s *S3MachineKeyStore) GetStatus(machineID string) (*MachineSignupStatus, error) {
	if st, err := s.memory.GetStatus(machineID); err == nil {
		return st, nil
	}

	if s.s3Client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		key := s.prefix + "/" + machineID + "/status.json"
		resp, err := s.s3Client.GetObject(ctx, &s3.GetObjectInput{
			Bucket: aws.String(s.bucket),
			Key:    aws.String(key),
		})
		if err != nil {
			return nil, fmt.Errorf("machine not found: %s", machineID)
		}
		defer resp.Body.Close()

		var status MachineSignupStatus
		if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
			return nil, fmt.Errorf("decode status: %w", err)
		}

		_ = s.memory.SaveStatus(&status)
		return &status, nil
	}

	return nil, fmt.Errorf("machine not found: %s", machineID)
}
