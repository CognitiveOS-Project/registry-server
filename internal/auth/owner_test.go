package auth

import (
	"testing"
	"time"
)

func TestMemoryOwnerStore_SaveAndGetByGitHubID(t *testing.T) {
	s := NewMemoryOwnerStore()

	owner := &Owner{
		GitHubID:   12345,
		GitHubUser: "octocat",
		AvatarURL:  "https://avatars.githubusercontent.com/u/12345",
		Keys:       []OwnerKey{},
	}

	if err := s.Save(owner); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := s.GetByGitHubID(12345)
	if err != nil {
		t.Fatalf("GetByGitHubID: %v", err)
	}

	if got.GitHubUser != "octocat" {
		t.Errorf("GitHubUser = %q, want %q", got.GitHubUser, "octocat")
	}

	if got.AvatarURL != "https://avatars.githubusercontent.com/u/12345" {
		t.Errorf("AvatarURL = %q, want %q", got.AvatarURL, "https://avatars.githubusercontent.com/u/12345")
	}
}

func TestMemoryOwnerStore_GetByGitHubID_NotFound(t *testing.T) {
	s := NewMemoryOwnerStore()

	_, err := s.GetByGitHubID(99999)
	if err == nil {
		t.Error("expected error for missing owner")
	}
}

func TestMemoryOwnerStore_AddKey(t *testing.T) {
	s := NewMemoryOwnerStore()

	owner := &Owner{
		GitHubID:   100,
		GitHubUser: "testuser",
		Keys:       []OwnerKey{},
	}
	_ = s.Save(owner)

	key := OwnerKey{
		Fingerprint: "SHA256:abc123",
		PublicKey:   "ssh-ed25519 AAAA... test@machine",
		DisplayName: "Living Room RPi",
	}

	if err := s.AddKey(100, key); err != nil {
		t.Fatalf("AddKey: %v", err)
	}

	got, gotKey, err := s.GetByKey("SHA256:abc123")
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}

	if got.GitHubID != 100 {
		t.Errorf("owner GitHubID = %d, want 100", got.GitHubID)
	}

	if gotKey.DisplayName != "Living Room RPi" {
		t.Errorf("DisplayName = %q, want %q", gotKey.DisplayName, "Living Room RPi")
	}

	if gotKey.Status != "active" {
		t.Errorf("Status = %q, want %q", gotKey.Status, "active")
	}

	if gotKey.AddedAt.IsZero() {
		t.Error("AddedAt should be set")
	}
}

func TestMemoryOwnerStore_AddKey_Duplicate(t *testing.T) {
	s := NewMemoryOwnerStore()

	owner := &Owner{
		GitHubID: 100,
		Keys:     []OwnerKey{},
	}
	_ = s.Save(owner)

	key := OwnerKey{
		Fingerprint: "SHA256:abc123",
		PublicKey:   "ssh-ed25519 AAAA... test@machine",
		DisplayName: "Key 1",
	}

	if err := s.AddKey(100, key); err != nil {
		t.Fatalf("AddKey: %v", err)
	}

	key.DisplayName = "Key 2"
	if err := s.AddKey(100, key); err == nil {
		t.Error("expected error for duplicate key")
	}
}

func TestMemoryOwnerStore_AddKey_OwnerNotFound(t *testing.T) {
	s := NewMemoryOwnerStore()

	key := OwnerKey{
		Fingerprint: "SHA256:abc123",
		PublicKey:   "ssh-ed25519 AAAA... test@machine",
		DisplayName: "Key 1",
	}

	if err := s.AddKey(99999, key); err == nil {
		t.Error("expected error for missing owner")
	}
}

func TestMemoryOwnerStore_RemoveKey(t *testing.T) {
	s := NewMemoryOwnerStore()

	owner := &Owner{
		GitHubID: 100,
		Keys:     []OwnerKey{},
	}
	_ = s.Save(owner)

	key := OwnerKey{
		Fingerprint: "SHA256:abc123",
		PublicKey:   "ssh-ed25519 AAAA... test@machine",
		DisplayName: "Key 1",
	}
	_ = s.AddKey(100, key)

	if err := s.RemoveKey(100, "SHA256:abc123"); err != nil {
		t.Fatalf("RemoveKey: %v", err)
	}

	_, _, err := s.GetByKey("SHA256:abc123")
	if err == nil {
		t.Error("expected error after removal")
	}
}

func TestMemoryOwnerStore_SetKeyStatus(t *testing.T) {
	s := NewMemoryOwnerStore()

	owner := &Owner{
		GitHubID: 100,
		Keys:     []OwnerKey{},
	}
	_ = s.Save(owner)

	key := OwnerKey{
		Fingerprint: "SHA256:abc123",
		PublicKey:   "ssh-ed25519 AAAA... test@machine",
		DisplayName: "Key 1",
	}
	_ = s.AddKey(100, key)

	if err := s.SetKeyStatus(100, "SHA256:abc123", "revoked"); err != nil {
		t.Fatalf("SetKeyStatus: %v", err)
	}

	_, gotKey, err := s.GetByKey("SHA256:abc123")
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}

	if gotKey.Status != "revoked" {
		t.Errorf("Status = %q, want %q", gotKey.Status, "revoked")
	}
}

func TestMemoryOwnerStore_SetKeyStatus_WrongOwner(t *testing.T) {
	s := NewMemoryOwnerStore()

	owner1 := &Owner{GitHubID: 100, Keys: []OwnerKey{}}
	owner2 := &Owner{GitHubID: 200, Keys: []OwnerKey{}}
	_ = s.Save(owner1)
	_ = s.Save(owner2)

	key := OwnerKey{
		Fingerprint: "SHA256:abc123",
		PublicKey:   "ssh-ed25519 AAAA... test@machine",
		DisplayName: "Key 1",
	}
	_ = s.AddKey(100, key)

	err := s.SetKeyStatus(200, "SHA256:abc123", "revoked")
	if err == nil {
		t.Error("expected error when wrong owner tries to set status")
	}
}

func TestS3OwnerStore_MemoryFallback(t *testing.T) {
	s := NewS3OwnerStore(nil, "test-bucket", "auth/owners")

	owner := &Owner{
		GitHubID:   200,
		GitHubUser: "s3user",
		AvatarURL:  "https://example.com/avatar.png",
		Keys:       []OwnerKey{},
	}

	if err := s.Save(owner); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := s.GetByGitHubID(200)
	if err != nil {
		t.Fatalf("GetByGitHubID: %v", err)
	}

	if got.GitHubUser != "s3user" {
		t.Errorf("GitHubUser = %q, want %q", got.GitHubUser, "s3user")
	}
}

func TestOwnerKey_DisplayName(t *testing.T) {
	key := OwnerKey{
		Fingerprint: "SHA256:abc123",
		PublicKey:   "ssh-ed25519 AAAA... test@machine",
		DisplayName: "Living Room RPi",
		AddedAt:     time.Now().UTC(),
		Status:      "active",
	}

	if key.DisplayName != "Living Room RPi" {
		t.Errorf("DisplayName = %q, want %q", key.DisplayName, "Living Room RPi")
	}
}
