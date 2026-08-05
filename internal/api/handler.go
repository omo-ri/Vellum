package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
)

// Handler 实现由契约生成的 ServerInterface。
//
// 骨架阶段它只有健康检查一个端点。等三个业务模块进来之后，每个模块会有自己的
// handler 包，这里只留装配所需的部分。
type Handler struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

// 编译期断言：契约新增端点而这里没跟上，构建就会失败。这是 spec-first 的兑现方式
// ——「端点有没有漏实现」由编译器回答，不靠人对着 openapi.yaml 数。
var _ ServerInterface = (*Handler)(nil)

func NewHandler(pool *pgxpool.Pool, logger *slog.Logger) *Handler {
	return &Handler{pool: pool, logger: logger}
}

// healthQueryTimeout 是健康检查查询的上限。
//
// 健康检查必须比它检查的东西更可靠：数据库卡住时，这个端点要在两秒内明确回答
// 「数据库不行」，而不是跟着一起挂住——否则负载均衡与部署脚本都会读不到结论。
const healthQueryTimeout = 2 * time.Second

// GetHealth 实现 GET /api/healthz。
//
// 它真的查一次库，而不是回一个常量：只有这样，「服务活着但数据库连不上」和
// 「迁移没跑、platform.account 还不存在」这两种情况才会被它抓住。
func (h *Handler) GetHealth(c echo.Context) error {
	ctx, cancel := context.WithTimeout(c.Request().Context(), healthQueryTimeout)
	defer cancel()

	var count int64
	err := h.pool.QueryRow(ctx, `SELECT count(*) FROM platform.account`).Scan(&count)
	if err != nil {
		// 这里刻意不把 err 交给统一错误处理器：契约规定 503 的响应体也是 Health，
		// 不是 Error。真实原因进日志，响应体只说「database: error」。
		h.logger.ErrorContext(ctx, "health check: database unreachable", slog.Any("error", err))
		return c.JSON(http.StatusServiceUnavailable, Health{
			Status:   HealthStatusDegraded,
			Database: HealthDatabaseError,
		})
	}

	// count 查出来了但不返回：这个端点公开可访问，行数是内部情况。
	return c.JSON(http.StatusOK, Health{
		Status:   HealthStatusOk,
		Database: HealthDatabaseOk,
	})
}
