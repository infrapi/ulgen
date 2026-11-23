// Command ulgen serves the ulgen MCP gateway: see internal/gateway.
//
//	ulgen -config /etc/ulgen/ulgen.yaml
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/infrapi/lib/pkg/auth"

	"github.com/infrapi/ulgen/internal/config"
	"github.com/infrapi/ulgen/internal/credentials"
	"github.com/infrapi/ulgen/internal/gateway"
	"github.com/infrapi/ulgen/internal/tlsfile"
)

func main() {
	if err := run(); err != nil {
		slog.Error("ulgen stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	path := flag.String("config", "ulgen.yaml", "configuration file")
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
	mcpHandler := g.Handler()
	mux.Handle(cfg.Path, mcpHandler)
	mux.Handle(strings.TrimRight(cfg.Path, "/")+"/", mcpHandler) // <path>/<upstream>
	mux.Handle("/apis/", g.SpecHandler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := &http.Server{Addr: cfg.Listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	serve := srv.ListenAndServe
	if cfg.TLS != nil {
		certs, err := tlsfile.New(cfg.TLS.CertFile, cfg.TLS.KeyFile)
		if err != nil {
			return err
		}
		srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: certs.GetCertificate}
		serve = func() error { return srv.ListenAndServeTLS("", "") }
	}
	errc := make(chan error, 1)
	go func() {
		log.Info("ulgen listening", "address", cfg.Listen, "tls", cfg.TLS != nil, "path", cfg.Path, "upstreams", len(cfg.Upstreams))
		errc <- serve()
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
