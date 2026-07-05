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
	addr := flag.String("addr", ":8080", "listen address")
	dataDir := flag.String("data-dir", "./data", "data directory for persistent store")
	sqlite := flag.Bool("sqlite", false, "use SQLite backend (default: memory)")
	flag.Parse()

	var st store.Store
	if *sqlite {
		log.Printf("Using file-backed store: %s", *dataDir+"/patches.json")
		st = store.NewFileStore(*dataDir + "/patches.json")
	} else {
		st = store.NewMemoryStore()
	}

	tokenStore := auth.NewMemoryTokenStore()
	_ = tokenStore.Add("test-token", "publish", "admin")

	cfg := server.Config{
		Addr:      *addr,
		DataDir:   *dataDir,
		Store:     st,
		TokenAuth: tokenStore,
	}

	srv := server.New(cfg)

	httpServer := &http.Server{
		Addr:    *addr,
		Handler: srv,
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
