package auth

import (
	"errors"
	"net/http"
	"strings"
	"sync"
)

var ErrUnauthorized = errors.New("unauthorized")

type TokenStore interface {
	Check(token string) bool
	Add(token string) error
	Remove(token string) error
}

type MemoryTokenStore struct {
	mu     sync.RWMutex
	tokens map[string]bool
}

func NewMemoryTokenStore() *MemoryTokenStore {
	return &MemoryTokenStore{
		tokens: make(map[string]bool),
	}
}

func (s *MemoryTokenStore) Check(token string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tokens[token]
}

func (s *MemoryTokenStore) Add(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[token] = true
	return nil
}

func (s *MemoryTokenStore) Remove(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tokens, token)
	return nil
}

func ExtractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
}

func RequireAuth(ts TokenStore, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := ExtractBearerToken(r)
		if token == "" || !ts.Check(token) {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}
