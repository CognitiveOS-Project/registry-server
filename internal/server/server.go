package server

import (
	"log"
	"net/http"

	"github.com/CognitiveOS-Project/registry-server/internal/auth"
	"github.com/CognitiveOS-Project/registry-server/internal/middleware"
	"github.com/CognitiveOS-Project/registry-server/internal/store"
)

type Config struct {
	Addr      string
	DataDir   string
	Store     store.Store
	TokenAuth auth.TokenStore
	SSHKeys   auth.SSHKeyStore
}

type Server struct {
	config     Config
	mux        *http.ServeMux
	rateLimit  *middleware.RateLimiter
	antiBot    *middleware.AntiBot
}

func New(config Config) *Server {
	if config.Addr == "" {
		config.Addr = ":8080"
	}
	if config.DataDir == "" {
		config.DataDir = "./data"
	}
	if config.Store == nil {
		config.Store = store.NewMemoryStore()
	}
	if config.TokenAuth == nil {
		config.TokenAuth = auth.NewMemoryTokenStore()
	}
	if config.SSHKeys == nil {
		config.SSHKeys = auth.NewMemorySSHKeyStore()
	}

	s := &Server{
		config:    config,
		mux:       http.NewServeMux(),
		rateLimit: middleware.NewRateLimiter(middleware.DefaultRateLimitConfig()),
		antiBot:   middleware.NewAntiBot(),
	}

	s.routes()
	return s
}

func (s *Server) routes() {
	publishAuth := auth.RequirePublishAuth(s.config.TokenAuth, s.config.SSHKeys, "publish")
	adminAuth := auth.RequirePublishAuth(s.config.TokenAuth, s.config.SSHKeys, "admin")

	s.mux.HandleFunc("GET /v1/health", s.handleHealth())
	s.mux.HandleFunc("GET /v1/search", s.handleSearch())
	s.mux.HandleFunc("GET /v1/patches/{name}", s.handleGetPatch())
	s.mux.HandleFunc("GET /v1/patches/{name}/versions", s.handleGetVersions())
	s.mux.HandleFunc("GET /v1/patches/{name}/{version}", s.handleGetPatch())
	s.mux.HandleFunc("GET /v1/patches/{name}/{version}/download", s.handleDownload())
	s.mux.HandleFunc("GET /v1/patches/{name}/dependencies", s.handleGetDependencies())
	s.mux.HandleFunc("GET /v1/notary/check", s.handleNotaryCheck())

	s.mux.HandleFunc("POST /v1/auth/register", s.handleAuthRegister())

	s.mux.HandleFunc("POST /v1/patches", publishAuth(s.handlePublish()))
	s.mux.HandleFunc("PUT /v1/patches/{name}/{version}", publishAuth(s.handlePutVersion()))

	s.mux.HandleFunc("PATCH /v1/patches/{name}/{version}/status", adminAuth(s.handleSetStatus()))
	s.mux.HandleFunc("POST /v1/patches/{name}/{version}/validate", adminAuth(s.handleValidate()))

	s.mux.HandleFunc("POST /v1/patches/{name}/{version}/unlock", s.handleUnlock())
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-SSH-Fingerprint, X-SSH-Signature")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	handler := http.Handler(s.mux)
	handler = s.rateLimit.Middleware(handler)
	handler = s.antiBot.Middleware(handler)

	handler.ServeHTTP(w, r)
}

func (s *Server) Start() error {
	log.Printf("Starting registry notary on %s", s.config.Addr)
	return http.ListenAndServe(s.config.Addr, s)
}
