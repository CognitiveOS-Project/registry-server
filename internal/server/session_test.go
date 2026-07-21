package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSessionMiddleware_CreateAndGet(t *testing.T) {
	sm := NewSessionMiddleware([]byte("test-secret-key-123456789012"))

	w := httptest.NewRecorder()
	user := &Session{
		GitHubID:   12345,
		GitHubUser: "octocat",
		AvatarURL:  "https://example.com/avatar.png",
	}

	if err := sm.CreateSession(w, user); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	cookies := w.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "cog_session" {
			sessionCookie = c
			break
		}
	}

	if sessionCookie == nil {
		t.Fatal("session cookie not set")
	}

	if !sessionCookie.HttpOnly {
		t.Error("cookie should be HttpOnly")
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(sessionCookie)

	got, err := sm.GetSession(req)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}

	if got.GitHubID != 12345 {
		t.Errorf("GitHubID = %d, want 12345", got.GitHubID)
	}

	if got.GitHubUser != "octocat" {
		t.Errorf("GitHubUser = %q, want %q", got.GitHubUser, "octocat")
	}

	if got.AvatarURL != "https://example.com/avatar.png" {
		t.Errorf("AvatarURL = %q, want %q", got.AvatarURL, "https://example.com/avatar.png")
	}
}

func TestSessionMiddleware_NoCookie(t *testing.T) {
	sm := NewSessionMiddleware([]byte("test-secret-key-123456789012"))

	req := httptest.NewRequest("GET", "/", nil)

	_, err := sm.GetSession(req)
	if err == nil {
		t.Error("expected error for missing cookie")
	}
}

func TestSessionMiddleware_InvalidSignature(t *testing.T) {
	sm := NewSessionMiddleware([]byte("test-secret-key-123456789012"))

	w := httptest.NewRecorder()
	user := &Session{
		GitHubID:   12345,
		GitHubUser: "octocat",
		AvatarURL:  "https://example.com/avatar.png",
	}

	_ = sm.CreateSession(w, user)

	cookies := w.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "cog_session" {
			sessionCookie = c
			break
		}
	}

	if sessionCookie == nil {
		t.Fatal("session cookie not set")
	}

	sessionCookie.Value = "invalid_signature.12345|b2N0b2NhdA|aHR0cHM6Ly9leGFtcGxlLmNvbS9hdmF0YXIucG5n"

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(sessionCookie)

	_, err := sm.GetSession(req)
	if err == nil {
		t.Error("expected error for invalid signature")
	}
}

func TestSessionMiddleware_ClearSession(t *testing.T) {
	sm := NewSessionMiddleware([]byte("test-secret-key-123456789012"))

	w := httptest.NewRecorder()
	sm.ClearSession(w)

	cookies := w.Result().Cookies()
	var clearedCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "cog_session" {
			clearedCookie = c
			break
		}
	}

	if clearedCookie == nil {
		t.Fatal("clear cookie not set")
	}

	if clearedCookie.MaxAge != -1 {
		t.Errorf("MaxAge = %d, want -1", clearedCookie.MaxAge)
	}
}

func TestSessionMiddleware_RequireSession(t *testing.T) {
	sm := NewSessionMiddleware([]byte("test-secret-key-123456789012"))

	handler := sm.RequireSession(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest("GET", "/protected", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d (redirect to login)", w.Code, http.StatusSeeOther)
	}

	location := w.Header().Get("Location")
	if location != "/ui/login" {
		t.Errorf("Location = %q, want %q", location, "/ui/login")
	}
}

func TestSessionMiddleware_RequireSession_WithValidSession(t *testing.T) {
	sm := NewSessionMiddleware([]byte("test-secret-key-123456789012"))

	w := httptest.NewRecorder()
	user := &Session{
		GitHubID:   12345,
		GitHubUser: "octocat",
		AvatarURL:  "https://example.com/avatar.png",
	}
	_ = sm.CreateSession(w, user)

	cookies := w.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "cog_session" {
			sessionCookie = c
			break
		}
	}

	handler := sm.RequireSession(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	req := httptest.NewRequest("GET", "/protected", nil)
	req.AddCookie(sessionCookie)
	w2 := httptest.NewRecorder()

	handler(w2, req)

	if w2.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w2.Code, http.StatusOK)
	}
}
