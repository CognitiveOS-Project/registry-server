package auth

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
)

type Token struct {
	Value  string
	Scopes []string
}

type TokenStore interface {
	Get(token string) (*Token, bool)
	Add(token string, scopes ...string) error
	Remove(token string) error
}

type MemoryTokenStore struct {
	mu     sync.RWMutex
	tokens map[string]*Token
}

func NewMemoryTokenStore() *MemoryTokenStore {
	return &MemoryTokenStore{
		tokens: make(map[string]*Token),
	}
}

func (s *MemoryTokenStore) Get(token string) (*Token, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tokens[token]
	return t, ok
}

func (s *MemoryTokenStore) Add(token string, scopes ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(scopes) == 0 {
		scopes = []string{"publish"}
	}
	s.tokens[token] = &Token{Value: token, Scopes: scopes}
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

func RequireAuth(ts TokenStore, requiredScopes ...string) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			tokenVal := ExtractBearerToken(r)
			if tokenVal == "" {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing authorization header")
				return
			}

			token, ok := ts.Get(tokenVal)
			if !ok {
				writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid token")
				return
			}

			if len(requiredScopes) > 0 {
				if !hasScopes(token.Scopes, requiredScopes) {
					writeError(w, http.StatusForbidden, "FORBIDDEN",
						fmt.Sprintf("token requires scope(s): %s", strings.Join(requiredScopes, ", ")))
					return
				}
			}

			next(w, r)
		}
	}
}

func hasScopes(tokenScopes, required []string) bool {
	for _, req := range required {
		found := false
		for _, s := range tokenScopes {
			if s == req {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"error":{"code":"%s","message":"%s"}}`, code, message)
}
