package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/CognitiveOS-Project/registry-server/internal/auth"
	githubclient "github.com/CognitiveOS-Project/registry-server/internal/github"
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
	if s3Endpoint := os.Getenv("S3_ENDPOINT"); s3Endpoint != "" {
		log.Printf("Using S3 store: bucket=%s endpoint=%s", envOrDefault("S3_BUCKET", "cognitiveos-registry"), s3Endpoint)
		var err error
		st, err = store.NewS3Store(store.S3Config{
			Endpoint:  s3Endpoint,
			Bucket:    envOrDefault("S3_BUCKET", "cognitiveos-registry"),
			AccessKey: os.Getenv("S3_ACCESS_KEY"),
			SecretKey: os.Getenv("S3_SECRET_KEY"),
			Region:    envOrDefault("S3_REGION", "auto"),
		})
		if err != nil {
			log.Fatalf("Failed to create S3 store: %v", err)
		}
	} else if *sqlite {
		log.Printf("Using file-backed store: %s/patches.json", dd)
		st = store.NewFileStore(dd + "/patches.json")
	} else {
		log.Printf("Using in-memory store")
		st = store.NewMemoryStore()
	}

	tokenStore := auth.NewMemoryTokenStore()
	sshKeys := auth.NewMemorySSHKeyStore()

	if trustedKeys := os.Getenv("SSH_TRUSTED_KEYS"); trustedKeys != "" {
		for _, pubKey := range strings.Split(trustedKeys, ",") {
			pubKey = strings.TrimSpace(pubKey)
			if pubKey == "" {
				continue
			}
			info, err := sshKeys.Register(pubKey)
			if err != nil {
				log.Printf("Warning: failed to register trusted key: %v", err)
				continue
			}
			log.Printf("Auth: loaded trusted key %s (%s)", info.Fingerprint, info.Comment)
		}
	}

	ghClient := githubclient.NewClient()
	if ghClient.Enabled() {
		log.Printf("GitHub: integration enabled (org=%s)", ghClient.Org)
	} else {
		log.Printf("GitHub: integration disabled (GITHUB_TOKEN not set)")
	}

	var owners auth.OwnerStore
	if s3Endpoint := os.Getenv("S3_ENDPOINT"); s3Endpoint != "" {
		s3Client, err := newS3Client(s3Endpoint, os.Getenv("S3_ACCESS_KEY"), os.Getenv("S3_SECRET_KEY"), envOrDefault("S3_REGION", "auto"))
		if err != nil {
			log.Fatalf("Failed to create S3 client for owner store: %v", err)
		}
		log.Printf("Using S3 owner store")
		owners = auth.NewS3OwnerStore(s3Client, envOrDefault("S3_BUCKET", "cognitiveos-registry"), "auth/owners")
	} else {
		log.Printf("Using in-memory owner store")
		owners = auth.NewMemoryOwnerStore()
	}

	var sessionMiddleware *server.SessionMiddleware
	sessionSecret := os.Getenv("CRS_SESSION_SECRET")
	if sessionSecret != "" {
		sessionMiddleware = server.NewSessionMiddleware([]byte(sessionSecret))
		log.Printf("Web UI: session enabled")
	} else {
		sessionMiddleware = server.NewSessionMiddleware(nil)
		log.Printf("Web UI: session enabled (random secret)")
	}

	var uiHandlers *server.UIHandlers
	githubClientID := os.Getenv("CRS_GITHUB_CLIENT_ID")
	githubClientSecret := os.Getenv("CRS_GITHUB_CLIENT_SECRET")
	if githubClientID != "" && githubClientSecret != "" {
		redirectURL := envOrDefault("CRS_GITHUB_REDIRECT_URL", "http://localhost:8080/ui/callback")
		oauth := auth.NewGitHubOAuth(githubClientID, githubClientSecret, redirectURL)
		uiHandlers = server.NewUIHandlers(oauth, owners, sshKeys, sessionMiddleware)
		log.Printf("Web UI: GitHub OAuth enabled (redirect=%s)", redirectURL)
	} else {
		log.Printf("Web UI: GitHub OAuth disabled (CRS_GITHUB_CLIENT_ID not set)")
	}

	cfg := server.Config{
		Addr:      *addr,
		DataDir:   dd,
		Store:     st,
		TokenAuth: tokenStore,
		SSHKeys:   sshKeys,
		Owners:    owners,
		GitHub:    ghClient,
		Session:   sessionMiddleware,
		UI:        uiHandlers,
	}

	srv := server.New(cfg)

	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           srv,
		ReadTimeout:       120 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      120 * time.Second,
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

func newS3Client(endpoint, accessKey, secretKey, region string) (*s3.Client, error) {
	awsCfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
	)
	if err != nil {
		return nil, err
	}
	return s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = true
		}
	}), nil
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
