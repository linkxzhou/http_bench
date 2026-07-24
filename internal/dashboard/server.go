// Package dashboard hosts the embedded web dashboard and the worker HTTP API
// for the http_bench tool. It owns the *http.Server lifecycle (start, signal
// handling, graceful shutdown) so the main package does not need to know
// about server plumbing.
package dashboard

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/linkxzhou/http_bench/internal/distributed"
	"github.com/linkxzhou/http_bench/internal/logging"
)

// Config controls the dashboard server. Zero values are valid defaults
// (Addr empty → error, IdleTimeout 0 → 90s).
type Config struct {
	Addr              string
	HTML              string
	WorkerAPIPath     string
	WorkerService     distributed.WorkerService
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
}

func (c *Config) applyDefaults() {
	if c.ReadHeaderTimeout == 0 {
		c.ReadHeaderTimeout = 10 * time.Second
	}
	if c.ReadTimeout == 0 {
		c.ReadTimeout = 30 * time.Second
	}
	if c.WriteTimeout == 0 {
		c.WriteTimeout = 30 * time.Second
	}
	if c.IdleTimeout == 0 {
		c.IdleTimeout = 90 * time.Second
	}
}

// Run starts the server and blocks until SIGINT/SIGTERM, at which point it
// triggers a graceful shutdown bounded by the supplied context. It returns
// the underlying error from ListenAndServe (other than http.ErrServerClosed,
// which indicates a normal shutdown).
func Run(ctx context.Context, cfg Config) error {
	cfg.applyDefaults()
	if cfg.Addr == "" {
		return fmt.Errorf("dashboard: empty listen address")
	}
	if cfg.WorkerService == nil {
		return fmt.Errorf("dashboard: nil WorkerService")
	}
	mux := http.NewServeMux()
	apiPath := "/api" + cfg.WorkerAPIPath
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(cfg.HTML))
	})
	mux.HandleFunc(apiPath, func(w http.ResponseWriter, r *http.Request) {
		distributed.ServeRequest(cfg.WorkerService, distributed.DefaultValidator, w, r)
	})
	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	}
	fmt.Printf("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Printf("Dashboard URL: http://%s/\n", cfg.Addr)
	fmt.Printf("Worker API: http://%s%s\n", cfg.Addr, apiPath)
	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")

	shutdownCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			logging.Info(0, "dashboard: received stop signal, shutting down")
		case <-shutdownCtx.Done():
		}
		cancel()
	}()

	serverErr := make(chan error, 1)
	go func() { serverErr <- server.ListenAndServe() }()
	select {
	case <-shutdownCtx.Done():
		gracefulCtx, cancelGraceful := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelGraceful()
		_ = server.Shutdown(gracefulCtx)
		return nil
	case err := <-serverErr:
		if err != nil && err != http.ErrServerClosed {
			logging.Error(0, "dashboard: server error: %v", err)
			return err
		}
		return nil
	}
}
