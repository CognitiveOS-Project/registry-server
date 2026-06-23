package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTokenCheck(t *testing.T) {
	ts := NewMemoryTokenStore()
	ts.Add("valid-token")

	if !ts.Check("valid-token") {
		t.Error("expected valid-token to be found")
	}
	if ts.Check("invalid-token") {
		t.Error("expected invalid-token to not be found")
	}
}

func TestTokenAddAndRemove(t *testing.T) {
	ts := NewMemoryTokenStore()

	if err := ts.Add("new-token"); err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	if !ts.Check("new-token") {
		t.Error("expected new-token to be found after Add")
	}

	if err := ts.Remove("new-token"); err != nil {
		t.Fatalf("Remove failed: %v", err)
	}
	if ts.Check("new-token") {
		t.Error("expected new-token to not be found after Remove")
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
	ts.Add("good-token")

	var called bool
	handler := RequireAuth(ts, func(w http.ResponseWriter, r *http.Request) {
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
	ts.Add("good-token")

	var called bool
	handler := RequireAuth(ts, func(w http.ResponseWriter, r *http.Request) {
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

func TestRequireAuthNoToken(t *testing.T) {
	ts := NewMemoryTokenStore()
	ts.Add("good-token")

	var called bool
	handler := RequireAuth(ts, func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	handler(w, r)

	if called {
		t.Error("expected handler NOT to be called")
	}
}
