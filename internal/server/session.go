package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type SessionMiddleware struct {
	Secret   []byte
	MaxAge   time.Duration
	Path     string
}

type Session struct {
	GitHubID   int64
	GitHubUser string
	AvatarURL  string
}

func NewSessionMiddleware(secret []byte) *SessionMiddleware {
	if len(secret) == 0 {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			panic("generate session secret: " + err.Error())
		}
		secret = b
	}
	return &SessionMiddleware{
		Secret: secret,
		MaxAge: 24 * time.Hour,
		Path:   "/",
	}
}

func (s *SessionMiddleware) Name() string {
	return "cog_session"
}

func (s *SessionMiddleware) CreateSession(w http.ResponseWriter, user *Session) error {
	value := fmt.Sprintf("%d|%s|%s",
		user.GitHubID,
		base64.RawURLEncoding.EncodeToString([]byte(user.GitHubUser)),
		base64.RawURLEncoding.EncodeToString([]byte(user.AvatarURL)),
	)

	mac := hmac.New(sha256.New, s.Secret)
	mac.Write([]byte(value))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	cookie := sig + "." + value

	http.SetCookie(w, &http.Cookie{
		Name:     s.Name(),
		Value:    cookie,
		Path:     s.Path,
		MaxAge:   int(s.MaxAge.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   true,
	})

	return nil
}

func (s *SessionMiddleware) GetSession(r *http.Request) (*Session, error) {
	cookie, err := r.Cookie(s.Name())
	if err != nil {
		return nil, fmt.Errorf("no session cookie")
	}

	parts := strings.SplitN(cookie.Value, ".", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid session format")
	}

	sig, value := parts[0], parts[1]

	mac := hmac.New(sha256.New, s.Secret)
	mac.Write([]byte(value))
	expectedSig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(sig), []byte(expectedSig)) {
		return nil, fmt.Errorf("invalid session signature")
	}

_fields := strings.Split(value, "|")
	if len(_fields) != 3 {
		return nil, fmt.Errorf("invalid session data")
	}

	id, err := strconv.ParseInt(_fields[0], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid github id")
	}

	userBytes, err := base64.RawURLEncoding.DecodeString(_fields[1])
	if err != nil {
		return nil, fmt.Errorf("invalid github user")
	}

	avatarBytes, err := base64.RawURLEncoding.DecodeString(_fields[2])
	if err != nil {
		return nil, fmt.Errorf("invalid avatar url")
	}

	return &Session{
		GitHubID:   id,
		GitHubUser: string(userBytes),
		AvatarURL:  string(avatarBytes),
	}, nil
}

func (s *SessionMiddleware) ClearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.Name(),
		Value:    "",
		Path:     s.Path,
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   true,
	})
}

func (s *SessionMiddleware) RequireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, err := s.GetSession(r)
		if err != nil {
			http.Redirect(w, r, "/ui/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}
