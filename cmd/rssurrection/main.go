// Command rssurrection serves the RSSurrection web UI on top of FreshRSS.
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

	"github.com/ldgnu/rssurrection/internal/ai"
	"github.com/ldgnu/rssurrection/internal/buildinfo"
	"github.com/ldgnu/rssurrection/internal/config"
	"github.com/ldgnu/rssurrection/internal/freshrss"
	"github.com/ldgnu/rssurrection/internal/media"
	"github.com/ldgnu/rssurrection/internal/regional"
	"github.com/ldgnu/rssurrection/internal/web"
)

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Printf("rssurrection %s (commit %s, built %s)\n", buildinfo.Version, buildinfo.Commit, buildinfo.Date)
		return
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.FromEnv()
	if err != nil {
		return fmt.Errorf("invalid configuration:\n%w", err)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var client *freshrss.Client
	var src web.DataSource
	if cfg.FreshRSS.Configured() {
		client, err = freshrss.New(freshrss.Options{
			BaseURL:          cfg.FreshRSS.BaseURL,
			User:             cfg.FreshRSS.User,
			Password:         cfg.FreshRSS.APIPassword,
			Timeout:          cfg.FreshRSS.Timeout,
			MaxResponseBytes: cfg.FreshRSS.MaxResponseBytes,
			UserAgent:        "RSSurrection/" + buildinfo.Version + " (+https://github.com/ldgnu/rssurrection)",
		})
		if err != nil {
			return err
		}
		defer client.CloseIdleConnections()
		src = client
	}

	var aiClient *ai.Client
	if cfg.AI.Configured() {
		aiClient, err = ai.New(ai.Options{
			BaseURL: cfg.AI.BaseURL,
			APIKey:  cfg.AI.APIKey,
			Model:   cfg.AI.Model,
			Timeout: cfg.AI.Timeout,
		})
		if err != nil {
			return err
		}
	}

	var regionalSources []regional.Source
	for _, r := range cfg.Regional {
		regionalSources = append(regionalSources, regional.Source{Name: r.Name, URL: r.URL, Lang: r.Lang})
	}
	rf := regional.New(regionalSources, cfg.RegionalLimit)

	srv, err := web.New(cfg, logger, src, media.New(cfg.SigningSecret), aiClient, rf)
	if err != nil {
		return err
	}

	httpSrv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	logger.Info("starting",
		"version", buildinfo.Version,
		"commit", buildinfo.Commit,
		"listen", cfg.ListenAddr,
		"freshrss", cfg.FreshRSS.LogValue(),
		"ai", cfg.AI.LogValue(),
		"regional_sources", len(regionalSources),
		"log_level", cfg.LogLevel.String(),
	)

	errCh := make(chan error, 1)
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("http server: %w", err)
		}
		return nil
	case <-ctx.Done():
		logger.Info("shutdown requested")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	logger.Info("stopped")
	return nil
}
