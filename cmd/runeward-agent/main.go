// Command runeward-agent runs the in-sandbox agent HTTP server, exposing
// shell, code, and file operations confined to a workspace root.
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

	"github.com/Runewardd/runeward/internal/agent"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8000", "address to listen on (loopback by default; this server has no auth, so binding a wider interface exposes shell/file access)")
	root := flag.String("root", "/workspace", "workspace root that file operations are confined to")
	flag.Parse()

	if err := os.MkdirAll(*root, 0o755); err != nil {
		log.Fatalf("runeward-agent: failed to create root %q: %v", *root, err)
	}

	srv := agent.New(*root)
	httpServer := &http.Server{
		Addr:    *addr,
		Handler: srv.Handler(),
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("runeward-agent: listening on %s (root=%s)", *addr, *root)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("runeward-agent: server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Printf("runeward-agent: shutdown signal received, draining connections")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("runeward-agent: graceful shutdown failed: %v", err)
	}
	log.Printf("runeward-agent: stopped")
}
