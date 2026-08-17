// Command server runs the Aegis Sample API.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nhatminh06/aegis-api/internal/api"
	"github.com/prometheus/client_golang/prometheus"
)

// warmUp is a fixed, short delay before the pod reports ready. It exists
// to give the liveness/readiness split real behavior to demonstrate even
// though the application has no external dependency to wait on today.
const warmUp = 2 * time.Second

// shutdownGrace is how long the server waits, after readiness is
// withdrawn, before it starts closing in-flight connections. It gives
// Kubernetes/Gateway time to notice the endpoint is gone and stop sending
// new requests before the process actually exits.
const shutdownGrace = 5 * time.Second

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	srv := api.NewServer(prometheus.NewRegistry())
	httpServer := &http.Server{
		Addr:              ":" + port,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		time.Sleep(warmUp)
		srv.SetReady(true)
	}()

	go func() {
		log.Printf("aegis-api listening on :%s", port)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	sig := <-sigCh
	log.Printf("received %s, starting graceful shutdown", sig)

	// Stop accepting new traffic first, then give the platform time to
	// drain the endpoint, before touching in-flight connections at all.
	srv.SetReady(false)
	time.Sleep(shutdownGrace)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
		os.Exit(1)
	}
	log.Print("shutdown complete")
}
