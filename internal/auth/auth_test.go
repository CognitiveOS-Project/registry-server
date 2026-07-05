package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTokenGet(t *testing.T) {
	ts := NewMemoryTokenStore()
	_ = ts.Add("valid-token", "publish")

	tok, ok := ts.Get("valid-token")
	if !ok {
		t.Fatal("expected valid-token to be found")
	}
	if tok.Value != "valid-token" {
		t.Errorf("expected value 'valid-token', got '%s'", tok.Value)
	}
}

func TestTokenGetNotFound(t *testing.T) {
	ts := NewMemoryTokenStore()
	_, ok := ts.Get("nonexistent")
	if ok {
		t.Error("expected nonexistent token to not be found")
	}
}

func TestTokenAddAndRemove(t *testing.T) {
	ts := NewMemoryTokenStore()

	if err := ts.Add("new-token", "publish", "admin"); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	tok, ok := ts.Get("new-token")
	if !ok {
		t.Fatal("expected new-token to be found after Add")
	}
	if len(tok.Scopes) != 2 {
		t.Errorf("expected 2 scopes, got %d", len(tok.Scopes))
	}

	if err := ts.Remove("new-token"); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	if _, ok := ts.Get("new-token"); ok {
		t.Error("expected new-token to not be found after Remove")
	}
}

func TestTokenDefaultScopes(t *testing.T) {
	ts := NewMemoryTokenStore()
	_ = ts.Add("default-token")

	tok, ok := ts.Get("default-token")
	if !ok {
		t.Fatal("expected default-token to be found")
	}
	if len(tok.Scopes) != 1 || tok.Scopes[0] != "publish" {
		t.Errorf("expected default scope [publish], got %v", tok.Scopes)
	}
}

func TestExtractBearerToken(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer my-token")

	token := ExtractBearerToken(r)
	if token != "my-token" {
		t.Errorf("expected 'my-token', got '%s'", token)
	}
}

func TestExtractBearerTokenMissing(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)

	token := ExtractBearerToken(r)
	if token != "" {
		t.Errorf("expected empty string, got '%s'", token)
	}
}

func TestExtractBearerTokenNoBearer(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Basic abc123")

	token := ExtractBearerToken(r)
	if token != "" {
		t.Errorf("expected empty string, got '%s'", token)
	}
}

func TestRequireAuthValidToken(t *testing.T) {
	ts := NewMemoryTokenStore()
	_ = ts.Add("good-token", "publish")

	var called bool
	mw := RequireAuth(ts, "publish")
	handler := mw(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer good-token")
	handler(w, r)

	if !called {
		t.Error("expected handler to be called")
	}
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestRequireAuthInvalidToken(t *testing.T) {
	ts := NewMemoryTokenStore()
	_ = ts.Add("good-token", "publish")

	var called bool
	mw := RequireAuth(ts, "publish")
	handler := mw(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer bad-token")
	handler(w, r)

	if called {
		t.Error("expected handler NOT to be called")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestRequireAuthMissingScope(t *testing.T) {
	ts := NewMemoryTokenStore()
	_ = ts.Add("readonly-token", "read")

	var called bool
	mw := RequireAuth(ts, "publish")
	handler := mw(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer readonly-token")
	handler(w, r)

	if called {
		t.Error("expected handler NOT to be called when scope missing")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestRequireAuthNoToken(t *testing.T) {
	ts := NewMemoryTokenStore()
	_ = ts.Add("good-token", "publish")

	var called bool
	mw := RequireAuth(ts, "publish")
	handler := mw(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	handler(w, r)

	if called {
		t.Error("expected handler NOT to be called")
	}
}
