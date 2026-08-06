package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
)

type Handler struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

var _ ServerInterface = (*Handler)(nil)

func NewHandler(pool *pgxpool.Pool, logger *slog.Logger) *Handler {
	return &Handler{pool: pool, logger: logger}
}

const healthQueryTimeout = 2 * time.Second

func (h *Handler) GetHealth(c echo.Context) error {
	ctx, cancel := context.WithTimeout(c.Request().Context(), healthQueryTimeout)
	defer cancel()

	var count int64
	err := h.pool.QueryRow(ctx, `SELECT count(*) FROM account`).Scan(&count)
	if err != nil {
		h.logger.ErrorContext(ctx, "health check: database unreachable", slog.Any("error", err))
		return c.JSON(http.StatusServiceUnavailable, Health{
			Status:   HealthStatusDegraded,
			Database: HealthDatabaseError,
		})
	}

	return c.JSON(http.StatusOK, Health{
		Status:   HealthStatusOk,
		Database: HealthDatabaseOk,
	})
}
