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
	global   *rate.Limiter
	routes   map[string]*routeLimiter
}

type routeLimiter struct {
	pattern string
	rate    rate.Limit
	burst   int
}

type RateLimitConfig struct {
	Global       rate.Limit
	GlobalBurst  int
	Routes       []RouteLimit
}

type RouteLimit struct {
	Pattern string
	Rate    float64
	Burst   int
}

func DefaultRateLimitConfig() RateLimitConfig {
	return RateLimitConfig{
		Global:      rate.Limit(30.0 / 60), // 30 per minute global per IP
		GlobalBurst: 30,
		Routes: []RouteLimit{
			{Pattern: "search", Rate: 100.0 / 60, Burst: 20},      // Read: 100/min
			{Pattern: "patches", Rate: 100.0 / 60, Burst: 20},     // Read: 100/min
			{Pattern: "notary", Rate: 30.0 / 60, Burst: 10},       // Notary: 30/min
			{Pattern: "unlock", Rate: 5.0 / 60, Burst: 5},         // Unlock: 5/min
			{Pattern: "auth", Rate: 10.0 / 60, Burst: 5},          // Auth: 10/min
			{Pattern: "download", Rate: 50.0 / 60, Burst: 15},     // Download: 50/min
		},
	}
}

func NewRateLimiter(cfg RateLimitConfig) *RateLimiter {
	rl := &RateLimiter{
		visitors: make(map[string]*visitor),
		global:   rate.NewLimiter(cfg.Global, cfg.GlobalBurst),
		routes:   make(map[string]*routeLimiter),
	}
	for _, r := range cfg.Routes {
		rl.routes[r.Pattern] = &routeLimiter{
			pattern: r.Pattern,
			rate:    rate.Limit(r.Rate),
			burst:   r.Burst,
		}
	}
	go rl.cleanup()
	return rl
}

func (rl *RateLimiter) getVisitor(key string, rt *routeLimiter) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	compositeKey := key
	if rt != nil {
		compositeKey = key + ":" + rt.pattern
	}

	v, exists := rl.visitors[compositeKey]
	if !exists {
		r := rate.Limit(10.0 / 60)
		b := 10
		if rt != nil {
			r = rt.rate
			b = rt.burst
		}
		limiter := rate.NewLimiter(r, b)
		rl.visitors[compositeKey] = &visitor{limiter: limiter, lastSeen: time.Now()}
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
	for k, v := range rl.visitors {
		if time.Since(v.lastSeen) > 5*time.Minute {
			delete(rl.visitors, k)
		}
	}
}

func (rl *RateLimiter) matchRoute(path string) *routeLimiter {
	for _, rt := range rl.routes {
		if strings.Contains(path, rt.pattern) {
			return rt
		}
	}
	return nil
}

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/health" {
			next.ServeHTTP(w, r)
			return
		}

		key := extractClientKey(r)

		if !rl.global.Allow() {
			w.Header().Set("Retry-After", "60")
			writeRateLimitError(w)
			return
		}

		rt := rl.matchRoute(r.URL.Path)
		limiter := rl.getVisitor(key, rt)

		remaining := int(limiter.Tokens()-1)
		if remaining < 0 {
			remaining = 0
		}

		limit := 10
		if rt != nil {
			limit = rt.burst
		}
		w.Header().Set("X-RateLimit-Limit", itoa(limit))
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
