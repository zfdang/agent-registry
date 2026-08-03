// Command registry-api is the single Go binary serving REST, MCP, and A2A
// over one shared service layer (docs/02-architecture.md).
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/zfdang/agent-registry/pkg/a2aserver"
	"github.com/zfdang/agent-registry/pkg/client/pg"
	"github.com/zfdang/agent-registry/pkg/config"
	"github.com/zfdang/agent-registry/pkg/mcpserver"
	"github.com/zfdang/agent-registry/pkg/middleware"
	"github.com/zfdang/agent-registry/pkg/probe"
	"github.com/zfdang/agent-registry/pkg/server"
	"github.com/zfdang/agent-registry/pkg/service"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "registry-api:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	log, err := buildLogger(cfg.LogLevel)
	if err != nil {
		return fmt.Errorf("logger: %w", err)
	}
	defer log.Sync() //nolint:errcheck
	sugar := log.Sugar()

	store, err := pg.Open(cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}

	probeClient := probe.NewClient(probe.WithAllowlist(cfg.ProbeHostAllowlist))
	svc := service.New(store, probeClient, sugar)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	svc.StartGCTicker(ctx)

	resolver := middleware.NewIPResolver(cfg.TrustedProxyCIDRs)

	engine := server.New(svc, sugar, resolver,
		func(e *gin.Engine, limiters *server.Limiters, resolver *middleware.IPResolver) {
			mcpserver.Mount(e, svc, cfg.PublicURL, limiters.Reads, limiters.Mutating, resolver)
		},
		func(e *gin.Engine, limiters *server.Limiters, resolver *middleware.IPResolver) {
			a2aserver.Mount(e, svc, cfg.PublicURL, limiters.Mutating, limiters.TaskRead, resolver)
		},
	)

	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           engine,
		ReadHeaderTimeout: 10 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		sugar.Infow("listening", "port", cfg.Port, "public_url", cfg.PublicURL)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		return fmt.Errorf("server: %w", err)
	case sig := <-sigCh:
		sugar.Infow("shutting down", "signal", sig.String())
	}

	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}

	return nil
}

func buildLogger(level string) (*zap.Logger, error) {
	var zapLevel zapcore.Level
	if err := zapLevel.Set(level); err != nil {
		zapLevel = zapcore.InfoLevel
	}

	cfg := zap.NewProductionConfig()
	cfg.Level = zap.NewAtomicLevelAt(zapLevel)
	return cfg.Build()
}
