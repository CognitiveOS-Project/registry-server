package auth

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/crypto/ssh"
)

func generateTestSSHKey(t *testing.T) (ssh.Signer, string) {
	t.Helper()
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(privKey)
	if err != nil {
		t.Fatalf("failed to create signer: %v", err)
	}
	sshPubKey, err := ssh.NewPublicKey(pubKey)
	if err != nil {
		t.Fatalf("failed to create SSH public key: %v", err)
	}
	pubKeyStr := string(ssh.MarshalAuthorizedKey(sshPubKey))
	return signer, pubKeyStr
}

func signManifest(t *testing.T, signer ssh.Signer, manifest []byte) string {
	t.Helper()
	hash := sha256.Sum256(manifest)
	signature, err := signer.Sign(nil, hash[:])
	if err != nil {
		t.Fatalf("failed to sign: %v", err)
	}
	wireFormat := marshalSSHSig(signature)
	return base64.RawStdEncoding.EncodeToString(wireFormat)
}

func marshalSSHSig(sig *ssh.Signature) []byte {
	formatBytes := []byte(sig.Format)
	blobBytes := sig.Blob

	formatLen := make([]byte, 4)
	formatLen[0] = byte(len(formatBytes) >> 24)
	formatLen[1] = byte(len(formatBytes) >> 16)
	formatLen[2] = byte(len(formatBytes) >> 8)
	formatLen[3] = byte(len(formatBytes))

	blobLen := make([]byte, 4)
	blobLen[0] = byte(len(blobBytes) >> 24)
	blobLen[1] = byte(len(blobBytes) >> 16)
	blobLen[2] = byte(len(blobBytes) >> 8)
	blobLen[3] = byte(len(blobBytes))

	result := make([]byte, 0, 8+len(formatBytes)+len(blobBytes))
	result = append(result, formatLen...)
	result = append(result, formatBytes...)
	result = append(result, blobLen...)
	result = append(result, blobBytes...)
	return result
}

func TestRequireSSHAuthSuccess(t *testing.T) {
	signer, pubKey := generateTestSSHKey(t)
	store := NewMemorySSHKeyStore()
	info, err := store.Register(pubKey)
	if err != nil {
		t.Fatalf("failed to register key: %v", err)
	}

	manifest := []byte(`{"name":"test","version":"1.0.0"}`)
	body, _ := json.Marshal(map[string]interface{}{
		"name":     "test",
		"version":  "1.0.0",
		"manifest": json.RawMessage(manifest),
	})

	sig := signManifest(t, signer, manifest)

	handler := RequireSSHAuth(store)(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	req := httptest.NewRequest("POST", "/v1/patches", bytes.NewReader(body))
	req.Header.Set("X-SSH-Fingerprint", info.Fingerprint)
	req.Header.Set("X-SSH-Signature", sig)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRequireSSHAuthMissingHeaders(t *testing.T) {
	store := NewMemorySSHKeyStore()

	handler := RequireSSHAuth(store)(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	})

	body, _ := json.Marshal(map[string]interface{}{
		"name":    "test",
		"version": "1.0.0",
	})

	req := httptest.NewRequest("POST", "/v1/patches", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestRequireSSHAuthBadSignature(t *testing.T) {
	_, pubKey := generateTestSSHKey(t)
	store := NewMemorySSHKeyStore()
	info, err := store.Register(pubKey)
	if err != nil {
		t.Fatalf("failed to register key: %v", err)
	}

	body, _ := json.Marshal(map[string]interface{}{
		"name":    "test",
		"version": "1.0.0",
		"manifest": map[string]string{
			"name":    "test",
			"version": "1.0.0",
		},
	})

	handler := RequireSSHAuth(store)(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	})

	req := httptest.NewRequest("POST", "/v1/patches", bytes.NewReader(body))
	req.Header.Set("X-SSH-Fingerprint", info.Fingerprint)
	req.Header.Set("X-SSH-Signature", "b2JpdXNzaWduYXR1cmU=")
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestRequireSSHAuthBodyReReadable(t *testing.T) {
	signer, pubKey := generateTestSSHKey(t)
	store := NewMemorySSHKeyStore()
	info, err := store.Register(pubKey)
	if err != nil {
		t.Fatalf("failed to register key: %v", err)
	}

	manifest := []byte(`{"name":"test","version":"1.0.0"}`)
	body, _ := json.Marshal(map[string]interface{}{
		"name":     "test",
		"version":  "1.0.0",
		"manifest": json.RawMessage(manifest),
	})

	sig := signManifest(t, signer, manifest)

	handler := RequireSSHAuth(store)(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("failed to re-read body: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if len(bodyBytes) == 0 {
			t.Error("body is empty after middleware")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("POST", "/v1/patches", bytes.NewReader(body))
	req.Header.Set("X-SSH-Fingerprint", info.Fingerprint)
	req.Header.Set("X-SSH-Signature", sig)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestRequirePublishAuthSSHPath(t *testing.T) {
	signer, pubKey := generateTestSSHKey(t)
	tokenStore := NewMemoryTokenStore()
	sshStore := NewMemorySSHKeyStore()
	info, _ := sshStore.Register(pubKey)

	manifest := []byte(`{"name":"test","version":"1.0.0"}`)
	body, _ := json.Marshal(map[string]interface{}{
		"name":     "test",
		"version":  "1.0.0",
		"manifest": json.RawMessage(manifest),
	})

	sig := signManifest(t, signer, manifest)

	handler := RequirePublishAuth(tokenStore, sshStore, "publish")(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("POST", "/v1/patches", bytes.NewReader(body))
	req.Header.Set("X-SSH-Fingerprint", info.Fingerprint)
	req.Header.Set("X-SSH-Signature", sig)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRequirePublishAuthTokenFallback(t *testing.T) {
	tokenStore := NewMemoryTokenStore()
	tokenStore.Add("test-token", "publish")
	sshStore := NewMemorySSHKeyStore()

	body, _ := json.Marshal(map[string]interface{}{
		"name":    "test",
		"version": "1.0.0",
	})

	handler := RequirePublishAuth(tokenStore, sshStore, "publish")(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("POST", "/v1/patches", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRequirePublishAuthNoCredentials(t *testing.T) {
	tokenStore := NewMemoryTokenStore()
	sshStore := NewMemorySSHKeyStore()

	body, _ := json.Marshal(map[string]interface{}{
		"name":    "test",
		"version": "1.0.0",
	})

	handler := RequirePublishAuth(tokenStore, sshStore, "publish")(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	})

	req := httptest.NewRequest("POST", "/v1/patches", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}
