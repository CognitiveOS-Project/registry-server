package server

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/CognitiveOS-Project/registry-server/internal/auth"
	"github.com/CognitiveOS-Project/registry-server/internal/store"
	"golang.org/x/crypto/ssh"
)

func setupE2EServer(t *testing.T) *Server {
	t.Helper()
	st := store.NewMemoryStore()
	ts := auth.NewMemoryTokenStore()
	_ = ts.Add("e2e-token", "publish")
	sshKeys := auth.NewMemorySSHKeyStore()
	owners := auth.NewMemoryOwnerStore()
	srv := New(Config{
		Addr:      ":0",
		Store:     st,
		TokenAuth: ts,
		SSHKeys:   sshKeys,
		Owners:    owners,
	})
	return srv
}

func e2eReq(srv *Server, method, path string, body interface{}, headers map[string]string) (int, map[string]interface{}) {
	var bodyReader *bytes.Buffer
	if body != nil {
		data, _ := json.Marshal(body)
		bodyReader = bytes.NewBuffer(data)
	} else {
		bodyReader = bytes.NewBuffer(nil)
	}
	r := httptest.NewRequest(method, path, bodyReader)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("User-Agent", "cpm/e2e-test")
	r.Header.Set("Authorization", "Bearer e2e-token")
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	var result map[string]interface{}
	json.NewDecoder(w.Body).Decode(&result)
	return w.Code, result
}

func generateSSHKeyPair(t *testing.T) (string, string, ssh.Signer) {
	t.Helper()
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}
	sshPubKey, err := ssh.NewPublicKey(pubKey)
	if err != nil {
		t.Fatalf("failed to create SSH public key: %v", err)
	}
	pubKeyStr := string(ssh.MarshalAuthorizedKey(sshPubKey))
	signer, err := ssh.NewSignerFromKey(privKey)
	if err != nil {
		t.Fatalf("failed to create signer: %v", err)
	}
	return pubKeyStr, ssh.FingerprintSHA256(sshPubKey), signer
}

