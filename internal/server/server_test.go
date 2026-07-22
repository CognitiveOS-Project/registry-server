package server

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/CognitiveOS-Project/registry-server/internal/auth"
	githubclient "github.com/CognitiveOS-Project/registry-server/internal/github"
	"github.com/CognitiveOS-Project/registry-server/internal/store"
	"golang.org/x/crypto/ssh"
)

func setupTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	dataDir := t.TempDir()

	memStore := store.NewMemoryStore()
	tokenAuth := auth.NewMemoryTokenStore()
	_ = tokenAuth.Add("test-token-123", "publish", "admin")

	cfg := Config{
		Addr:      ":0",
		DataDir:   dataDir,
		Store:     memStore,
		TokenAuth: tokenAuth,
	}

	_ = memStore.Put(store.Package{
		Name:           "test-patch",
		Version:        "1.0.0",
		Description:    "A test cognitive patch",
		Author:         "test-author",
		Size:           2048,
		ChecksumSHA256: "deadbeef",
		DownloadURL:    "https://example.com/test-patch-1.0.0.cgp",
		Tags:           []string{"test", "alpha"},
		Status:         "active",
	})

	_ = memStore.Put(store.Package{
		Name:        "existing-dep",
		Version:     "1.0.0",
		Description: "An existing dependency",
		Status:      "active",
	})

	codeHash := sha256.Sum256([]byte("SECRET123"))
	_ = memStore.Put(store.Package{
		Name:        "locked-patch",
		Version:     "1.0.0",
		Description: "A locked patch",
		Status:      "active",
		UnlockCodes: []string{hex.EncodeToString(codeHash[:])},
	})

	return New(cfg), dataDir
}

func testNewRequest(method, path string, body io.Reader) *http.Request {
	r := httptest.NewRequest(method, path, body)
	r.Header.Set("User-Agent", "cpm/test")
	return r
}

func TestHealth(t *testing.T) {
	srv, _ := setupTestServer(t)
	w := httptest.NewRecorder()
	r := testNewRequest("GET", "/v1/health", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "healthy" {
		t.Errorf("expected status healthy, got %s", resp["status"])
	}
}

func TestSearch(t *testing.T) {
	srv, _ := setupTestServer(t)
	w := httptest.NewRecorder()
	r := testNewRequest("GET", "/v1/search?q=test", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Results []store.Package `json:"results"`
		Total   int             `json:"total"`
		Page    int             `json:"page"`
		PerPage int             `json:"per_page"`
	}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Results) == 0 {
		t.Fatal("expected search results")
	}
	if resp.Results[0].Name != "test-patch" {
		t.Errorf("expected test-patch, got %s", resp.Results[0].Name)
	}
	if resp.Total != 1 {
		t.Errorf("expected total 1, got %d", resp.Total)
	}
	if resp.Page != 1 {
		t.Errorf("expected page 1, got %d", resp.Page)
	}
	if resp.PerPage != 20 {
		t.Errorf("expected per_page 20, got %d", resp.PerPage)
	}
}

func TestSearchNoQuery(t *testing.T) {
	srv, _ := setupTestServer(t)
	w := httptest.NewRecorder()
	r := testNewRequest("GET", "/v1/search", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Results []store.Package `json:"results"`
		Total   int             `json:"total"`
	}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Results) == 0 {
		t.Error("expected at least one result with empty query")
	}
	if resp.Total != 3 {
		t.Errorf("expected total 3, got %d", resp.Total)
	}
}

func TestSearchPagination(t *testing.T) {
	srv, _ := setupTestServer(t)
	w := httptest.NewRecorder()
	r := testNewRequest("GET", "/v1/search?page=1&per_page=1", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Results []store.Package `json:"results"`
		Total   int             `json:"total"`
		Page    int             `json:"page"`
		PerPage int             `json:"per_page"`
	}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Results) != 1 {
		t.Errorf("expected 1 result with per_page=1, got %d", len(resp.Results))
	}
	if resp.Total != 3 {
		t.Errorf("expected total 3, got %d", resp.Total)
	}
	if resp.PerPage != 1 {
		t.Errorf("expected per_page 1, got %d", resp.PerPage)
	}
}

