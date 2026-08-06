// Command vellum 是这个站的唯一二进制。
//
// 两个子命令：
//
//	vellum serve            起 HTTP 服务
//	vellum migrate up|down|status
//
// 迁移刻意是一个独立的子命令而不是服务启动时的一步：部署流程是 pull → migrate →
// up -d，迁移失败就中止、不切换应用版本，应用因此不会陷入崩溃循环。
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

	// 把时区库编进二进制。零成本，换来的是将来构建成 scratch/alpine 镜像时
	// time.LoadLocation(SITE_TIMEZONE) 不会在生产上找不到 /usr/share/zoneinfo
	// ——而这个失败在本地永远复现不了。
	_ "time/tzdata"

	"vellum/internal/api"
	"vellum/internal/platform/config"
	"vellum/internal/platform/db"
	"vellum/internal/platform/httpx"
	"vellum/internal/platform/log"
)

// gitSHA 由 make build-release 用 -ldflags -X 注入（因此必须是 var，const 注入不进去）。
// 本地 go run 与 make build 都不注入，留着这个默认值。
// 它只出现在启动日志里，不进 /api/healthz——健康检查回答的是「活着吗」，
// 而不是「你是哪一版」，后者看日志。
var gitSHA = "dev"

const usage = `用法：vellum <命令>

命令：
  serve                起 HTTP 服务
  migrate up           迁移到最新版本
  migrate down         回滚一个版本
  migrate status       查看各迁移的应用状态
`

func main() {
	// Ctrl-C 与 SIGTERM 取消这个 context，交给各子命令自己决定怎么收场。
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

	// 配置在任何子命令之前加载：缺变量就在这里失败，而不是让某个子命令跑到一半
	// 才发现自己少了点什么。
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

// shutdownTimeout 是收到 SIGTERM 后留给在途请求的时间。
const shutdownTimeout = 10 * time.Second

// runServe 装配并启动 HTTP 服务。
//
// 这里是唯一的装配处：手写 new，不引入 DI 框架。
func runServe(ctx context.Context, cfg config.Config) error {
	logger := log.New(cfg.IsDev())

	pool, err := db.Open(ctx, cfg.DSN())
	if err != nil {
		return err
	}
	defer pool.Close()

	e := httpx.New(logger)
	// 契约里 servers 写的是 /api，路径写的是 /healthz；前缀在这里补上，
	// 两边合起来才是 GET /api/healthz。
	api.RegisterHandlersWithBaseURL(e, api.NewHandler(pool, logger), "/api")

	// Start 会阻塞，因此放进 goroutine，让主流程去等信号。
	// 缓冲为 1：没人接收时 Start 的返回值也不会把这个 goroutine 泄漏掉。
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
		// 端口被占用一类的启动失败。ErrServerClosed 只会出现在 Shutdown 之后，
		// 走不到这个分支。
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("http 服务：%w", err)
	case <-ctx.Done():
		logger.Info("shutting down")
	}

	// 用 Background 而不是已经被取消的 ctx——否则「优雅关闭」会在开始的那一刻就超时。
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
	// 动作名的合法性由 db.Migrate 判定——这里不重复一份动作清单。
	return db.Migrate(ctx, cfg.DSN(), db.MigrateAction(args[0]))
}
