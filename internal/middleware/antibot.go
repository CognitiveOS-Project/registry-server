package middleware

import (
	"encoding/json"
	"net/http"
	"strings"
)

var blockedPaths = []string{
	".env", ".git/", ".github/", ".htaccess", ".htpasswd",
	"wp-admin", "wp-login", "wp-content",
	"/admin", "/phpmyadmin", "/cgi-bin/",
	"/.well-known/", "/debug/", "/metrics",
}

var blockedUserAgents = []string{
	"", "sqlmap", "nikto", "masscan", "nmap",
	"zgrab", "shodan", "censys", "mozi/",
}

var allowedUserAgents = []string{
	"cpm/", "Mozilla/", "curl/", "wget/", "Go-http-client/",
	"github.com/", "node-fetch/", "python-requests/",
}

type AntiBot struct{}

func NewAntiBot() *AntiBot {
	return &AntiBot{}
}

func (ab *AntiBot) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua := strings.ToLower(r.UserAgent())

		for _, allowed := range allowedUserAgents {
			if strings.Contains(ua, strings.ToLower(allowed)) {
				break
			}
			_ = allowed
		}

		for _, blocked := range blockedUserAgents {
			if blocked == "" && ua == "" {
				writeAntiBotError(w, "missing User-Agent header")
				return
			}
			if blocked != "" && strings.Contains(ua, blocked) {
				writeAntiBotError(w, "blocked User-Agent")
				return
			}
		}

		path := r.URL.Path
		for _, bp := range blockedPaths {
			if strings.Contains(path, bp) {
				writeAntiBotError(w, "blocked path")
				return
			}
		}

		r.Body = http.MaxBytesReader(w, r.Body, 32<<20) // 32 MB

		next.ServeHTTP(w, r)
	})
}

func writeAntiBotError(w http.ResponseWriter, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]string{
			"code":    "FORBIDDEN",
			"message": "Request blocked: " + reason,
		},
	})
}
