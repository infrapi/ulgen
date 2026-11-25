// Command ulgen serves one MCP endpoint in front of the platform's APIs:
// see internal/gateway. Its settings come from the dotenv file
// INFRAPI_CONFIG_DOTENV_FILE names (internal/config).
//
//	INFRAPI_CONFIG_DOTENV_FILE=/etc/ulgen/ulgen.env ulgen
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/infrapi/lib/pkg/auth"
	"github.com/infrapi/lib/pkg/listener"
	"github.com/infrapi/lib/pkg/logger"
	"github.com/infrapi/lib/pkg/server"

	"github.com/infrapi/ulgen/internal/config"
	"github.com/infrapi/ulgen/internal/gateway"
	"github.com/infrapi/ulgen/internal/kayra"
	"github.com/infrapi/ulgen/internal/metrics"
)

// version is set at build time: -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ulgen:", err)
		os.Exit(1)
	}
}

func run() (err error) {
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		_, err := fmt.Println(version)
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log, err := logger.New(logger.Options{Level: cfg.App.AppLogLevel.String(), Format: cfg.App.AppLogFormat})
	if err != nil {
		return err
	}
	logger.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	client, err := auth.NewClient(auth.ClientOptions{BaseURL: cfg.AuthURL, CacheTTL: cfg.AuthCacheTTL, PublicURL: cfg.App.AppUrl})
	if err != nil {
		return err
	}
	root := cfg.Root()
	// a 401 names <root>/-/oauth-protected-resource: an MCP client logs in at
	// Kayra from there
	opts := []server.Option{server.WithProtectedResource(client.AuthorizationServer)}
	if cfg.Resource != "" {
		// the gateway's: the token is then good for the APIs too
		opts = append(opts, server.WithResource(cfg.Resource))
	}
	engine, err := server.NewServerGin(cfg.App, []string{root}, opts...)
	if err != nil {
		return err
	}
	g := gateway.New(gateway.Options{
		Catalog:    kayra.New(client, cfg.AuthURL, 30*time.Second),
		GatewayURL: cfg.GatewayURL,
		Serves:     cfg.Serves,
		ToolsTTL:   cfg.ToolsTTL,
		Logger:     log,
		Metrics:    metrics.New(prometheus.DefaultRegisterer, version),
	})
	defer g.Close()
	// under <root>/-/, open unless a route says otherwise: these need a token
	engine.Any(root+server.MCPPath, client.Middleware(), g.Serve)
	engine.Any(root+server.MCPPath+"/:api", client.Middleware(), g.Serve)

	socket, err := listener.Listen(cfg.App)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: engine, ReadHeaderTimeout: 10 * time.Second}
	served := make(chan error, 1)
	go func() {
		log.Info("listening", logger.String("version", version), logger.String("addr", socket.Addr().String()),
			logger.String("mcp", cfg.App.AppUrl+root+server.MCPPath), logger.String("kayra", cfg.AuthURL))
		served <- srv.Serve(socket)
	}()

	select {
	case err := <-served:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve: %w", err)
		}
		return nil
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	log.Info("stopping")
	if err := srv.Shutdown(shutdown); err != nil {
		return errors.Join(fmt.Errorf("shutdown: %w", err), srv.Close())
	}
	return nil
}
