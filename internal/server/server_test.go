package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/CognitiveOS-Project/registry-server/internal/auth"
	"github.com/CognitiveOS-Project/registry-server/internal/store"
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

	var resp map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "healthy" {
		t.Errorf("expected status healthy, got %s", resp["status"])
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
	r := httptest.NewRequest("GET", "/v1/search", nil)
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
	if resp.Total != 2 {
		t.Errorf("expected total 2, got %d", resp.Total)
	}
}

func TestSearchPagination(t *testing.T) {
	srv, _ := setupTestServer(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/search?page=1&per_page=1", nil)
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
	if resp.Total != 2 {
		t.Errorf("expected total 2, got %d", resp.Total)
	}
	if resp.PerPage != 1 {
		t.Errorf("expected per_page 1, got %d", resp.PerPage)
	}
}

func TestSearchExact(t *testing.T) {
	srv, _ := setupTestServer(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/search?q=test-patch&exact=true", nil)
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
	r := httptest.NewRequest("GET", "/v1/patches/test-patch/1.0.0", nil)
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
	r := httptest.NewRequest("GET", "/v1/patches/multi-ver", nil)
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
	r := httptest.NewRequest("GET", "/v1/patches/nonexistent/9.9.9", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestGetPatchNotFoundLatest(t *testing.T) {
	srv, _ := setupTestServer(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/patches/nonexistent", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestGetVersions(t *testing.T) {
	srv, _ := setupTestServer(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/patches/test-patch/versions", nil)
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
	r := httptest.NewRequest("GET", "/v1/patches/test-patch/1.0.0/download", nil)
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
	r := httptest.NewRequest("GET", "/v1/patches/nonexistent/9.9.9/download", nil)
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
	r := httptest.NewRequest("GET", "/v1/patches/no-download/1.0.0/download", nil)
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for package without download URL, got %d", w.Code)
	}
}

func TestPublishRequiresAuth(t *testing.T) {
	srv, _ := setupTestServer(t)

	body := bytes.NewBufferString(`{"name":"p","version":"1.0.0","download_url":"https://example.com/p-1.0.0.cgp","sha256":"abc123def456abc123def456abc123def456abc123def456abc123def456abc1","manifest":{"name":"p","version":"1.0.0","description":"test"}}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/patches", body)
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
	r := httptest.NewRequest("POST", "/v1/patches", bytes.NewReader(body))
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
	r := httptest.NewRequest("POST", "/v1/patches", bytes.NewReader(body))
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
	r := httptest.NewRequest("POST", "/v1/patches", bytes.NewReader(body))
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
	if pkg["download_url"] != "https://example.com/json-patch-2.0.0.cgp" {
		t.Errorf("expected download_url, got %s", pkg["download_url"])
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
	r := httptest.NewRequest("POST", "/v1/patches", bytes.NewReader(body))
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
	r := httptest.NewRequest("POST", "/v1/patches", bytes.NewReader(body))
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
	r := httptest.NewRequest("PUT", "/v1/patches/test-patch/2.0.0", bytes.NewReader(body))
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
	r := httptest.NewRequest("PUT", "/v1/patches/test-patch/3.0.0", bytes.NewReader(body))
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
	r := httptest.NewRequest("PATCH", "/v1/patches/test-patch/1.0.0/status", body)
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
	r := httptest.NewRequest("PATCH", "/v1/patches/test-patch/1.0.0/status", body)
	r.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestGetDependencies(t *testing.T) {
	srv, _ := setupTestServer(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/patches/test-patch/dependencies", nil)
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
	r := httptest.NewRequest("GET", "/v1/patches/root-pkg/dependencies", nil)
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
	r := httptest.NewRequest("POST", "/v1/patches/validatable/1.0.0/validate", nil)
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

	body := bytes.NewBufferString(`{"code":"CODE123"}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/patches/test-patch/1.0.0/unlock", body)
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
	if resp["name"] != "test-patch" {
		t.Errorf("expected name test-patch, got %s", resp["name"])
	}
	if resp["version"] != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %s", resp["version"])
	}
}

func TestUnlockMissingCode(t *testing.T) {
	srv, _ := setupTestServer(t)

	body := bytes.NewBufferString(`{"code":""}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/patches/test-patch/1.0.0/unlock", body)
	r.Header.Set("Content-Type", "application/json")
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

func TestPublishRequiresPublishScope(t *testing.T) {
	srv, _ := setupTestServer(t)

	_ = srv.config.TokenAuth.(*auth.MemoryTokenStore).Add("readonly-token", "read")

	body := bytes.NewBufferString(`{"name":"x","version":"1.0.0","download_url":"https://example.com/x.cgp","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","manifest":{"name":"x","version":"1.0.0","description":"test"}}`)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/patches", body)
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
	r := httptest.NewRequest("POST", "/v1/patches", bytes.NewReader(body))
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
	r := httptest.NewRequest("POST", "/v1/patches", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer test-token-123")
	srv.ServeHTTP(w, r)

	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 for missing dependency, got %d: %s", w.Code, w.Body.String())
	}
}
