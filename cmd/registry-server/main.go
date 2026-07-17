package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/CognitiveOS-Project/registry-server/internal/auth"
	"github.com/CognitiveOS-Project/registry-server/internal/server"
	"github.com/CognitiveOS-Project/registry-server/internal/store"
)

func main() {
	addr := flag.String("addr", "", "listen address (overrides PORT env)")
	dataDir := flag.String("data-dir", "", "data directory (overrides DATA_DIR env)")
	sqlite := flag.Bool("sqlite", false, "use file-backed store (default: memory)")
	flag.Parse()

	port := envOrDefault("PORT", "8080")
	if *addr == "" {
		*addr = ":" + port
	}
	dd := envOrDefault("DATA_DIR", "./data")
	if *dataDir != "" {
		dd = *dataDir
	}

	var st store.Store
	if *sqlite {
		log.Printf("Using file-backed store: %s/patches.json", dd)
		st = store.NewFileStore(dd + "/patches.json")
	} else {
		st = store.NewMemoryStore()
	}

	tokenStore := auth.NewMemoryTokenStore()

	cfg := server.Config{
		Addr:      *addr,
		DataDir:   dd,
		Store:     st,
		TokenAuth: tokenStore,
	}

	srv := server.New(cfg)

	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           srv,
		ReadTimeout:       10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MB
	}

	go func() {
		log.Printf("Starting registry notary on %s (notary/redirect mode)", *addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		log.Fatalf("forced shutdown: %v", err)
	}

	log.Println("Server stopped")
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
