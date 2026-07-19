package github

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_Enabled(t *testing.T) {
	c := &Client{Org: "test-org", Token: ""}
	if c.Enabled() {
		t.Error("expected disabled when no token")
	}
	c.Token = "ghp_test"
	if !c.Enabled() {
		t.Error("expected enabled when token set")
	}
}

func TestPublishPackage(t *testing.T) {
	var repoCreated bool
	var releaseCreated bool
	var assetUploaded bool

	mux := http.NewServeMux()

	mux.HandleFunc("GET /repos/test-org/hello-world", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	})

	mux.HandleFunc("POST /orgs/test-org/repos", func(w http.ResponseWriter, r *http.Request) {
		repoCreated = true
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(map[string]interface{}{"name": "hello-world"})
	})

	mux.HandleFunc("POST /repos/test-org/hello-world/releases", func(w http.ResponseWriter, r *http.Request) {
		releaseCreated = true
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":         123,
			"tag_name":   "v1.0.0",
			"name":       "hello-world v1.0.0",
		})
	})

	mux.HandleFunc("POST /repos/test-org/hello-world/releases/123/assets", func(w http.ResponseWriter, r *http.Request) {
		assetUploaded = true
		body, _ := io.ReadAll(r.Body)
		if len(body) < 10 {
			t.Errorf("expected .cgp data, got %d bytes", len(body))
		}
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"browser_download_url": "https://github.com/test-org/hello-world/releases/download/v1.0.0/hello-world-1.0.0.cgp",
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &Client{
		Org:     "test-org",
		Token:   "ghp_test",
		HTTP:    srv.Client(),
		BaseURL: srv.URL,
	}

	cgpData := []byte("fake cgp content with enough data to pass validation")
	result, err := c.PublishPackage("hello-world", "1.0.0", "A test patch", cgpData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !repoCreated {
		t.Error("expected repo to be created")
	}
	if !releaseCreated {
		t.Error("expected release to be created")
	}
	if !assetUploaded {
		t.Error("expected asset to be uploaded")
	}
	if result.DownloadURL == "" {
		t.Error("expected download URL in result")
	}
	if result.ReleaseTag != "v1.0.0" {
		t.Errorf("expected tag v1.0.0, got %s", result.ReleaseTag)
	}
}

func TestPublishPackage_ExistingRepo(t *testing.T) {
	var repoCreated bool

	mux := http.NewServeMux()

	mux.HandleFunc("GET /repos/test-org/existing-pkg", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]interface{}{"name": "existing-pkg"})
	})

	mux.HandleFunc("POST /orgs/test-org/repos", func(w http.ResponseWriter, r *http.Request) {
		repoCreated = true
		w.WriteHeader(201)
	})

	mux.HandleFunc("POST /repos/test-org/existing-pkg/releases", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":       456,
			"tag_name": "v2.0.0",
			"name":     "existing-pkg v2.0.0",
		})
	})

	mux.HandleFunc("POST /repos/test-org/existing-pkg/releases/456/assets", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"browser_download_url": "https://github.com/test-org/existing-pkg/releases/download/v2.0.0/existing-pkg-2.0.0.cgp",
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &Client{
		Org:     "test-org",
		Token:   "ghp_test",
		HTTP:    srv.Client(),
		BaseURL: srv.URL,
	}

	_, err := c.PublishPackage("existing-pkg", "2.0.0", "Existing package", []byte("fake cgp data"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if repoCreated {
		t.Error("should not create repo when it already exists")
	}
}

func TestPublishPackage_NoToken(t *testing.T) {
	c := &Client{Org: "test-org", Token: ""}
	_, err := c.PublishPackage("test", "1.0.0", "desc", []byte("data"))
	if err == nil {
		t.Error("expected error when no token")
	}
}
