package server

import (
	"crypto/rand"
	"encoding/hex"
	"html/template"
	"log"
	"net/http"
	"net/url"

	"github.com/CognitiveOS-Project/registry-server/internal/auth"
	"github.com/CognitiveOS-Project/registry-server/internal/server/templates"
)

var (
	landingTmpl   = template.Must(template.ParseFS(templates.TemplateFS, "landing.html"))
	dashboardTmpl = template.Must(template.ParseFS(templates.TemplateFS, "dashboard.html"))
)

type UIHandlers struct {
	OAuth   *auth.GitHubOAuth
	Owners  auth.OwnerStore
	Keys    auth.SSHKeyStore
	Session *SessionMiddleware
}

func NewUIHandlers(oauth *auth.GitHubOAuth, owners auth.OwnerStore, keys auth.SSHKeyStore, session *SessionMiddleware) *UIHandlers {
	return &UIHandlers{
		OAuth:   oauth,
		Owners:  owners,
		Keys:    keys,
		Session: session,
	}
}

func (u *UIHandlers) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /ui/", u.handleLanding)
	mux.HandleFunc("GET /ui/login", u.handleLogin)
	mux.HandleFunc("GET /ui/callback", u.handleCallback)
	mux.HandleFunc("GET /ui/logout", u.handleLogout)
	mux.HandleFunc("GET /ui/dashboard", u.Session.RequireSession(u.handleDashboard))
	mux.HandleFunc("POST /ui/keys/add", u.Session.RequireSession(u.handleAddKey))
	mux.HandleFunc("GET /ui/keys/", u.handleKeyAction)
}

func (u *UIHandlers) handleLanding(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/ui/" {
		http.NotFound(w, r)
		return
	}
	if err := landingTmpl.Execute(w, nil); err != nil {
		log.Printf("render landing: %v", err)
	}
}

func (u *UIHandlers) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	state := make([]byte, 16)
	if _, err := rand.Read(state); err != nil {
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	stateHex := hex.EncodeToString(state)

	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    stateHex,
		Path:     "/",
		MaxAge:   600,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	http.Redirect(w, r, u.OAuth.AuthURL(stateHex), http.StatusSeeOther)
}

func (u *UIHandlers) handleCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "Missing code", http.StatusBadRequest)
		return
	}

	stateCookie, err := r.Cookie("oauth_state")
	if err != nil || stateCookie.Value != r.URL.Query().Get("state") {
		http.Error(w, "Invalid state", http.StatusBadRequest)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:   "oauth_state",
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})

	token, err := u.OAuth.ExchangeCode(r.Context(), code)
	if err != nil {
		log.Printf("oauth exchange: %v", err)
		http.Error(w, "Failed to authenticate", http.StatusInternalServerError)
		return
	}

	user, err := u.OAuth.FetchUser(r.Context(), token)
	if err != nil {
		log.Printf("oauth fetch user: %v", err)
		http.Error(w, "Failed to get user info", http.StatusInternalServerError)
		return
	}

	owner, err := u.Owners.GetByGitHubID(user.ID)
	if err != nil {
		owner = &auth.Owner{
			GitHubID:   user.ID,
			GitHubUser: user.Login,
			AvatarURL:  user.AvatarURL,
			Keys:       []auth.OwnerKey{},
		}
		if err := u.Owners.Save(owner); err != nil {
			log.Printf("save new owner: %v", err)
			http.Error(w, "Internal error", http.StatusInternalServerError)
			return
		}
	} else {
		owner.GitHubUser = user.Login
		owner.AvatarURL = user.AvatarURL
		_ = u.Owners.Save(owner)
	}

	session := &Session{
		GitHubID:   user.ID,
		GitHubUser: user.Login,
		AvatarURL:  user.AvatarURL,
	}
	if err := u.Session.CreateSession(w, session); err != nil {
		log.Printf("create session: %v", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/ui/dashboard", http.StatusSeeOther)
}

func (u *UIHandlers) handleLogout(w http.ResponseWriter, r *http.Request) {
	u.Session.ClearSession(w)
	http.Redirect(w, r, "/ui/login", http.StatusSeeOther)
}

func (u *UIHandlers) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	session, err := u.Session.GetSession(r)
	if err != nil {
		http.Redirect(w, r, "/ui/login", http.StatusSeeOther)
		return
	}

	owner, err := u.Owners.GetByGitHubID(session.GitHubID)
	if err != nil {
		owner = &auth.Owner{Keys: []auth.OwnerKey{}}
	}

	data := map[string]interface{}{
		"Session": session,
		"Keys":    owner.Keys,
	}

	if err := dashboardTmpl.Execute(w, data); err != nil {
		log.Printf("render dashboard: %v", err)
	}
}

