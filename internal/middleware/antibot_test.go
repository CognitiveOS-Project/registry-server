package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAntiBotBlocksEmptyUserAgent(t *testing.T) {
	ab := NewAntiBot()
	handler := ab.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/search?q=test", nil)
	r.RemoteAddr = "192.168.1.1:12345"
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for empty User-Agent, got %d", w.Code)
	}
}

func TestAntiBotBlocksMaliciousUserAgent(t *testing.T) {
	ab := NewAntiBot()
	handler := ab.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	blocked := []string{"sqlmap/1.0", "nikto/2.1", "masscan/1.0", "zgrab/0.x", "shodan/1.0"}
	for _, ua := range blocked {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/v1/search?q=test", nil)
		r.Header.Set("User-Agent", ua)
		r.RemoteAddr = "192.168.1.1:12345"
		handler.ServeHTTP(w, r)

		if w.Code != http.StatusForbidden {
			t.Errorf("expected 403 for User-Agent %q, got %d", ua, w.Code)
		}
	}
}

func TestAntiBotAllowsLegitimateUserAgents(t *testing.T) {
	ab := NewAntiBot()
	handler := ab.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	allowed := []string{"cpm/1.0", "Mozilla/5.0", "curl/7.68.0", "Go-http-client/1.1"}
	for _, ua := range allowed {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/v1/search?q=test", nil)
		r.Header.Set("User-Agent", ua)
		r.RemoteAddr = "192.168.1.1:12345"
		handler.ServeHTTP(w, r)

		if w.Code != http.StatusOK {
			t.Errorf("expected 200 for User-Agent %q, got %d", ua, w.Code)
		}
	}
}

func TestAntiBotBlocksSuspiciousPaths(t *testing.T) {
	ab := NewAntiBot()
	handler := ab.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	blocked := []string{"/.env", "/.git/config", "/wp-admin/", "/phpmyadmin/", "/admin"}
	for _, path := range blocked {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("User-Agent", "cpm/test")
		r.RemoteAddr = "192.168.1.1:12345"
		handler.ServeHTTP(w, r)

		if w.Code != http.StatusForbidden {
			t.Errorf("expected 403 for path %q, got %d", path, w.Code)
		}
	}
}

func TestAntiBotAllowsLegitimatePaths(t *testing.T) {
	ab := NewAntiBot()
	handler := ab.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	allowed := []string{"/v1/search", "/v1/patches", "/v1/health"}
	for _, path := range allowed {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("User-Agent", "cpm/test")
		r.RemoteAddr = "192.168.1.1:12345"
		handler.ServeHTTP(w, r)

		if w.Code != http.StatusOK {
			t.Errorf("expected 200 for path %q, got %d", path, w.Code)
		}
	}
}

func TestAntiBotReturnsJSONError(t *testing.T) {
	ab := NewAntiBot()
	handler := ab.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/search?q=test", nil)
	r.RemoteAddr = "192.168.1.1:12345"
	handler.ServeHTTP(w, r)

	ct := w.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", ct)
	}

	body := w.Body.String()
	if !strings.Contains(body, "FORBIDDEN") {
		t.Errorf("expected FORBIDDEN in response body, got %s", body)
	}
}
