package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "time/tzdata"

	"vellum/internal/api"
	"vellum/internal/platform/config"
	"vellum/internal/platform/db"
	"vellum/internal/platform/httpx"
	"vellum/internal/platform/log"
)

var gitSHA = "dev"

const usage = `用法：vellum <命令>

命令：
  serve                起 HTTP 服务
  migrate up           迁移到最新版本
  migrate down         回滚一个版本
  migrate status       查看各迁移的应用状态
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "vellum: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return errors.New("缺少命令")
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	switch args[0] {
	case "serve":
		return runServe(ctx, cfg)
	case "migrate":
		return runMigrate(ctx, cfg, args[1:])
	default:
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("未知命令 %q", args[0])
	}
}

const shutdownTimeout = 10 * time.Second

func runServe(ctx context.Context, cfg config.Config) error {
	logger := log.New(cfg.IsDev())

	pool, err := db.Open(ctx, cfg.DSN())
	if err != nil {
		return err
	}
	defer pool.Close()

	e := httpx.New(logger)
	api.RegisterHandlersWithBaseURL(e, api.NewHandler(pool, logger), "/api")

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("http server started",
			slog.String("git_sha", gitSHA),
			slog.String("addr", cfg.HTTPAddr),
			slog.String("env", cfg.AppEnv),
			slog.String("timezone", cfg.SiteTimezone.String()),
		)
		serverErr <- e.Start(cfg.HTTPAddr)
	}()

	select {
	case err := <-serverErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("http 服务：%w", err)
	case <-ctx.Done():
		logger.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := e.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("关闭 http 服务：%w", err)
	}
	return nil
}

func runMigrate(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("migrate 需要一个动作：%s、%s 或 %s",
			db.MigrateUp, db.MigrateDown, db.MigrateStatus)
	}
	return db.Migrate(ctx, cfg.DSN(), db.MigrateAction(args[0]))
}