func TestSearchExact(t *testing.T) {
	srv, _ := setupTestServer(t)
	w := httptest.NewRecorder()
	r := testNewRequest("GET", "/v1/search?q=test-patch&exact=true", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Results []store.Package `json:"results"`
		Total   int             `json:"total"`
	}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Results) != 1 {
		t.Errorf("expected 1 exact match, got %d", len(resp.Results))
	}
}

func TestGetPatchByVersion(t *testing.T) {
	srv, _ := setupTestServer(t)
	w := httptest.NewRecorder()
	r := testNewRequest("GET", "/v1/patches/test-patch/1.0.0", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var pkg store.Package
	_ = json.NewDecoder(w.Body).Decode(&pkg)
	if pkg.Name != "test-patch" {
		t.Errorf("expected test-patch, got %s", pkg.Name)
	}
	if pkg.Version != "1.0.0" {
		t.Errorf("expected 1.0.0, got %s", pkg.Version)
	}
}

func TestGetPatchLatest(t *testing.T) {
	srv, _ := setupTestServer(t)

	_ = srv.config.Store.Put(store.Package{
		Name:        "multi-ver",
		Version:     "2.0.0",
		Description: "newer version",
		Status:      "active",
	})

	w := httptest.NewRecorder()
	r := testNewRequest("GET", "/v1/patches/multi-ver", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var pkg store.Package
	_ = json.NewDecoder(w.Body).Decode(&pkg)
	if pkg.Version != "2.0.0" {
		t.Errorf("expected latest version 2.0.0, got %s", pkg.Version)
	}
}

func TestGetPatchNotFound(t *testing.T) {
	srv, _ := setupTestServer(t)
	w := httptest.NewRecorder()
	r := testNewRequest("GET", "/v1/patches/nonexistent/9.9.9", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestGetPatchNotFoundLatest(t *testing.T) {
	srv, _ := setupTestServer(t)
	w := httptest.NewRecorder()
	r := testNewRequest("GET", "/v1/patches/nonexistent", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestGetVersions(t *testing.T) {
	srv, _ := setupTestServer(t)
	w := httptest.NewRecorder()
	r := testNewRequest("GET", "/v1/patches/test-patch/versions", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var versions []struct {
		Version string `json:"version"`
		Status  string `json:"status"`
	}
	_ = json.NewDecoder(w.Body).Decode(&versions)
	if len(versions) != 1 {
		t.Fatalf("expected 1 version, got %d", len(versions))
	}
	if versions[0].Version != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %s", versions[0].Version)
	}
	if versions[0].Status != "active" {
		t.Errorf("expected status active, got %s", versions[0].Status)
	}
}

func TestDownload(t *testing.T) {
	srv, _ := setupTestServer(t)

	w := httptest.NewRecorder()
	r := testNewRequest("GET", "/v1/patches/test-patch/1.0.0/download", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusFound {
		t.Errorf("expected 302 redirect, got %d", w.Code)
	}

	loc := w.Header().Get("Location")
	if loc != "https://example.com/test-patch-1.0.0.cgp" {
		t.Errorf("expected redirect to download URL, got %s", loc)
	}
}

func TestDownloadNotFound(t *testing.T) {
	srv, _ := setupTestServer(t)

	w := httptest.NewRecorder()
	r := testNewRequest("GET", "/v1/patches/nonexistent/9.9.9/download", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for missing package, got %d", w.Code)
	}
}

func TestDownloadNoURL(t *testing.T) {
	srv, _ := setupTestServer(t)

	_ = srv.config.Store.Put(store.Package{
		Name:    "no-download",
		Version: "1.0.0",
	})

	w := httptest.NewRecorder()
	r := testNewRequest("GET", "/v1/patches/no-download/1.0.0/download", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for package without download URL, got %d", w.Code)
	}
}

func TestPublishRequiresAuth(t *testing.T) {
	srv, _ := setupTestServer(t)

	body := bytes.NewBufferString(`{"name":"p","version":"1.0.0","download_url":"https://example.com/p-1.0.0.cgp","sha256":"abc123def456abc123def456abc123def456abc123def456abc123def456abc1","manifest":{"name":"p","version":"1.0.0","description":"test"}}`)
	w := httptest.NewRecorder()
	r := testNewRequest("POST", "/v1/patches", body)
	r.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 without auth, got %d", w.Code)
	}
}

func TestPublishWithAuth(t *testing.T) {
	srv, _ := setupTestServer(t)

	manifest := `{"name":"new-patch","version":"0.1.0","description":"brand new","author":"tester"}`
	payload := map[string]interface{}{
		"name":         "new-patch",
		"version":      "0.1.0",
		"description":  "brand new",
		"author":       "tester",
		"download_url": "https://example.com/new-patch-0.1.0.cgp",
		"sha256":       "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"manifest":     json.RawMessage(manifest),
	}
	body, _ := json.Marshal(payload)

	w := httptest.NewRecorder()
	r := testNewRequest("POST", "/v1/patches", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer test-token-123")
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["name"] != "new-patch" {
		t.Errorf("expected new-patch, got %s", resp["name"])
	}
}

func TestPublishWithoutManifest(t *testing.T) {
	srv, _ := setupTestServer(t)

	payload := map[string]interface{}{
		"name":         "no-manifest-patch",
		"version":      "1.0.0",
		"description":  "published without manifest",
		"download_url": "https://example.com/no-manifest-1.0.0.cgp",
		"sha256":       "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	body, _ := json.Marshal(payload)

	w := httptest.NewRecorder()
	r := testNewRequest("POST", "/v1/patches", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer test-token-123")
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201 for publish without manifest, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPublishWithManifestAndDownloadURL(t *testing.T) {
	srv, _ := setupTestServer(t)

	manifest := `{"name":"json-patch","version":"2.0.0","description":"published via JSON","author":"json-test","license":"MIT"}`
	payload := map[string]interface{}{
		"name":         "json-patch",
		"version":      "2.0.0",
		"description":  "published via JSON",
		"author":       "json-test",
		"download_url": "https://example.com/json-patch-2.0.0.cgp",
		"sha256":       "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"manifest":     json.RawMessage(manifest),
	}
	body, _ := json.Marshal(payload)

	w := httptest.NewRecorder()
	r := testNewRequest("POST", "/v1/patches", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer test-token-123")
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var pkg map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&pkg)
	if pkg["name"] != "json-patch" {
		t.Errorf("expected json-patch, got %s", pkg["name"])
	}
	urls, ok := pkg["download_urls"].(map[string]interface{})
	if !ok || urls == nil {
		t.Errorf("expected download_urls map, got %v", pkg["download_urls"])
	} else if urls[""] != "https://example.com/json-patch-2.0.0.cgp" {
		t.Errorf("expected download_urls[''], got %s", urls[""])
	}
}

func TestPublishRejectsDuplicate(t *testing.T) {
	srv, _ := setupTestServer(t)

	manifest := `{"name":"test-patch","version":"1.0.0","description":"duplicate"}`
	payload := map[string]interface{}{
		"name":         "test-patch",
		"version":      "1.0.0",
		"description":  "duplicate",
		"download_url": "https://example.com/t-1.0.0.cgp",
		"sha256":       "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		"manifest":     json.RawMessage(manifest),
	}
	body, _ := json.Marshal(payload)

	w := httptest.NewRecorder()
	r := testNewRequest("POST", "/v1/patches", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer test-token-123")
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusConflict {
		t.Errorf("expected 409 for duplicate, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPublishRequiresDownloadURL(t *testing.T) {
	srv, _ := setupTestServer(t)

	payload := map[string]interface{}{
		"name":    "no-url",
		"version": "1.0.0",
	}
	body, _ := json.Marshal(payload)

	w := httptest.NewRecorder()
	r := testNewRequest("POST", "/v1/patches", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer test-token-123")
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPutVersion(t *testing.T) {
	srv, _ := setupTestServer(t)

	manifest := `{"name":"test-patch","version":"2.0.0","description":"new version via PUT"}`
	payload := map[string]interface{}{
		"name":         "test-patch",
		"version":      "2.0.0",
		"description":  "new version via PUT",
		"download_url": "https://example.com/test-patch-2.0.0.cgp",
		"sha256":       "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		"manifest":     json.RawMessage(manifest),
	}
	body, _ := json.Marshal(payload)

	w := httptest.NewRecorder()
	r := testNewRequest("PUT", "/v1/patches/test-patch/2.0.0", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer test-token-123")
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPutVersionMismatch(t *testing.T) {
	srv, _ := setupTestServer(t)

	manifest := `{"name":"test-patch","version":"3.0.0","description":"version mismatch"}`
	payload := map[string]interface{}{
		"name":         "test-patch",
		"version":      "2.0.0",
		"description":  "version mismatch",
		"download_url": "https://example.com/t-2.0.0.cgp",
		"sha256":       "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		"manifest":     json.RawMessage(manifest),
	}
	body, _ := json.Marshal(payload)

	w := httptest.NewRecorder()
	r := testNewRequest("PUT", "/v1/patches/test-patch/3.0.0", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer test-token-123")
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for version mismatch, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSetStatus(t *testing.T) {
	srv, _ := setupTestServer(t)

	body := bytes.NewBufferString(`{"status":"deprecated"}`)
	w := httptest.NewRecorder()
	r := testNewRequest("PATCH", "/v1/patches/test-patch/1.0.0/status", body)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer test-token-123")
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]string
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "deprecated" {
		t.Errorf("expected deprecated, got %s", resp["status"])
	}
}

func TestSetStatusRequiresAuth(t *testing.T) {
	srv, _ := setupTestServer(t)

	body := bytes.NewBufferString(`{"status":"deprecated"}`)
	w := httptest.NewRecorder()
	r := testNewRequest("PATCH", "/v1/patches/test-patch/1.0.0/status", body)
	r.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestGetDependencies(t *testing.T) {
	srv, _ := setupTestServer(t)

	w := httptest.NewRecorder()
	r := testNewRequest("GET", "/v1/patches/test-patch/dependencies", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp dependencyTree
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp.Name != "test-patch" {
		t.Errorf("expected test-patch, got %s", resp.Name)
	}
	if resp.Version != "1.0.0" {
		t.Errorf("expected 1.0.0, got %s", resp.Version)
	}
}

func TestGetDependenciesTree(t *testing.T) {
	srv, _ := setupTestServer(t)

	_ = srv.config.Store.Put(store.Package{
		Name:        "leaf-dep",
		Version:     "1.0.0",
		Description: "leaf dependency",
		Manifest:    `{"name":"leaf-dep","version":"1.0.0"}`,
		Status:      "active",
	})

	_ = srv.config.Store.Put(store.Package{
		Name:        "mid-dep",
		Version:     "1.0.0",
		Description: "middle dependency",
		Manifest:    `{"name":"mid-dep","version":"1.0.0","dependencies":{"leaf-dep":"^1.0.0"}}`,
		Status:      "active",
	})

	_ = srv.config.Store.Put(store.Package{
		Name:        "root-pkg",
		Version:     "1.0.0",
		Description: "root package",
		Manifest:    `{"name":"root-pkg","version":"1.0.0","dependencies":{"mid-dep":"^1.0.0"}}`,
		Status:      "active",
	})

	w := httptest.NewRecorder()
	r := testNewRequest("GET", "/v1/patches/root-pkg/dependencies", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp dependencyTree
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp.Name != "root-pkg" {
		t.Errorf("expected root-pkg, got %s", resp.Name)
	}
	if len(resp.Dependencies) != 1 {
		t.Fatalf("expected 1 direct dep, got %d", len(resp.Dependencies))
	}
	if resp.Dependencies[0].Name != "mid-dep" {
		t.Errorf("expected mid-dep, got %s", resp.Dependencies[0].Name)
	}
	if len(resp.Dependencies[0].Dependencies) != 1 {
		t.Fatalf("expected 1 transitive dep, got %d", len(resp.Dependencies[0].Dependencies))
	}
	if resp.Dependencies[0].Dependencies[0].Name != "leaf-dep" {
		t.Errorf("expected leaf-dep, got %s", resp.Dependencies[0].Dependencies[0].Name)
	}
}

func TestValidate(t *testing.T) {
	srv, _ := setupTestServer(t)

	_ = srv.config.Store.Put(store.Package{
		Name:           "validatable",
		Version:        "1.0.0",
		Description:    "can be validated",
		ChecksumSHA256: "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		Manifest:       `{"name":"validatable","version":"1.0.0","description":"can be validated"}`,
	})

	w := httptest.NewRecorder()
	r := testNewRequest("POST", "/v1/patches/validatable/1.0.0/validate", nil)
	r.Header.Set("Authorization", "Bearer test-token-123")
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "valid" {
		t.Errorf("expected valid, got %s", resp["status"])
	}
}

func TestUnlock(t *testing.T) {
	srv, _ := setupTestServer(t)

	body := bytes.NewBufferString(`{"code":"SECRET123"}`)
	w := httptest.NewRecorder()
	r := testNewRequest("POST", "/v1/patches/locked-patch/1.0.0/unlock", body)
	r.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "ok" {
		t.Errorf("expected status ok, got %s", resp["status"])
	}
	if resp["name"] != "locked-patch" {
		t.Errorf("expected name locked-patch, got %s", resp["name"])
	}
	if resp["version"] != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %s", resp["version"])
	}
}

func TestUnlockWrongCode(t *testing.T) {
	srv, _ := setupTestServer(t)

	body := bytes.NewBufferString(`{"code":"WRONG"}`)
	w := httptest.NewRecorder()
	r := testNewRequest("POST", "/v1/patches/locked-patch/1.0.0/unlock", body)
	r.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUnlockMissingCode(t *testing.T) {
	srv, _ := setupTestServer(t)

	body := bytes.NewBufferString(`{"code":""}`)
	w := httptest.NewRecorder()
	r := testNewRequest("POST", "/v1/patches/test-patch/1.0.0/unlock", body)
	r.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCORSHeaders(t *testing.T) {
	srv, _ := setupTestServer(t)

	w := httptest.NewRecorder()
	r := testNewRequest("OPTIONS", "/v1/health", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204 for OPTIONS, got %d", w.Code)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("expected CORS header")
	}
}

func TestPublishRequiresPublishScope(t *testing.T) {
	srv, _ := setupTestServer(t)

	_ = srv.config.TokenAuth.(*auth.MemoryTokenStore).Add("readonly-token", "read")

	body := bytes.NewBufferString(`{"name":"x","version":"1.0.0","download_url":"https://example.com/x.cgp","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","manifest":{"name":"x","version":"1.0.0","description":"test"}}`)
	w := httptest.NewRecorder()
	r := testNewRequest("POST", "/v1/patches", body)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer readonly-token")
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for missing publish scope, got %d", w.Code)
	}
}

func TestDependencyValidation(t *testing.T) {
	srv, _ := setupTestServer(t)

	manifest := `{"name":"dep-user","version":"1.0.0","description":"uses existing-dep","dependencies":{"existing-dep":"^1.0.0"}}`
	payload := map[string]interface{}{
		"name":         "dep-user",
		"version":      "1.0.0",
		"description":  "uses existing-dep",
		"download_url": "https://example.com/dep-user-1.0.0.cgp",
		"sha256":       "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		"manifest":     json.RawMessage(manifest),
	}
	body, _ := json.Marshal(payload)

	w := httptest.NewRecorder()
	r := testNewRequest("POST", "/v1/patches", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer test-token-123")
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201 for valid dependency, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDependencyValidationMissingDep(t *testing.T) {
	srv, _ := setupTestServer(t)

	manifest := `{"name":"bad-dep","version":"1.0.0","description":"missing dep","dependencies":{"nonexistent-dep":"^1.0.0"}}`
	payload := map[string]interface{}{
		"name":         "bad-dep",
		"version":      "1.0.0",
		"description":  "missing dep",
		"download_url": "https://example.com/bad-dep-1.0.0.cgp",
		"sha256":       "iiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiiii",
		"manifest":     json.RawMessage(manifest),
	}
	body, _ := json.Marshal(payload)

	w := httptest.NewRecorder()
	r := testNewRequest("POST", "/v1/patches", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer test-token-123")
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 for missing dependency, got %d: %s", w.Code, w.Body.String())
	}
}

func setupOfficialTestServer(t *testing.T, ghHandler http.HandlerFunc) (*Server, *httptest.Server) {
	t.Helper()
	dataDir := t.TempDir()

	memStore := store.NewMemoryStore()
	tokenAuth := auth.NewMemoryTokenStore()
	_ = tokenAuth.Add("test-token-123", "publish", "admin")
	sshKeys := auth.NewMemorySSHKeyStore()

	ghSrv := httptest.NewServer(ghHandler)
	t.Cleanup(ghSrv.Close)

	ghClient := &githubclient.Client{
		Org:     "test-org",
		Token:   "ghp_test",
		HTTP:    ghSrv.Client(),
		BaseURL: ghSrv.URL,
	}

	cfg := Config{
		Addr:      ":0",
		DataDir:   dataDir,
		Store:     memStore,
		TokenAuth: tokenAuth,
		SSHKeys:   sshKeys,
		GitHub:    ghClient,
	}

	return New(cfg), ghSrv
}

func buildMultipartBody(t *testing.T, metadata map[string]interface{}, cgpData []byte) (string, []byte) {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("failed to marshal metadata: %v", err)
	}

	if err := writer.WriteField("metadata", string(metadataJSON)); err != nil {
		t.Fatalf("failed to write metadata field: %v", err)
	}

	part, err := writer.CreateFormFile("cgp", "test-pkg-1.0.0.cgp")
	if err != nil {
		t.Fatalf("failed to create cgp part: %v", err)
	}
	if _, err := part.Write(cgpData); err != nil {
		t.Fatalf("failed to write cgp data: %v", err)
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("failed to close writer: %v", err)
	}

	return writer.FormDataContentType(), body.Bytes()
}

func TestOfficialPublishRequiresGitHub(t *testing.T) {
	dataDir := t.TempDir()
	memStore := store.NewMemoryStore()
	tokenAuth := auth.NewMemoryTokenStore()
	_ = tokenAuth.Add("test-token-123", "publish", "admin")
	sshKeys := auth.NewMemorySSHKeyStore()

	cfg := Config{
		Addr:      ":0",
		DataDir:   dataDir,
		Store:     memStore,
		TokenAuth: tokenAuth,
		SSHKeys:   sshKeys,
		GitHub:    nil,
	}

	srv := New(cfg)

	metadata := map[string]interface{}{
		"name":        "my-pkg",
		"version":     "1.0.0",
		"description": "test package",
		"sha256":      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	ct, body := buildMultipartBody(t, metadata, []byte("fake cgp data"))

	w := httptest.NewRecorder()
	r := testNewRequest("POST", "/v1/patches", bytes.NewReader(body))
	r.Header.Set("Content-Type", ct)
	r.Header.Set("Authorization", "Bearer test-token-123")
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when GitHub not configured, got %d: %s", w.Code, w.Body.String())
	}
}

func TestOfficialPublishSuccess(t *testing.T) {
	var assetUploaded bool

	srv, _ := setupOfficialTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/repos/test-org/my-pkg":
			w.WriteHeader(404)
		case r.Method == "POST" && r.URL.Path == "/orgs/test-org/repos":
			w.WriteHeader(201)
			fmt.Fprintf(w, `{"name":"my-pkg"}`)
		case r.Method == "POST" && r.URL.Path == "/repos/test-org/my-pkg/releases":
			w.WriteHeader(201)
			fmt.Fprintf(w, `{"id":1,"tag_name":"v1.0.0","name":"my-pkg v1.0.0"}`)
		case r.Method == "POST" && r.URL.Path == "/repos/test-org/my-pkg/releases/1/assets":
			assetUploaded = true
			w.WriteHeader(201)
			fmt.Fprintf(w, `{"browser_download_url":"https://github.com/test-org/my-pkg/releases/download/v1.0.0/my-pkg-1.0.0.cgp"}`)
		default:
			w.WriteHeader(404)
			fmt.Fprintf(w, `{"message":"not found: %s %s"}`, r.Method, r.URL.Path)
		}
	})

	metadata := map[string]interface{}{
		"name":        "my-pkg",
		"version":     "1.0.0",
		"description": "my awesome package",
		"author":      "tester",
		"sha256":      "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"manifest":    map[string]interface{}{"name": "my-pkg", "version": "1.0.0", "description": "my awesome package"},
	}
	ct, body := buildMultipartBody(t, metadata, []byte("fake cgp archive data"))

	w := httptest.NewRecorder()
	r := testNewRequest("POST", "/v1/patches", bytes.NewReader(body))
	r.Header.Set("Content-Type", ct)
	r.Header.Set("Authorization", "Bearer test-token-123")
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if !assetUploaded {
		t.Error("expected .cgp asset to be uploaded to GitHub")
	}

	var resp map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["name"] != "my-pkg" {
		t.Errorf("expected name my-pkg, got %v", resp["name"])
	}
	if resp["version"] != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %v", resp["version"])
	}
	if resp["download_url"] == nil {
		t.Error("expected download_url in response")
	}

	pkg, err := srv.config.Store.Get("my-pkg", "1.0.0")
	if err != nil {
		t.Fatalf("package not stored: %v", err)
	}
	if pkg.DownloadURL == "" {
		t.Error("expected download URL stored")
	}
}

func TestOfficialPublishDuplicate(t *testing.T) {
	srv, _ := setupOfficialTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		fmt.Fprintf(w, `{}`)
	})

	_ = srv.config.Store.Put(store.Package{
		Name:           "dup-pkg",
		Version:        "1.0.0",
		Description:    "already exists",
		ChecksumSHA256: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		DownloadURL:    "https://example.com/dup-pkg-1.0.0.cgp",
	})

	metadata := map[string]interface{}{
		"name":        "dup-pkg",
		"version":     "1.0.0",
		"description": "duplicate",
		"sha256":      "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
	}
	ct, body := buildMultipartBody(t, metadata, []byte("fake cgp data"))

	w := httptest.NewRecorder()
	r := testNewRequest("POST", "/v1/patches", bytes.NewReader(body))
	r.Header.Set("Content-Type", ct)
	r.Header.Set("Authorization", "Bearer test-token-123")
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusConflict {
		t.Errorf("expected 409 for duplicate, got %d: %s", w.Code, w.Body.String())
	}
}

func TestOfficialPublishMissingMetadata(t *testing.T) {
	srv, _ := setupOfficialTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		fmt.Fprintf(w, `{}`)
	})
	_ = srv

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("cgp", "test.cgp")
	part.Write([]byte("fake data"))
	writer.Close()

	w := httptest.NewRecorder()
	r := testNewRequest("POST", "/v1/patches", bytes.NewReader(body.Bytes()))
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r.Header.Set("Authorization", "Bearer test-token-123")
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing metadata, got %d: %s", w.Code, w.Body.String())
	}
}

func TestOfficialPublishMissingCGP(t *testing.T) {
	srv, _ := setupOfficialTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		fmt.Fprintf(w, `{}`)
	})
	_ = srv

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	writer.WriteField("metadata", `{"name":"x","version":"1.0.0"}`)
	writer.Close()

	w := httptest.NewRecorder()
	r := testNewRequest("POST", "/v1/patches", bytes.NewReader(body.Bytes()))
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r.Header.Set("Authorization", "Bearer test-token-123")
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing cgp, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAuthStatusRegistered(t *testing.T) {
	srv, _ := setupTestServer(t)

	// Generate a real ed25519 key pair for testing
	_, pubKeyBytes := generateTestSSHKey(t)
	info, err := srv.config.SSHKeys.Register(string(pubKeyBytes))
	if err != nil {
		t.Fatalf("failed to register key: %v", err)
	}

	fp := info.Fingerprint

	body, _ := json.Marshal(map[string]string{"fingerprint": fp})
	w := httptest.NewRecorder()
	r := testNewRequest("PUT", "/v1/auth/status", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["registered"] != true {
		t.Errorf("expected registered=true, got %v", resp["registered"])
	}
	if resp["fingerprint"] != fp {
		t.Errorf("expected fingerprint %s, got %v", fp, resp["fingerprint"])
	}
}

func TestAuthStatusNotRegistered(t *testing.T) {
	srv, _ := setupTestServer(t)

	body, _ := json.Marshal(map[string]string{"fingerprint": "SHA256:nonexistent"})
	w := httptest.NewRecorder()
	r := testNewRequest("PUT", "/v1/auth/status", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["registered"] != false {
		t.Errorf("expected registered=false, got %v", resp["registered"])
	}
}

func TestAuthStatusMissingFingerprint(t *testing.T) {
	srv, _ := setupTestServer(t)

	body, _ := json.Marshal(map[string]string{})
	w := httptest.NewRecorder()
	r := testNewRequest("PUT", "/v1/auth/status", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing fingerprint, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAuthStatusMethodNotAllowed(t *testing.T) {
	srv, _ := setupTestServer(t)

	w := httptest.NewRecorder()
	r := testNewRequest("GET", "/v1/auth/status", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for GET, got %d", w.Code)
	}
}

func TestAuthSignup(t *testing.T) {
	srv, _ := setupTestServer(t)

	priv, pubBytes := generateTestSSHKey(t)

	// Create profile
	profile := map[string]interface{}{
		"hardware": map[string]interface{}{
			"cpu":    "ARM Cortex-A72",
			"cores":  4,
			"arch":   "aarch64",
			"ram_mb": 8192,
		},
		"software": map[string]interface{}{
			"os":         "linux",
			"distro":     "CognitiveOS",
			"cpm_version": "1.0.0",
		},
	}
	profileJSON, _ := json.Marshal(profile)

	// Sign SHA-256 hash of profile using ed25519
	hash := sha256.Sum256(profileJSON)
	sig := ed25519.Sign(priv, hash[:])

	// Marshal as SSH wire format
	wireSig := marshalSSHWire("ssh-ed25519", sig)
	sigB64 := base64.RawStdEncoding.EncodeToString(wireSig)

	body, _ := json.Marshal(map[string]interface{}{
		"profile":    json.RawMessage(profileJSON),
		"public_key": string(pubBytes),
		"signature":  sigB64,
	})

	w := httptest.NewRecorder()
	r := testNewRequest("POST", "/v1/auth/signup", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "pending" {
		t.Errorf("expected status=pending, got %v", resp["status"])
	}
	if resp["machine_id"] == nil || resp["machine_id"] == "" {
		t.Errorf("expected machine_id, got %v", resp["machine_id"])
	}
}

func marshalSSHWire(format string, sigBytes []byte) []byte {
	formatBytes := []byte(format)
	formatLen := make([]byte, 4)
	binary.BigEndian.PutUint32(formatLen, uint32(len(formatBytes)))

	blobLen := make([]byte, 4)
	binary.BigEndian.PutUint32(blobLen, uint32(len(sigBytes)))

	result := make([]byte, 0, 8+len(formatBytes)+len(sigBytes))
	result = append(result, formatLen...)
	result = append(result, formatBytes...)
	result = append(result, blobLen...)
	result = append(result, sigBytes...)
	return result
}

func TestAuthSignupInvalidSignature(t *testing.T) {
	srv, _ := setupTestServer(t)

	_, pubBytes := generateTestSSHKey(t)

	profile := map[string]interface{}{"hardware": map[string]interface{}{"cpu": "test"}}
	profileJSON, _ := json.Marshal(profile)

	body, _ := json.Marshal(map[string]interface{}{
		"profile":    json.RawMessage(profileJSON),
		"public_key": string(pubBytes),
		"signature":  "invalidsig",
	})

	w := httptest.NewRecorder()
	r := testNewRequest("POST", "/v1/auth/signup", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for invalid signature, got %d: %s", w.Code, w.Body.String())
	}
}

func generateTestSSHKey(t *testing.T) (ed25519.PrivateKey, []byte) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	sshPubKey, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("failed to create SSH public key: %v", err)
	}

	pubBytes := ssh.MarshalAuthorizedKey(sshPubKey)

	_ = priv
	return priv, pubBytes
}