func TestE2EFullFlow(t *testing.T) {
	srv := setupE2EServer(t)
	passed := 0
	failed := 0

	test := func(name string, ok bool) {
		t.Helper()
		if ok {
			t.Logf("  PASS: %s", name)
			passed++
		} else {
			t.Errorf("  FAIL: %s", name)
			failed++
		}
	}

	// 1. Health
	t.Run("Health", func(t *testing.T) {
		code, body := e2eReq(srv, "GET", "/v1/health", nil, nil)
		test("status 200", code == 200)
		test("version 2.1.0", body["version"] == "2.1.0")
		test("has uptime_seconds", body["uptime_seconds"] != nil)
		test("status healthy", body["status"] == "healthy")
	})

	// 2. SSH key setup
	t.Run("SSHKeySetup", func(t *testing.T) {
		pubKeyStr, fingerprint, _ := generateSSHKeyPair(t)

		// Register key
		code, body := e2eReq(srv, "POST", "/v1/auth/register", map[string]string{"public_key": pubKeyStr}, nil)
		test("register 201", code == 201)
		test("got fingerprint", body["fingerprint"] != nil)
		gotFP := body["fingerprint"].(string)
		test("fingerprint matches", gotFP == fingerprint)

		// Auth status
		code, body = e2eReq(srv, "PUT", "/v1/auth/status", map[string]string{"fingerprint": fingerprint}, nil)
		test("auth status 200", code == 200)
		test("registered true", body["registered"] == true)
		// 3. Publish contacts-bridge first (hello-world depends on it)
		t.Run("PublishContactsBridge", func(t *testing.T) {
			publishBody := map[string]interface{}{
				"name": "contacts-bridge", "version": "1.2.0",
				"description": "Contacts bridge MCP", "author": "tester", "license": "MIT",
				"sha256": "bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222",
				"download_urls": map[string]string{"linux/amd64": "https://example.com/contacts-bridge-1.2.0.cgp"},
				"manifest": map[string]interface{}{"name": "contacts-bridge", "version": "1.2.0", "description": "Contacts bridge MCP"},
				"hardware_requirements": map[string]interface{}{"min_ram_mb": 256, "min_storage_mb": 100},
			}
			code, resp := e2eReq(srv, "POST", "/v1/patches", publishBody, nil)
			t.Logf("  contacts-bridge publish status=%d resp=%v", code, resp)
			test("publish contacts-bridge 201", code == 201)
		})

		// 4. Publish hello-world (depends on contacts-bridge)
		t.Run("PublishHelloWorld", func(t *testing.T) {
			manifest := map[string]interface{}{
				"name": "hello-world", "version": "1.0.0",
				"description": "A test hello world", "author": "tester", "license": "MIT",
				"hardware_requirements": map[string]interface{}{"min_ram_mb": 512},
				"dependencies":         map[string]interface{}{"contacts-bridge": "^1.0.0"},
			}

			publishBody := map[string]interface{}{
				"name": "hello-world", "version": "1.0.0",
				"description": "A test hello world package", "author": "tester", "license": "MIT",
				"sha256": "aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111",
				"download_urls": map[string]string{"linux/amd64": "https://example.com/hello-world-1.0.0-linux-amd64.cgp"},
				"manifest": manifest,
				"hardware_requirements": map[string]interface{}{"min_ram_mb": 512},
			}

			code, body := e2eReq(srv, "POST", "/v1/patches", publishBody, nil)
			test("publish 201", code == 201)
			test("has publisher_fingerprint", body["publisher_fingerprint"] != nil)
			test("no download_url (singular)", body["download_url"] == nil)
			test("no release_tag", body["release_tag"] == nil)
			test("has download_urls", body["download_urls"] != nil)
			test("has url", body["url"] != nil)
			t.Logf("  Response: %v", body)
		})

		// 5. Publish email-manager
		t.Run("PublishEmailManager", func(t *testing.T) {
			emailManifest := map[string]interface{}{
				"name": "email-manager", "version": "2.0.0", "description": "Email management",
				"capabilities": []string{"email", "smtp"},
			}
			publishBody := map[string]interface{}{
				"name": "email-manager", "version": "2.0.0",
				"description": "Email management", "author": "tester", "license": "Apache-2.0",
				"sha256": "cccc3333cccc3333cccc3333cccc3333cccc3333cccc3333cccc3333cccc3333",
				"download_urls": map[string]string{
					"linux/amd64": "https://example.com/email-manager-2.0.0.cgp",
					"linux/arm64": "https://example.com/email-manager-2.0.0-arm64.cgp",
				},
				"manifest": emailManifest,
				"hardware_requirements": map[string]interface{}{"min_ram_mb": 1024, "min_storage_mb": 500},
			}
			code, _ := e2eReq(srv, "POST", "/v1/patches", publishBody, nil)
			test("publish email-manager 201", code == 201)
		})

		// 6. Search
		t.Run("Search", func(t *testing.T) {
			code, body := e2eReq(srv, "GET", "/v1/search?q=hello", nil, nil)
			test("search 200", code == 200)
			results := body["results"].([]interface{})
			test("found hello-world", len(results) > 0)
			if len(results) > 0 {
				r := results[0].(map[string]interface{})
				test("sha256 field (not checksum_sha256)", r["sha256"] != nil && r["checksum_sha256"] == nil)
				test("has hardware_requirements", r["hardware_requirements"] != nil)
				test("has published_at", r["published_at"] != nil)
				test("no tags field", r["tags"] == nil)
				t.Logf("  Search result: %v", r)
			}
		})

		// 7. Search with min_storage_mb
		t.Run("SearchMinStorage", func(t *testing.T) {
			code, body := e2eReq(srv, "GET", "/v1/search?q=&min_storage_mb=400", nil, nil)
			test("search 200", code == 200)
			results := body["results"].([]interface{})
			test("found 1 package with min_storage_mb>=400", len(results) == 1)
			if len(results) > 0 {
				r := results[0].(map[string]interface{})
				test("result is email-manager", r["name"] == "email-manager")
			}
		})

		// 8. Get versions (wrapped shape)
		t.Run("GetVersions", func(t *testing.T) {
			code, body := e2eReq(srv, "GET", "/v1/patches/hello-world/versions", nil, nil)
			test("versions 200", code == 200)
			test("has name field", body["name"] == "hello-world")
			versions := body["versions"].([]interface{})
			test("has versions array", len(versions) == 1)
			if len(versions) > 0 {
				v := versions[0].(map[string]interface{})
				test("has published_at", v["published_at"] != nil)
				test("has sha256", v["sha256"] != nil)
				test("has status", v["status"] != nil)
				test("has download_urls", v["download_urls"] != nil)
				t.Logf("  Version: %v", v)
			}
		})

		// 9. Dependencies (flat map + transitive)
		t.Run("Dependencies", func(t *testing.T) {
			code, body := e2eReq(srv, "GET", "/v1/patches/hello-world/dependencies", nil, nil)
			test("deps 200", code == 200)
			test("name hello-world", body["name"] == "hello-world")
			deps := body["dependencies"].(map[string]interface{})
			test("has contacts-bridge dep", deps["contacts-bridge"] != nil)
			transitive := body["transitive"].([]interface{})
			test("has transitive deps", len(transitive) > 0)
			test("has status field", body["status"] != nil)
			t.Logf("  Dependencies: %v", deps)
			t.Logf("  Transitive: %v", transitive)
		})

		// 10. Notary check
		t.Run("NotaryCheck", func(t *testing.T) {
			code, body := e2eReq(srv, "GET", "/v1/notary/check?source=github&path=hello-world&version=1.0.0", nil, nil)
			test("notary 200", code == 200)
			test("verified field present", body["verified"] != nil)
			test("has stored_hash", body["stored_hash"] != nil)
			test("has remote_hash", body["remote_hash"] != nil)
			test("has remote_etag", body["remote_etag"] != nil)
			test("has checked_at", body["checked_at"] != nil)
			t.Logf("  Notary: %v", body)
		})

		// 11. Get specific version
		t.Run("GetSpecificVersion", func(t *testing.T) {
			code, body := e2eReq(srv, "GET", "/v1/patches/hello-world/1.0.0", nil, nil)
			test("get version 200", code == 200)
			test("version 1.0.0", body["version"] == "1.0.0")
			test("name hello-world", body["name"] == "hello-world")
		})

		// 12. Error envelope
		t.Run("ErrorEnvelope", func(t *testing.T) {
			code, body := e2eReq(srv, "GET", "/v1/patches/nonexistent/1.0.0", nil, nil)
			test("not found 404", code == 404)
			errObj := body["error"].(map[string]interface{})
			test("has error code", errObj["code"] == "NOT_FOUND")
			test("has error message", errObj["message"] != nil)
			t.Logf("  Error: %v", errObj)
		})

		// 13. Get patch summary
		t.Run("GetPatchSummary", func(t *testing.T) {
			code, body := e2eReq(srv, "GET", "/v1/patches/hello-world", nil, nil)
			test("get patch 200", code == 200)
			test("name hello-world", body["name"] == "hello-world")
		})

		// 14. Duplicate publish (should fail)
		t.Run("DuplicatePublish", func(t *testing.T) {
			publishBody := map[string]interface{}{
				"name": "hello-world", "version": "1.0.0",
				"description": "duplicate", "sha256": "dddd",
				"download_urls": map[string]string{"": "https://example.com/dup.cgp"},
				"manifest": map[string]interface{}{"name": "hello-world", "version": "1.0.0", "description": "A test hello world package"},
			}
			code, body := e2eReq(srv, "POST", "/v1/patches", publishBody, nil)
			test("duplicate 409", code == 409)
			errObj := body["error"].(map[string]interface{})
			test("ALREADY_EXISTS code", errObj["code"] == "ALREADY_EXISTS")
		})
	})

	t.Logf("\n=== E2E RESULTS: %d passed, %d failed ===", passed, failed)
}

