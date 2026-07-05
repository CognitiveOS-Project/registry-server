package server

import (
	"log"
	"net/http"

	"github.com/CognitiveOS-Project/registry-server/internal/auth"
	"github.com/CognitiveOS-Project/registry-server/internal/store"
)

type Config struct {
	Addr      string
	DataDir   string
	Store     store.Store
	TokenAuth auth.TokenStore
}

type Server struct {
	config Config
	mux    *http.ServeMux
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

	s := &Server{
		config: config,
		mux:    http.NewServeMux(),
	}

	s.routes()
	return s
}

func (s *Server) routes() {
	publishAuth := auth.RequireAuth(s.config.TokenAuth, "publish")
	adminAuth := auth.RequireAuth(s.config.TokenAuth, "admin")

	// Public read endpoints
	s.mux.HandleFunc("GET /v1/health", s.handleHealth())
	s.mux.HandleFunc("GET /v1/search", s.handleSearch())
	s.mux.HandleFunc("GET /v1/patches/{name}", s.handleGetPatch())
	s.mux.HandleFunc("GET /v1/patches/{name}/versions", s.handleGetVersions())
	s.mux.HandleFunc("GET /v1/patches/{name}/{version}", s.handleGetPatch())
	s.mux.HandleFunc("GET /v1/patches/{name}/{version}/download", s.handleDownload())
	s.mux.HandleFunc("GET /v1/patches/{name}/dependencies", s.handleGetDependencies())

	// Publish endpoints (require publish scope)
	s.mux.HandleFunc("POST /v1/patches", publishAuth(s.handlePublish()))
	s.mux.HandleFunc("PUT /v1/patches/{name}/{version}", publishAuth(s.handlePutVersion()))

	// Admin endpoints
	s.mux.HandleFunc("PATCH /v1/patches/{name}/{version}/status", adminAuth(s.handleSetStatus()))
	s.mux.HandleFunc("POST /v1/patches/{name}/{version}/validate", adminAuth(s.handleValidate()))

	// Unlock (public)
	s.mux.HandleFunc("POST /v1/unlock", s.handleUnlock())
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	s.mux.ServeHTTP(w, r)
}

func (s *Server) Start() error {
	log.Printf("Starting registry notary on %s", s.config.Addr)
	return http.ListenAndServe(s.config.Addr, s)
}
