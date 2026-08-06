// Package db 负责 Postgres 连接池的构造与迁移的执行。
//
// 应用代码一律通过 pgx 原生接口（*pgxpool.Pool）访问数据库，SQL 手写。
// database/sql 在整个项目里只出现一次——就在本文件的 Migrate 里，因为 goose 只认
// database/sql。它经 pgx/v5/stdlib 包装同一个 pgx 驱动，用完即关，不参与请求路径。
package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // 注册 "pgx" driver，仅供下面的 goose 使用
	"github.com/pressly/goose/v3"

	"vellum/migrations"
)

// Open 建立连接池并确认它真的能连上。
//
// pgxpool.New 是惰性的：它不会在返回前建立任何连接，因此一个拼错的 DSN 要到第一次
// 查询才暴露。这里显式 Ping 一次，把「数据库连不上」这件事挪回启动阶段。
func Open(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("构造连接池：%w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("连接数据库：%w", err)
	}
	return pool, nil
}

// MigrateAction 是 migrate 子命令认得的动作。
type MigrateAction string

const (
	MigrateUp     MigrateAction = "up"     // 迁移到最新版本
	MigrateDown   MigrateAction = "down"   // 回滚一个版本
	MigrateStatus MigrateAction = "status" // 打印每个迁移的应用状态
)

// Migrate 执行一个迁移动作。
//
// 「动作名对应哪个 goose 调用」只在下面这个 switch 里出现一次：调用方把命令行上的
// 字符串原样交过来，不必自己再分发一遍，也就不会出现两处各认一半动作的情况。
// 未知动作在这里被拒，因此它同时也是这个字符串的校验点。
func Migrate(ctx context.Context, dsn string, action MigrateAction) error {
	var run func(*sql.DB) error
	switch action {
	case MigrateUp:
		run = func(db *sql.DB) error { return goose.UpContext(ctx, db, ".") }
	case MigrateDown:
		run = func(db *sql.DB) error { return goose.DownContext(ctx, db, ".") }
	case MigrateStatus:
		run = func(db *sql.DB) error { return goose.StatusContext(ctx, db, ".") }
	default:
		return fmt.Errorf("未知的 migrate 动作 %q（可用：%s、%s、%s）",
			string(action), MigrateUp, MigrateDown, MigrateStatus)
	}
	return withGoose(ctx, dsn, run)
}

// withGoose 处理迁移命令的开场与收场：开一个临时的 database/sql 连接、
// 把内嵌的 SQL 交给 goose、用完关掉。
func withGoose(ctx context.Context, dsn string, fn func(*sql.DB) error) error {
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("设置 goose dialect：%w", err)
	}

	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("打开迁移连接：%w", err)
	}
	// 关闭失败无可挽回也无需上报：迁移到这时要么已经提交、要么已经回滚，
	// 而这个连接本来就活不过这个函数。
	defer func() { _ = sqlDB.Close() }()

	if err := sqlDB.PingContext(ctx); err != nil {
		return fmt.Errorf("连接数据库：%w", err)
	}
	return fn(sqlDB)
}