func TestE2ERateLimits(t *testing.T) {
	srv := setupE2EServer(t)

	handler := srv.rateLimit.Middleware(srv)

	// Make requests to search endpoint (100/min limit)
	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/v1/search?q=test", nil)
		r.Header.Set("User-Agent", "cpm/e2e")
		r.RemoteAddr = "10.0.0.1:12345"
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i, w.Code)
		}
	}

	// Check rate limit headers
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/search?q=test", nil)
	r.Header.Set("User-Agent", "cpm/e2e")
	r.RemoteAddr = "10.0.0.1:12345"
	handler.ServeHTTP(w, r)

	limit := w.Header().Get("X-RateLimit-Limit")
	if limit == "" {
		t.Error("missing X-RateLimit-Limit header")
	} else {
		t.Logf("X-RateLimit-Limit: %s", limit)
		// Search endpoint should have 100/min limit (burst 20)
		if limit != "20" {
			t.Errorf("expected limit 20 for search, got %s", limit)
		}
	}
}

func TestE2EMinRAMFilter(t *testing.T) {
	srv := setupE2EServer(t)

	// Publish packages with different RAM requirements
	packages := []map[string]interface{}{
		{
			"name": "light-pkg", "version": "1.0.0", "sha256": "aaaa",
			"download_urls": map[string]string{"": "https://example.com/light.cgp"},
			"hardware_requirements": map[string]interface{}{"min_ram_mb": 256},
		},
		{
			"name": "medium-pkg", "version": "1.0.0", "sha256": "bbbb",
			"download_urls": map[string]string{"": "https://example.com/medium.cgp"},
			"hardware_requirements": map[string]interface{}{"min_ram_mb": 1024},
		},
		{
			"name": "heavy-pkg", "version": "1.0.0", "sha256": "cccc",
			"download_urls": map[string]string{"": "https://example.com/heavy.cgp"},
			"hardware_requirements": map[string]interface{}{"min_ram_mb": 4096, "min_storage_mb": 2000},
		},
	}

	for _, pkg := range packages {
		data, _ := json.Marshal(pkg)
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/v1/patches", bytes.NewReader(data))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer e2e-token")
		r.Header.Set("User-Agent", "cpm/e2e")
		srv.ServeHTTP(w, r)
		if w.Code != 201 {
			t.Fatalf("failed to publish %s: %d %s", pkg["name"], w.Code, w.Body.String())
		}
	}

	// Search with min_ram_mb filter
	code, body := e2eReq(srv, "GET", "/v1/search?q=&min_ram_mb=500", nil, nil)
	if code != 200 {
		t.Fatalf("search failed: %d", code)
	}
	results := body["results"].([]interface{})
	if len(results) != 2 {
		t.Errorf("expected 2 packages with min_ram_mb>=500, got %d", len(results))
	}

	// Search with min_storage_mb filter
	code, body = e2eReq(srv, "GET", "/v1/search?q=&min_storage_mb=1500", nil, nil)
	if code != 200 {
		t.Fatalf("search failed: %d", code)
	}
	results = body["results"].([]interface{})
	if len(results) != 1 {
		t.Errorf("expected 1 package with min_storage_mb>=1500, got %d", len(results))
	}
	if len(results) > 0 {
		r := results[0].(map[string]interface{})
		if r["name"] != "heavy-pkg" {
			t.Errorf("expected heavy-pkg, got %s", r["name"])
		}
	}
}

