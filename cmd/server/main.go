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

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/tagawa0525/app_man/internal/applog"
	"github.com/tagawa0525/app_man/internal/config"
	"github.com/tagawa0525/app_man/internal/db"
)

const binaryName = "appmgr-server"

func main() {
	configPath := flag.String("config", "config.yml", "path to config.yml")
	flag.Parse()

	if err := run(*configPath); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", binaryName, err)
		os.Exit(1)
	}
}

func run(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger, closeLog, err := applog.New(cfg.Logging, binaryName)
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	defer func() {
		if cerr := closeLog(); cerr != nil {
			fmt.Fprintf(os.Stderr, "%s: close log: %v\n", binaryName, cerr)
		}
	}()
	slog.SetDefault(logger)

	sqlDB, closeDB, err := db.Open(cfg.Database)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	// closeDB は closeLog より後に defer 登録する。LIFO で
	// closeDB → closeLog の順に実行され、DB クローズ中のエラーも
	// logger が生きているうちに記録できる。
	defer func() {
		if cerr := closeDB(); cerr != nil {
			logger.Error("close db", slog.Any("error", cerr))
		}
	}()

	if err := db.CheckVersion(sqlDB); err != nil {
		return fmt.Errorf("check schema version: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Get("/healthz", healthHandler)

	srv := &http.Server{
		Addr:              cfg.Server.Listen,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("server starting", slog.String("listen", cfg.Server.Listen))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("server error: %w", err)
		}
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown error", slog.Any("error", err))
		return fmt.Errorf("shutdown: %w", err)
	}
	logger.Info("server stopped")
	return nil
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}
