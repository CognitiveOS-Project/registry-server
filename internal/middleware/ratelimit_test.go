package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestRateLimiterAllowsUnderLimit(t *testing.T) {
	rl := NewRateLimiter(RateLimitConfig{
		Rate:     rate.Limit(10.0 / 60),
		Burst:    10,
		Global:   rate.Limit(30.0 / 60),
		GlobalBurst: 30,
	})

	handler := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/v1/search?q=test", nil)
		r.Header.Set("User-Agent", "cpm/test")
		r.RemoteAddr = "192.168.1.1:12345"
		handler.ServeHTTP(w, r)

		if w.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i, w.Code)
		}
	}
}

func TestRateLimiterRejectsOverLimit(t *testing.T) {
	rl := NewRateLimiter(RateLimitConfig{
		Rate:     rate.Limit(1.0 / 60), // 1 per minute
		Burst:    1,
		Global:   rate.Limit(30.0 / 60),
		GlobalBurst: 30,
	})

	handler := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/search?q=test", nil)
	r.Header.Set("User-Agent", "cpm/test")
	r.RemoteAddr = "192.168.1.1:12345"
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("first request: expected 200, got %d", w.Code)
	}

	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/v1/search?q=test", nil)
	r.Header.Set("User-Agent", "cpm/test")
	r.RemoteAddr = "192.168.1.1:12345"
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("second request: expected 429, got %d", w.Code)
	}
}

func TestRateLimiterSkipsHealth(t *testing.T) {
	rl := NewRateLimiter(RateLimitConfig{
		Rate:     rate.Limit(1.0 / 60),
		Burst:    1,
		Global:   rate.Limit(1.0 / 60),
		GlobalBurst: 1,
	})

	handler := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/v1/health", nil)
		r.Header.Set("User-Agent", "cpm/test")
		r.RemoteAddr = "192.168.1.1:12345"
		handler.ServeHTTP(w, r)

		if w.Code != http.StatusOK {
			t.Errorf("health request %d: expected 200, got %d", i, w.Code)
		}
	}
}

func TestRateLimiterSetsHeaders(t *testing.T) {
	rl := NewRateLimiter(DefaultRateLimitConfig())

	handler := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/search?q=test", nil)
	r.Header.Set("User-Agent", "cpm/test")
	r.RemoteAddr = "192.168.1.1:12345"
	handler.ServeHTTP(w, r)

	if w.Header().Get("X-RateLimit-Limit") == "" {
		t.Error("expected X-RateLimit-Limit header")
	}
	if w.Header().Get("X-RateLimit-Remaining") == "" {
		t.Error("expected X-RateLimit-Remaining header")
	}
	if w.Header().Get("X-RateLimit-Reset") == "" {
		t.Error("expected X-RateLimit-Reset header")
	}
}

func TestRateLimiterIPv6Subnet(t *testing.T) {
	rl := NewRateLimiter(RateLimitConfig{
		Rate:     rate.Limit(1.0 / 60),
		Burst:    1,
		Global:   rate.Limit(30.0 / 60),
		GlobalBurst: 30,
	})

	handler := rl.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1/search?q=test", nil)
	r.Header.Set("User-Agent", "cpm/test")
	r.RemoteAddr = "2001:db8:85a3::8a2e:370:7334:12345"
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("first request: expected 200, got %d", w.Code)
	}

	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/v1/search?q=test", nil)
	r.Header.Set("User-Agent", "cpm/test")
	r.RemoteAddr = "2001:db8:85a3::8a2e:370:7334:54321"
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("same /64 subnet should be rate-limited together: got %d", w.Code)
	}
}

func TestRateLimiterCleanup(t *testing.T) {
	rl := NewRateLimiter(DefaultRateLimitConfig())

	rl.getVisitor("test-ip")
	rl.mu.Lock()
	rl.visitors["test-ip"].lastSeen = time.Now().Add(-10 * time.Minute)
	rl.mu.Unlock()

	rl.sweep()

	rl.mu.Lock()
	_, exists := rl.visitors["test-ip"]
	rl.mu.Unlock()

	if exists {
		t.Error("expected stale visitor to be cleaned up")
	}
}

func TestCanonicalizeIP(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"192.168.1.1", "192.168.1.1"},
		{"2001:db8:85a3::8a2e:370:7334", "2001:db8:85a3::/64"},
		{"::1", "::/64"},
	}

	for _, tt := range tests {
		result := canonicalizeIP(tt.input)
		if result != tt.expected {
			t.Errorf("canonicalizeIP(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}
