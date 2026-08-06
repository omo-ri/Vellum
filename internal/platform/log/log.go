// Package log 构造全站唯一的 slog.Logger。
//
// 没有自建的日志抽象，也没有第三方日志库：业务代码直接拿 *slog.Logger。
// 这里唯一的决定是 handler 的形态——开发时要人读，生产上要机器读。
package log

import (
	"log/slog"
	"os"
)

// New 构造 logger。dev 为 true 时用 TextHandler（人眼友好的 key=value 单行），
// 否则用 JSONHandler（便于将来喂给日志收集器）。
//
// 日志一律写 stdout 而不是 stderr：容器化部署下 stdout 就是日志通道，而 stderr
// 留给「进程起不来」这类致命信息（见 main 的 os.Exit 分支），两者不该混在一起。
func New(dev bool) *slog.Logger {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	if dev {
		opts.Level = slog.LevelDebug
		return slog.New(slog.NewTextHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, opts))
}
