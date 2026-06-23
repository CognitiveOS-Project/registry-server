package server

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/CognitiveOS-Project/registry-server/internal/auth"
	"github.com/CognitiveOS-Project/registry-server/internal/store"
)

func setupTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	dataDir := t.TempDir()

	memStore := store.NewMemoryStore()
	tokenAuth := auth.NewMemoryTokenStore()
	tokenAuth.Add("test-token-123")

	cfg := Config{
		Addr:      ":0",
		DataDir:   dataDir,
		Store:     memStore,
		TokenAuth: tokenAuth,
	}

	memStore.Put(store.Package{
		Name:        "test-patch",
		Version:     "1.0.0",
		Description: "A test cognitive patch",
		Author:      "test-author",
		Size:        2048,
		SHA256:      "deadbeef",
		Tags:        []string{"test", "alpha"},
	})

	return New(cfg), dataDir
}

func TestHealth(t *testing.T) {
	srv, _ := setupTestServer(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/health", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "ok" {
		t.Errorf("expected status ok, got %s", resp["status"])
	}
}

func TestSearch(t *testing.T) {
	srv, _ := setupTestServer(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/search?q=test", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var results []store.Package
	json.NewDecoder(w.Body).Decode(&results)
	if len(results) == 0 {
		t.Fatal("expected search results")
	}
	if results[0].Name != "test-patch" {
		t.Errorf("expected test-patch, got %s", results[0].Name)
	}
}

func TestSearchNoQuery(t *testing.T) {
	srv, _ := setupTestServer(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/search", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var results []store.Package
	json.NewDecoder(w.Body).Decode(&results)
	if len(results) == 0 {
		t.Error("expected at least one result with empty query")
	}
}

func TestGetPatch(t *testing.T) {
	srv, _ := setupTestServer(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/patches/test-patch/1.0.0", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var pkg store.Package
	json.NewDecoder(w.Body).Decode(&pkg)
	if pkg.Name != "test-patch" {
		t.Errorf("expected test-patch, got %s", pkg.Name)
	}
	if pkg.Version != "1.0.0" {
		t.Errorf("expected 1.0.0, got %s", pkg.Version)
	}
}

func TestGetPatchNotFound(t *testing.T) {
	srv, _ := setupTestServer(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/patches/nonexistent/9.9.9", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestDownload(t *testing.T) {
	srv, dataDir := setupTestServer(t)

	cgpContent := []byte("fake .cgp content")
	cgpPath := filepath.Join(dataDir, "test-patch-1.0.0.cgp")
	if err := os.WriteFile(cgpPath, cgpContent, 0644); err != nil {
		t.Fatalf("failed to write test .cgp file: %v", err)
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/patches/test-patch/1.0.0/download", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	if w.Body.String() != string(cgpContent) {
		t.Errorf("expected body %q, got %q", string(cgpContent), w.Body.String())
	}

	ct := w.Header().Get("Content-Type")
	if ct != "application/octet-stream" {
		t.Errorf("expected application/octet-stream, got %s", ct)
	}
}

func TestDownloadNotFound(t *testing.T) {
	srv, _ := setupTestServer(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/patches/test-patch/1.0.0/download", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for missing .cgp file, got %d", w.Code)
	}
}

func TestPublishRequiresAuth(t *testing.T) {
	srv, _ := setupTestServer(t)

	body := bytes.NewBufferString(`{"name":"p","version":"1.0.0"}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/patches", body)
	r.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without auth, got %d", w.Code)
	}
}

func TestPublishWithAuth(t *testing.T) {
	srv, dataDir := setupTestServer(t)

	var buf bytes.Buffer
	mp := multipart.NewWriter(&buf)
	mp.WriteField("name", "new-patch")
	mp.WriteField("version", "0.1.0")
	mp.WriteField("description", "brand new")
	mp.WriteField("author", "tester")

	fw, _ := mp.CreateFormFile("file", "patch.cgp")
	fw.Write([]byte("cgp data here"))
	mp.Close()

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/patches", &buf)
	r.Header.Set("Content-Type", mp.FormDataContentType())
	r.Header.Set("Authorization", "Bearer test-token-123")
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var pkg store.Package
	json.NewDecoder(w.Body).Decode(&pkg)
	if pkg.Name != "new-patch" {
		t.Errorf("expected new-patch, got %s", pkg.Name)
	}
	if pkg.Size <= 0 {
		t.Errorf("expected size > 0, got %d", pkg.Size)
	}

	if _, err := os.Stat(filepath.Join(dataDir, "new-patch-0.1.0.cgp")); os.IsNotExist(err) {
		t.Error("expected .cgp file to exist on disk")
	}
}

func TestUnlock(t *testing.T) {
	srv, _ := setupTestServer(t)

	body := bytes.NewBufferString(`{"model":"gpt-4","unlock_code":"CODE123"}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/unlock", body)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer test-token-123")
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "ok" {
		t.Errorf("expected status ok, got %s", resp["status"])
	}
}

func TestUnlockRequiresAuth(t *testing.T) {
	srv, _ := setupTestServer(t)

	body := bytes.NewBufferString(`{"model":"gpt-4","unlock_code":"CODE123"}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/unlock", body)
	r.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without auth, got %d", w.Code)
	}
}

func TestUnlockMissingFields(t *testing.T) {
	srv, _ := setupTestServer(t)

	body := bytes.NewBufferString(`{"model":""}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/unlock", body)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer test-token-123")
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCORSHeaders(t *testing.T) {
	srv, _ := setupTestServer(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("OPTIONS", "/v1/health", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204 for OPTIONS, got %d", w.Code)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("expected CORS header")
	}
}
