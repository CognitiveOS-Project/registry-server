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
	s.mux.HandleFunc("GET /v1/health", s.handleHealth())
	s.mux.HandleFunc("GET /v1/search", s.handleSearch())
	s.mux.HandleFunc("GET /v1/patches/{name}/{version}", s.handleGetPatch())
	s.mux.HandleFunc("GET /v1/patches/{name}/{version}/download", s.handleDownload())
	s.mux.HandleFunc("POST /v1/patches", auth.RequireAuth(s.config.TokenAuth, s.handlePublish()))
	s.mux.HandleFunc("POST /v1/unlock", auth.RequireAuth(s.config.TokenAuth, s.handleUnlock()))
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
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