func (u *UIHandlers) handleAddKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	session, err := u.Session.GetSession(r)
	if err != nil {
		http.Redirect(w, r, "/ui/login", http.StatusSeeOther)
		return
	}

	displayName := r.FormValue("display_name")
	publicKey := r.FormValue("public_key")

	if displayName == "" || publicKey == "" {
		u.renderDashboard(w, session, "Display name and public key are required", true)
		return
	}

	info, err := u.Keys.Register(publicKey)
	if err != nil {
		log.Printf("register key: %v", err)
		u.renderDashboard(w, session, "Failed to register key", true)
		return
	}

	key := auth.OwnerKey{
		Fingerprint: info.Fingerprint,
		PublicKey:   publicKey,
		DisplayName: displayName,
		Status:      "active",
	}

	if err := u.Owners.AddKey(session.GitHubID, key); err != nil {
		log.Printf("add key to owner: %v", err)
		u.renderDashboard(w, session, "Failed to link key to your account", true)
		return
	}

	u.renderDashboard(w, session, "Machine key linked successfully", false)
}

func (u *UIHandlers) handleKeyAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	session, err := u.Session.GetSession(r)
	if err != nil {
		http.Redirect(w, r, "/ui/login", http.StatusSeeOther)
		return
	}

	path := r.URL.Path
	fingerprint := ""

	if len(path) > len("/ui/keys/") {
		rest := path[len("/ui/keys/"):]
		actionEnd := len(rest)
		for i, c := range rest {
			if c == '/' {
				actionEnd = i
				break
			}
		}
		fingerprint = rest[:actionEnd]
	}

	if fingerprint == "" {
		http.Error(w, "Invalid key path", http.StatusBadRequest)
		return
	}

	if decoded, err := url.PathUnescape(fingerprint); err == nil {
		fingerprint = decoded
	}

	action := ""
	if len(path) > len("/ui/keys/"+fingerprint+"/") {
		action = path[len("/ui/keys/" + fingerprint + "/"):]
	}

	switch action {
	case "revoke":
		if err := u.Owners.SetKeyStatus(session.GitHubID, fingerprint, "revoked"); err != nil {
			u.renderDashboard(w, session, "Failed to revoke key", true)
			return
		}
		u.renderDashboard(w, session, "Key revoked", false)
	case "activate":
		if err := u.Owners.SetKeyStatus(session.GitHubID, fingerprint, "active"); err != nil {
			u.renderDashboard(w, session, "Failed to activate key", true)
			return
		}
		u.renderDashboard(w, session, "Key activated", false)
	case "grant-publish":
		if err := u.Owners.SetPublishPermission(session.GitHubID, fingerprint, true); err != nil {
			u.renderDashboard(w, session, "Failed to grant publish permission", true)
			return
		}
		u.renderDashboard(w, session, "Publish permission granted", false)
	case "revoke-publish":
		if err := u.Owners.SetPublishPermission(session.GitHubID, fingerprint, false); err != nil {
			u.renderDashboard(w, session, "Failed to revoke publish permission", true)
			return
		}
		u.renderDashboard(w, session, "Publish permission revoked", false)
	case "remove":
		if err := u.Owners.RemoveKey(session.GitHubID, fingerprint); err != nil {
			u.renderDashboard(w, session, "Failed to remove key", true)
			return
		}
		u.renderDashboard(w, session, "Key removed", false)
	default:
		http.Error(w, "Unknown action", http.StatusBadRequest)
	}
}

func (u *UIHandlers) renderDashboard(w http.ResponseWriter, session *Session, msg string, isErr bool) {
	owner, err := u.Owners.GetByGitHubID(session.GitHubID)
	if err != nil {
		owner = &auth.Owner{Keys: []auth.OwnerKey{}}
	}

	type keyView struct {
		auth.OwnerKey
		FingerprintURL string
	}
	keys := make([]keyView, len(owner.Keys))
	for i, k := range owner.Keys {
		keys[i] = keyView{OwnerKey: k, FingerprintURL: url.PathEscape(k.Fingerprint)}
	}

	data := map[string]interface{}{
		"Session": session,
		"Keys":    keys,
		"Message": msg,
		"Error":   isErr,
	}

	if err := dashboardTmpl.Execute(w, data); err != nil {
		log.Printf("render dashboard: %v", err)
	}
}
