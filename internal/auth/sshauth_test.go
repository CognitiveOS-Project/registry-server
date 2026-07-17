package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestSSHKeyRegister(t *testing.T) {
	store := NewMemorySSHKeyStore()

	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	sshPub, _ := ssh.NewPublicKey(pub)
	pubBytes := ssh.MarshalAuthorizedKey(sshPub)

	info, err := store.Register(string(pubBytes))
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	if info.Fingerprint == "" {
		t.Error("expected non-empty fingerprint")
	}
	if info.KeyType != "ssh-ed25519" {
		t.Errorf("expected ssh-ed25519, got %s", info.KeyType)
	}
}

func TestSSHKeyGetByFingerprint(t *testing.T) {
	store := NewMemorySSHKeyStore()

	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	sshPub, _ := ssh.NewPublicKey(pub)
	pubBytes := ssh.MarshalAuthorizedKey(sshPub)

	info, _ := store.Register(string(pubBytes))

	got, err := store.GetByFingerprint(info.Fingerprint)
	if err != nil {
		t.Fatalf("GetByFingerprint failed: %v", err)
	}
	if got.Fingerprint != info.Fingerprint {
		t.Errorf("expected %s, got %s", info.Fingerprint, got.Fingerprint)
	}
}

func TestSSHKeyGetNotFound(t *testing.T) {
	store := NewMemorySSHKeyStore()
	_, err := store.GetByFingerprint("SHA256:nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent key")
	}
}

func TestSSHKeyVerifySignature(t *testing.T) {
	store := NewMemorySSHKeyStore()

	signer, _ := generateSigner()
	sshPub := signer.PublicKey()
	pubBytes := ssh.MarshalAuthorizedKey(sshPub)

	info, _ := store.Register(string(pubBytes))

	data := []byte("test data to sign")
	sig, _ := signer.Sign(rand.Reader, data)

	// Marshal to SSH wire format: string(format) + string(blob)
	wireSig := marshalSSHSignature(sig.Format, sig.Blob)
	b64Sig := base64.RawStdEncoding.EncodeToString(wireSig)

	err := store.VerifySignature(info.Fingerprint, b64Sig, data)
	if err != nil {
		t.Fatalf("VerifySignature failed: %v", err)
	}
}

func TestSSHKeyVerifySignatureBadSig(t *testing.T) {
	store := NewMemorySSHKeyStore()

	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	sshPub, _ := ssh.NewPublicKey(pub)
	pubBytes := ssh.MarshalAuthorizedKey(sshPub)

	info, _ := store.Register(string(pubBytes))

	data := []byte("test data")
	badSig := base64.RawStdEncoding.EncodeToString([]byte("not-a-valid-sig"))

	err := store.VerifySignature(info.Fingerprint, badSig, data)
	if err == nil {
		t.Error("expected error for bad signature")
	}
}

func TestSSHKeyDelete(t *testing.T) {
	store := NewMemorySSHKeyStore()

	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	sshPub, _ := ssh.NewPublicKey(pub)
	pubBytes := ssh.MarshalAuthorizedKey(sshPub)

	info, _ := store.Register(string(pubBytes))

	err := store.Delete(info.Fingerprint)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	_, err = store.GetByFingerprint(info.Fingerprint)
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestSSHKeyList(t *testing.T) {
	store := NewMemorySSHKeyStore()

	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	sshPub, _ := ssh.NewPublicKey(pub)
	pubBytes := ssh.MarshalAuthorizedKey(sshPub)
	store.Register(string(pubBytes))

	pub2, _, _ := ed25519.GenerateKey(rand.Reader)
	sshPub2, _ := ssh.NewPublicKey(pub2)
	pubBytes2 := ssh.MarshalAuthorizedKey(sshPub2)
	store.Register(string(pubBytes2))

	keys, _ := store.ListKeys()
	if len(keys) != 2 {
		t.Errorf("expected 2 keys, got %d", len(keys))
	}
}

func marshalSSHSignature(format string, blob []byte) []byte {
	var buf []byte
	formatBytes := []byte(format)
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(formatBytes)))
	buf = append(buf, lenBuf[:]...)
	buf = append(buf, formatBytes...)
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(blob)))
	buf = append(buf, lenBuf[:]...)
	buf = append(buf, blob...)
	return buf
}

func generateSigner() (ssh.Signer, error) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	return ssh.NewSignerFromKey(priv)
}
