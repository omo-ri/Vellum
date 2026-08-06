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

func New(logger *slog.Logger) *echo.Echo {
	e := echo.New()

	e.HideBanner = true
	e.HidePort = true

	e.HTTPErrorHandler = errorHandler(logger)

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
		HandleError:     true,
		LogValuesFunc:   logRequest(logger),
	}))

	e.Use(middleware.RecoverWithConfig(middleware.RecoverConfig{
		DisableErrorHandler: true,
		LogErrorFunc: func(c echo.Context, err error, stack []byte) error {
			logger.LogAttrs(c.Request().Context(), slog.LevelError, "panic recovered",
				append(requestAttrs(c),
					slog.Any("error", err),
					slog.String("stack", string(stack)),
				)...,
			)
			return err
		},
	}))

	return e
}

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

func errorHandler(logger *slog.Logger) echo.HTTPErrorHandler {
	return func(err error, c echo.Context) {
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

		if status >= http.StatusInternalServerError {
			message = "服务器内部错误"
			logger.LogAttrs(c.Request().Context(), slog.LevelError, "request failed",
				append(requestAttrs(c), slog.Any("error", err))...,
			)
		}

		body := api.Error{Message: message}
		if id := requestID(c); id != "" {
			body.RequestId = &id
		}

		var writeErr error
		if c.Request().Method == http.MethodHead {
			writeErr = c.NoContent(status)
		} else {
			writeErr = c.JSON(status, body)
		}
		if writeErr != nil && !errors.Is(writeErr, context.Canceled) {
			logger.LogAttrs(c.Request().Context(), slog.LevelError, "failed to write error response",
				append(requestAttrs(c), slog.Any("error", writeErr))...,
			)
		}
	}
}

func requestAttrs(c echo.Context) []slog.Attr {
	return []slog.Attr{
		slog.String("request_id", requestID(c)),
		slog.String("method", c.Request().Method),
		slog.String("uri", c.Request().RequestURI),
	}
}

func requestID(c echo.Context) string {
	return c.Response().Header().Get(echo.HeaderXRequestID)
}