func TestE2EDependenciesTransitive(t *testing.T) {
	srv := setupE2EServer(t)

	// Create a dependency chain: root -> mid -> leaf
	pkgs := []map[string]interface{}{
		{
			"name": "leaf-dep", "version": "1.0.0", "sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"download_urls": map[string]string{"": "https://example.com/leaf.cgp"},
			"manifest": map[string]interface{}{"name": "leaf-dep", "version": "1.0.0", "description": "leaf dep"},
		},
		{
			"name": "mid-dep", "version": "1.0.0", "sha256": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			"download_urls": map[string]string{"": "https://example.com/mid.cgp"},
			"manifest": map[string]interface{}{
				"name": "mid-dep", "version": "1.0.0", "description": "mid dep",
				"dependencies": map[string]interface{}{"leaf-dep": "^1.0.0"},
			},
		},
		{
			"name": "root-pkg", "version": "1.0.0", "sha256": "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			"download_urls": map[string]string{"": "https://example.com/root.cgp"},
			"manifest": map[string]interface{}{
				"name": "root-pkg", "version": "1.0.0", "description": "root pkg",
				"dependencies": map[string]interface{}{"mid-dep": "^1.0.0"},
			},
		},
	}

	for _, pkg := range pkgs {
		data, _ := json.Marshal(pkg)
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/v1/patches", bytes.NewReader(data))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer e2e-token")
		r.Header.Set("User-Agent", "cpm/e2e")
		srv.ServeHTTP(w, r)
		if w.Code != 201 {
			t.Fatalf("failed to publish %s: %d %s", pkg["name"], w.Code, w.Body.String())
		}
	}

	// Get dependencies for root
	code, body := e2eReq(srv, "GET", "/v1/patches/root-pkg/dependencies", nil, nil)
	if code != 200 {
		t.Fatalf("deps failed: %d", code)
	}

	// Check flat map
	deps := body["dependencies"].(map[string]interface{})
	if _, ok := deps["mid-dep"]; !ok {
		t.Error("expected mid-dep in dependencies")
	}

	// Check transitive
	transitive := body["transitive"].([]interface{})
	if len(transitive) != 2 {
		t.Errorf("expected 2 transitive deps, got %d: %v", len(transitive), transitive)
	}

	// Check status
	if body["status"] == nil {
		t.Error("missing status field")
	}

	t.Logf("Dependencies: %v", deps)
	t.Logf("Transitive: %v", transitive)
}

func TestE2EMultiVariantDownload(t *testing.T) {
	srv := setupE2EServer(t)

	// Publish with multi-variant URLs
	publishBody := map[string]interface{}{
		"name": "multi-arch", "version": "1.0.0", "sha256": "dddd",
		"download_urls": map[string]string{
			"linux/amd64": "https://example.com/multi-linux-amd64.cgp",
			"linux/arm64": "https://example.com/multi-linux-arm64.cgp",
		},
	}
	data, _ := json.Marshal(publishBody)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/patches", bytes.NewReader(data))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer e2e-token")
	r.Header.Set("User-Agent", "cpm/e2e")
	srv.ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatalf("publish failed: %d %s", w.Code, w.Body.String())
	}

	// Download with os/arch params
	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/v1/patches/multi-arch/1.0.0/download?os=linux&arch=arm64", nil)
	r.Header.Set("User-Agent", "cpm/e2e")
	srv.ServeHTTP(w, r)
	if w.Code != http.StatusFound {
		t.Errorf("expected 302 redirect, got %d", w.Code)
	}
Location := w.Header().Get("Location")
	if Location != "https://example.com/multi-linux-arm64.cgp" {
		t.Errorf("wrong redirect URL: %s", Location)
	}
	t.Logf("Download redirect: %s", Location)
}
