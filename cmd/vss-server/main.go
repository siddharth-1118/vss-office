package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vss-suite/vss/server/internal/api"
	"github.com/vss-suite/vss/server/internal/collab"
	"github.com/vss-suite/vss/server/internal/identity"
)

func main() {
	var addr string
	var jwtSecret string
	var dataDir string

	flag.StringVar(&addr, "addr", ":8080", "Server listen address")
	flag.StringVar(&jwtSecret, "jwt-secret", "", "JWT secret key for authentication (required)")
	flag.StringVar(&dataDir, "data-dir", "./data", "Directory for persistent document and metadata storage")
	flag.Parse()

	if jwtSecret == "" {
		fmt.Fprintln(os.Stderr, "error: -jwt-secret is required")
		os.Exit(1)
	}

	// LIMITATION (cmd/vss-server/main.go:34): no metrics endpoint implemented; see cmd/vss-server/main.go for where it would be wired.

	userStore := identity.NewMemoryUserStore()
	if userStore == nil {
		log.Fatalf("boot failed: identity subsystem failed to initialize")
	}

	authSvc := identity.NewService(userStore, []byte(jwtSecret))
	if authSvc == nil {
		log.Fatalf("boot failed: identity service failed to initialize")
	}

	docStore := api.NewFileDocStore(dataDir)
	if docStore == nil {
		log.Fatalf("boot failed: docstore subsystem failed to initialize")
	}

	hub := collab.NewHub(authSvc)
	if hub == nil {
		log.Fatalf("boot failed: hub subsystem failed to initialize")
	}

	srv := api.NewServerWithOptions(api.ServerOptions{
		Hub:      hub,
		Auth:     authSvc,
		DocStore: docStore,
		DataDir:  dataDir,
	})
	if srv == nil || srv.Mux == nil {
		log.Fatalf("boot failed: mux subsystem failed to initialize")
	}

	log.Printf("VSS Server initializing subsystems: identity=ready, docstore=ready (%s), hub=ready, mux=ready, port=%s", dataDir, addr)

	httpServer := &http.Server{
		Addr:         addr,
		Handler:      srv.Mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	go func() {
		log.Printf("VSS Server listening on %s", addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}
	log.Println("Server gracefully stopped")
}
