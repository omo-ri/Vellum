// Package migrations 持有全部 goose 迁移的 SQL，并把它们编进二进制。
//
// 迁移作为部署流程中一个独立、显式的步骤执行（cmd/vellum migrate up），
// 而不是在服务启动时顺带跑——迁移失败时应用不应陷入崩溃循环。
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
