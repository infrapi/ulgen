// Command thanos serves the thanos MCP gateway: see internal/gateway.
//
//	thanos -config /etc/thanos/thanos.yaml
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/infrapi/lib/pkg/auth"

	"github.com/infrapi/thanos/internal/config"
	"github.com/infrapi/thanos/internal/credentials"
	"github.com/infrapi/thanos/internal/gateway"
)

func main() {
	if err := run(); err != nil {
		slog.Error("thanos stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	path := flag.String("config", "thanos.yaml", "configuration file")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(log)

	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	authClient, err := auth.NewClient(auth.ClientOptions{BaseURL: cfg.Kayra.URL, CacheTTL: config.Duration(cfg.Kayra.CacheTTL, time.Minute)})
	if err != nil {
		return err
	}
	opts := gateway.Options{Kayra: gateway.NewKayraClient(authClient, cfg.Kayra.URL, nil), Logger: log}
	if cfg.Vault != nil {
		vc, err := credentials.Login(ctx, cfg.Vault)
		if err != nil {
			return err
		}
		opts.Credentials = credentials.NewVault(vc, config.Duration(cfg.CredentialTTL, time.Minute))
	}

	g, err := gateway.New(cfg, opts)
	if err != nil {
		return err
	}
	defer g.Close()

	mux := http.NewServeMux()
	mux.Handle(cfg.Path, g.Handler())
	mux.Handle("/apis/", g.SpecHandler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := &http.Server{Addr: cfg.Listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() {
		log.Info("thanos listening", "address", cfg.Listen, "path", cfg.Path, "upstreams", len(cfg.Upstreams))
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve: %w", err)
		}
		return nil
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}
