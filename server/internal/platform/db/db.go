package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"vellum/migrations"
)

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

type MigrateAction string

const (
	MigrateUp     MigrateAction = "up"
	MigrateDown   MigrateAction = "down"
	MigrateStatus MigrateAction = "status"
)

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

func withGoose(ctx context.Context, dsn string, fn func(*sql.DB) error) error {
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("设置 goose dialect：%w", err)
	}

	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("打开迁移连接：%w", err)
	}
	defer func() { _ = sqlDB.Close() }()

	if err := sqlDB.PingContext(ctx); err != nil {
		return fmt.Errorf("连接数据库：%w", err)
	}
	return fn(sqlDB)
}
