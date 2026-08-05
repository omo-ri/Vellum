// Package httpx 构造 Echo 实例：中间件链与统一的错误响应都在这里定下来。
//
// 刻意只挂三个中间件——RequestID、访问日志、Recover。没有 CORS（前后端同源）、
// 没有 Gzip（Cloudflare 在前面）、没有 CSRF（前提是 GET 无副作用，见
// docs/architecture.md 的「纪律」）。BodyLimit、Secure 头、IPExtractor 等留到需要
// 它们的那一批。
package httpx

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"vellum/internal/api"
)

// New 构造一个已经装好中间件与错误处理器的 Echo 实例。路由由调用方注册。
func New(logger *slog.Logger) *echo.Echo {
	e := echo.New()

	// Echo 自带的 banner 与 "http server started on ..." 都走它自己的 logger，
	// 绕开 slog。关掉，改由 main 用 slog 打一行。
	e.HideBanner = true
	e.HidePort = true

	e.HTTPErrorHandler = errorHandler(logger)

	// 中间件顺序即嵌套顺序，最先加的在最外层：
	//
	//	RequestID → 访问日志 → Recover → handler
	//
	// RequestID 在最外层，因为后两者都要用它。访问日志在 Recover 之外，是为了让
	// panic 转成的 500 也被记进访问日志——反过来的话，panic 的请求就没有日志行了。
	e.Use(middleware.RequestID())

	e.Use(middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogRequestID:    true,
		LogRemoteIP:     true,
		LogMethod:       true,
		LogURI:          true,
		LogStatus:       true,
		LogLatency:      true,
		LogResponseSize: true,
		LogError:        true,
		// 先把错误交给上面的 HTTPErrorHandler，再记日志——否则状态码还没被决定，
		// 日志里会是一个恒为 200 的假值。
		HandleError:   true,
		LogValuesFunc: logRequest(logger),
	}))

	e.Use(middleware.RecoverWithConfig(middleware.RecoverConfig{
		// 让 panic 变成一个普通的 error 继续向上传，而不是就地调用错误处理器：
		// 这样它会依次经过访问日志中间件与统一错误处理器，跟其他任何 500 走同一条路。
		DisableErrorHandler: true,
		LogErrorFunc: func(c echo.Context, err error, stack []byte) error {
			logger.ErrorContext(c.Request().Context(), "panic recovered",
				slog.String("request_id", requestID(c)),
				slog.String("method", c.Request().Method),
				slog.String("uri", c.Request().RequestURI),
				slog.Any("error", err),
				slog.String("stack", string(stack)),
			)
			return err
		},
	}))

	return e
}

// logRequest 是访问日志的格式定义。
//
// 用 RequestLoggerWithConfig 而不是自带格式的 middleware.Logger：前者负责算耗时、
// 取最终状态码与响应字节数这些脏活，把结果交给回调，格式则 100% 由我们决定。
func logRequest(logger *slog.Logger) func(echo.Context, middleware.RequestLoggerValues) error {
	return func(c echo.Context, v middleware.RequestLoggerValues) error {
		attrs := []slog.Attr{
			slog.String("request_id", v.RequestID),
			slog.String("remote_ip", v.RemoteIP),
			slog.String("method", v.Method),
			slog.String("uri", v.URI),
			slog.Int("status", v.Status),
			slog.Duration("latency", v.Latency),
			slog.Int64("bytes", v.ResponseSize),
		}
		if v.Error != nil {
			attrs = append(attrs, slog.String("error", v.Error.Error()))
		}

		// 级别跟着状态码走，这样 `level=ERROR` 就是一个可以直接告警的信号。
		level := slog.LevelInfo
		switch {
		case v.Status >= http.StatusInternalServerError:
			level = slog.LevelError
		case v.Status >= http.StatusBadRequest:
			level = slog.LevelWarn
		}

		logger.LogAttrs(c.Request().Context(), level, "request", attrs...)
		return nil
	}
}

// errorHandler 把任何 error 变成契约里的 Error 响应体。
//
// 它引用生成的 api.Error 而不是就地写一个 map：错误响应的形状也是契约的一部分，
// 让编译器盯着它，比让它只存在于 openapi.yaml 的文字里可靠。
func errorHandler(logger *slog.Logger) echo.HTTPErrorHandler {
	return func(err error, c echo.Context) {
		// 响应已经开始写出（例如 handler 自己写了一半才出错），此时改不了状态码，
		// 再写只会得到一个撕裂的响应体。
		if c.Response().Committed {
			return
		}

		status := http.StatusInternalServerError
		message := "服务器内部错误"

		var he *echo.HTTPError
		if errors.As(err, &he) {
			status = he.Code
			if m, ok := he.Message.(string); ok {
				message = m
			} else {
				message = http.StatusText(status)
			}
		}

		// 5xx 的真实原因只进日志，不进响应体：站主看不懂 SQL 错误，而公网上的
		// 陌生人不该看到我们的表名。
		if status >= http.StatusInternalServerError {
			message = "服务器内部错误"
			logger.ErrorContext(c.Request().Context(), "request failed",
				slog.String("request_id", requestID(c)),
				slog.String("method", c.Request().Method),
				slog.String("uri", c.Request().RequestURI),
				slog.Any("error", err),
			)
		}

		body := api.Error{Message: message}
		if id := requestID(c); id != "" {
			body.RequestId = &id
		}

		// HEAD 按 RFC 不能有响应体，只回状态码。
		var writeErr error
		if c.Request().Method == http.MethodHead {
			writeErr = c.NoContent(status)
		} else {
			writeErr = c.JSON(status, body)
		}
		if writeErr != nil && !errors.Is(writeErr, context.Canceled) {
			logger.ErrorContext(c.Request().Context(), "failed to write error response",
				slog.String("request_id", requestID(c)),
				slog.Any("error", writeErr),
			)
		}
	}
}

// requestID 取本次请求的 ID。RequestID 中间件把它写在响应头上，因此这里从响应头读——
// 客户端有没有自带 X-Request-Id 都不影响。
func requestID(c echo.Context) string {
	return c.Response().Header().Get(echo.HeaderXRequestID)
}
