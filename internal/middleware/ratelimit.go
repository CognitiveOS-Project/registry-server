package middleware

import (
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type visitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

type RateLimiter struct {
	visitors map[string]*visitor
	mu       sync.Mutex
	rate     rate.Limit
	burst    int
	global   *rate.Limiter
}

type RateLimitConfig struct {
	Rate     rate.Limit
	Burst    int
	Global   rate.Limit
	GlobalBurst int
}

func DefaultRateLimitConfig() RateLimitConfig {
	return RateLimitConfig{
		Rate:     rate.Limit(10.0 / 60), // 10 per minute per IP
		Burst:    10,
		Global:   rate.Limit(30.0 / 60), // 30 per minute global per IP
		GlobalBurst: 30,
	}
}

func NewRateLimiter(cfg RateLimitConfig) *RateLimiter {
	rl := &RateLimiter{
		visitors: make(map[string]*visitor),
		rate:     cfg.Rate,
		burst:    cfg.Burst,
		global:   rate.NewLimiter(cfg.Global, cfg.GlobalBurst),
	}
	go rl.cleanup()
	return rl
}

func (rl *RateLimiter) getVisitor(key string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	v, exists := rl.visitors[key]
	if !exists {
		limiter := rate.NewLimiter(rl.rate, rl.burst)
		rl.visitors[key] = &visitor{limiter: limiter, lastSeen: time.Now()}
		return limiter
	}
	v.lastSeen = time.Now()
	return v.limiter
}

func (rl *RateLimiter) cleanup() {
	for {
		time.Sleep(3 * time.Minute)
		rl.sweep()
	}
}

func (rl *RateLimiter) sweep() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	for ip, v := range rl.visitors {
		if time.Since(v.lastSeen) > 5*time.Minute {
			delete(rl.visitors, ip)
		}
	}
}

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/health" {
			next.ServeHTTP(w, r)
			return
		}

		key := extractClientKey(r)
		limiter := rl.getVisitor(key)

		remaining := int(limiter.Tokens()-1)
		if remaining < 0 {
			remaining = 0
		}

		w.Header().Set("X-RateLimit-Limit", "10")
		w.Header().Set("X-RateLimit-Remaining", itoa(remaining))
		w.Header().Set("X-RateLimit-Reset", itoa64(time.Now().Add(time.Minute).Unix()))

		if !limiter.Allow() {
			w.Header().Set("Retry-After", "60")
			writeRateLimitError(w)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func extractClientKey(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		ip := strings.TrimSpace(parts[0])
		return canonicalizeIP(ip)
	}
	if xri := r.Header.Get("X-Real-Ip"); xri != "" {
		return canonicalizeIP(strings.TrimSpace(xri))
	}
	addr := r.RemoteAddr
	if idx := strings.LastIndex(addr, ":"); idx != -1 {
		addr = addr[:idx]
	}
	return canonicalizeIP(addr)
}

func canonicalizeIP(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return ip
	}
	if parsed4 := parsed.To4(); parsed4 != nil {
		return ip
	}
	// IPv6: mask to /64 subnet
	mask := net.CIDRMask(64, 128)
	subnet := parsed.Mask(mask)
	return subnet.String() + "/64"
}

func writeRateLimitError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]string{
			"code":    "RATE_LIMITED",
			"message": "Rate limit exceeded. Please wait before retrying.",
		},
	})
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

func itoa64(n int64) string {
	return strconv.FormatInt(n, 10)
}
