package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"golang.org/x/crypto/ssh"
)

type SSHKeyStore interface {
	Register(publicKey string) (*SSHKeyInfo, error)
	GetByFingerprint(fingerprint string) (*SSHKeyInfo, error)
	VerifySignature(fingerprint, signature string, data []byte) error
	ListKeys() ([]SSHKeyInfo, error)
	Delete(fingerprint string) error
}

type SSHKeyInfo struct {
	Fingerprint string    `json:"fingerprint"`
	PublicKey   string    `json:"public_key"`
	KeyType     string    `json:"key_type"`
	Comment     string    `json:"comment,omitempty"`
	Registered  time.Time `json:"registered_at"`
	Scope       string    `json:"scope,omitempty"`
}

type MemorySSHKeyStore struct {
	mu   sync.RWMutex
	keys map[string]*SSHKeyInfo
	pubs map[string]ssh.PublicKey
}

func NewMemorySSHKeyStore() *MemorySSHKeyStore {
	return &MemorySSHKeyStore{
		keys: make(map[string]*SSHKeyInfo),
		pubs: make(map[string]ssh.PublicKey),
	}
}

func Fingerprint(pubKey ssh.PublicKey) string {
	hash := sha256.Sum256(pubKey.Marshal())
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(hash[:])
}

func (s *MemorySSHKeyStore) Register(publicKey string) (*SSHKeyInfo, error) {
	pubKeyObj, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(strings.TrimSpace(publicKey)))
	if err != nil {
		return nil, fmt.Errorf("invalid public key: %w", err)
	}

	fp := Fingerprint(pubKeyObj)

	info := &SSHKeyInfo{
		Fingerprint: fp,
		PublicKey:   strings.TrimSpace(publicKey),
		KeyType:     pubKeyObj.Type(),
		Comment:     comment,
		Registered:  time.Now().UTC(),
		Scope:       "publish",
	}

	s.mu.Lock()
	s.keys[fp] = info
	s.pubs[fp] = pubKeyObj
	s.mu.Unlock()

	log.Printf("Auth: registered key %s (%s %s)", fp, pubKeyObj.Type(), comment)
	return info, nil
}

func (s *MemorySSHKeyStore) GetByFingerprint(fingerprint string) (*SSHKeyInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	info, ok := s.keys[fingerprint]
	if !ok {
		return nil, fmt.Errorf("key not found: %s", fingerprint)
	}
	return info, nil
}

func (s *MemorySSHKeyStore) VerifySignature(fingerprint, signatureB64 string, data []byte) error {
	s.mu.RLock()
	pubKey, ok := s.pubs[fingerprint]
	s.mu.RUnlock()

	if !ok {
		return fmt.Errorf("key not found: %s", fingerprint)
	}

	sigBytes, err := base64.RawStdEncoding.DecodeString(signatureB64)
	if err != nil {
		return fmt.Errorf("invalid signature encoding: %w", err)
	}

	format, rest := ReadSSHString(sigBytes)
	blob, _ := ReadSSHString(rest)

	sig := &ssh.Signature{
		Format: string(format),
		Blob:   blob,
	}

	if err := pubKey.Verify(data, sig); err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}

	return nil
}

func ReadSSHString(data []byte) ([]byte, []byte) {
	if len(data) < 4 {
		return nil, nil
	}
	length := binary.BigEndian.Uint32(data[:4])
	if int(length)+4 > len(data) {
		return nil, nil
	}
	return data[4 : 4+length], data[4+length:]
}

func (s *MemorySSHKeyStore) ListKeys() ([]SSHKeyInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []SSHKeyInfo
	for _, info := range s.keys {
		result = append(result, *info)
	}
	return result, nil
}

func (s *MemorySSHKeyStore) Delete(fingerprint string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.keys[fingerprint]; !ok {
		return fmt.Errorf("key not found: %s", fingerprint)
	}
	delete(s.keys, fingerprint)
	delete(s.pubs, fingerprint)
	return nil
}

type S3SSHKeyStore struct {
	s3Client *s3.Client
	bucket   string
	prefix   string
	memory   *MemorySSHKeyStore
}

func NewS3SSHKeyStore(s3Client *s3.Client, bucket, prefix string) *S3SSHKeyStore {
	if prefix == "" {
		prefix = "auth/keys"
	}
	return &S3SSHKeyStore{
		s3Client: s3Client,
		bucket:   bucket,
		prefix:   prefix,
		memory:   NewMemorySSHKeyStore(),
	}
}

func (s *S3SSHKeyStore) Register(publicKey string) (*SSHKeyInfo, error) {
	info, err := s.memory.Register(publicKey)
	if err != nil {
		return nil, err
	}

	if s.s3Client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		key := s.prefix + "/" + info.Fingerprint + ".pub"
		_, err = s.s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket:      aws.String(s.bucket),
			Key:         aws.String(key),
			Body:        strings.NewReader(info.PublicKey),
			ContentType: aws.String("text/plain"),
		})
		if err != nil {
			log.Printf("S3: failed to store key %s: %v", info.Fingerprint, err)
		}
	}

	return info, nil
}

func (s *S3SSHKeyStore) GetByFingerprint(fp string) (*SSHKeyInfo, error) {
	if info, err := s.memory.GetByFingerprint(fp); err == nil {
		return info, nil
	}

	if s.s3Client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		key := s.prefix + "/" + fp + ".pub"
		resp, err := s.s3Client.GetObject(ctx, &s3.GetObjectInput{
			Bucket: aws.String(s.bucket),
			Key:    aws.String(key),
		})
		if err != nil {
			return nil, fmt.Errorf("key not found: %s", fp)
		}
		defer resp.Body.Close()

		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read key: %w", err)
		}

		info, err := s.memory.Register(string(data))
		if err != nil {
			return nil, err
		}
		info.Fingerprint = fp
		return info, nil
	}

	return nil, fmt.Errorf("key not found: %s", fp)
}

func (s *S3SSHKeyStore) VerifySignature(fp, signature string, data []byte) error {
	if _, err := s.GetByFingerprint(fp); err != nil {
		return err
	}
	return s.memory.VerifySignature(fp, signature, data)
}

func (s *S3SSHKeyStore) ListKeys() ([]SSHKeyInfo, error) {
	return s.memory.ListKeys()
}

func (s *S3SSHKeyStore) Delete(fp string) error {
	return s.memory.Delete(fp)
}
