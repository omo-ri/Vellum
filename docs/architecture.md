# 架构

这份文档回答的是**「某段代码该写在哪个包、为什么边界画在这里」**。它描述结构，不描述功能——要做什么看 [`.scratch/vellum-mvp/spec.md`](../.scratch/vellum-mvp/spec.md)，术语怎么叫看 [`UBIQUITOUS_LANGUAGE.md`](../UBIQUITOUS_LANGUAGE.md)，跨文件的纪律看 [`conventions.md`](conventions.md)。

标注 **（未落地）** 的部分是已经定案、但骨架阶段还没有代码的结构。写在这里是为了让第一个业务模块进来时不必重新决定一遍。

## 总体形态

一个 Go 单体进程 + 一个 Postgres 实例。生产上由 docker compose 编排在一台海外 VM 上，前置 Cloudflare。前端构建产物将来通过 `embed.FS` 编进同一个二进制，因此**部署产物只有一个可执行文件**——它同时提供 SPA 与 `/api`。

本地开发不走这条路：docker 只跑 Postgres，Go 与 Vite 直接跑在宿主机上，以获得完整的热重载速度。两种环境唯一共享的配置通路是**进程环境变量**（见 conventions 的「配置无默认值」）。

## 依赖方向

```
                    cmd/vellum  ← 唯一的装配处
                         │
        ┌────────────────┼────────────────┐
        ▼                ▼                ▼
   internal/api    internal/platform   migrations
   （契约产物）      config db httpx log   （内嵌 SQL）
        ▲
        │  （未落地）三个业务模块，各自内部三层
        └── internal/finance ─┐
            internal/recipe  ─┼─ handler → service → repo
            internal/album   ─┘
```

两条规则决定了这张图：

**横向：业务模块之间没有箭头。** `finance` / `recipe` / `album` 不得互相 import，数据库层面也不存在跨模块外键。需要共享的能力一律下沉到 `platform`。图片处理是这条纪律的第一个考验——菜谱和相册都要存图，因此「上传、生成派生图、落盘、返回路径」属于 `platform`，两个模块各自调用它、各自保存自己的元数据。

**纵向：模块内部严格单向。** `handler → service → repo`，反向引用与跨层跳跃（handler 直接调 repo）都不允许。

## 平台层

`internal/platform` 下每个包只做一件事，且都被刻意做薄——它是「共享能力」的落点，不是「杂物抽屉」。

| 包 | 职责 | 刻意不做的事 |
| --- | --- | --- |
| `config` | 把环境变量读成一个已校验的 `Config` | 不读 `.env` 文件；不提供任何默认值 |
| `log` | 构造全站唯一的 `*slog.Logger` | 不自建日志抽象，业务代码直接拿 `*slog.Logger` |
| `db` | 连接池的构造与迁移的执行 | 不提供 query helper 或 ORM，SQL 手写在各模块 repo |
| `httpx` | Echo 实例：中间件链与统一错误响应 | 不挂 CORS（同源）、Gzip（Cloudflare 在前）、CSRF（见 conventions） |

**（未落地）** 还会有：`auth`（argon2id 与会话）、图片处理与文件存储、时钟接口。

### 「全站唯一处」清单

有几件事只允许发生在一个地方。它们是这套结构里最容易被无意破坏的部分，因此集中列在这里：

| 这件事 | 唯一发生在 |
| --- | --- |
| 依赖装配（手写 `new`，不引入 DI 框架） | `cmd/vellum` 的 `runServe` |
| 拼装 Postgres DSN | `config.Config.DSN()` |
| 出现 `database/sql` | `db.withGoose`——因为 goose 只认它，用完即关，不参与请求路径 |
| 写 SQL | **（未落地）** 各模块的 repo 层，且每个 repo 只访问自己的 schema |
| 构造 API 客户端 | `web/src/api/client.ts` |
| 决定错误响应的形状 | `httpx.errorHandler`，产出契约里的 `api.Error` |

应用代码访问数据库一律通过 pgx 原生接口（`*pgxpool.Pool`）。

## 一个请求的生命周期

中间件顺序即嵌套顺序，最先加的在最外层：

```
RequestID → 访问日志 → Recover → handler
```

顺序不是随意的：

- **RequestID 在最外层**，因为后两者都要用它。它把 ID 写在响应头上，因此下游从响应头取，客户端有没有自带 `X-Request-Id` 都不影响。
- **访问日志在 Recover 之外**，是为了让 panic 转成的 500 也被记进访问日志。反过来的话，恰恰是最需要日志的那些请求没有日志行。
- **Recover 配了 `DisableErrorHandler`**，让 panic 变成一个普通 error 继续向上传，从而与其他任何 500 走完全同一条路（统一错误处理器 → 访问日志），而不是就地岔出去。

错误响应统一由 `httpx.errorHandler` 产出，形状是契约里的 `Error`。两条规则：

- **5xx 的真实原因只进日志，不进响应体。** 站主看不懂 SQL 错误，公网上的陌生人不该看到我们的表名。
- **`Response().Committed` 之后直接返回。** 响应已经开始写出时改不了状态码，再写只会得到一个撕裂的响应体。

日志级别跟着状态码走（5xx → ERROR、4xx → WARN、其余 INFO），这样 `level=ERROR` 就是一个可以直接接告警的信号。

## 启动与关停

`cmd/vellum` 有两个子命令，`serve` 与 `migrate`。启动顺序是刻意的，每一步都把一类失败挪到尽可能早的时刻：

1. **加载配置**——在任何子命令之前。缺变量就在这里失败，而不是让某个子命令跑到一半才发现自己少了点什么。
2. **`db.Open` 并 `Ping`**——`pgxpool.New` 是惰性的，不 Ping 的话一个拼错的 DSN 要到第一次查询才暴露。
3. **装配路由**——`RegisterHandlersWithBaseURL(e, handler, "/api")`。契约里 `servers` 写 `/api`、`paths` 写 `/healthz`，前缀在这里补上。
4. **`e.Start` 放进 goroutine**，主流程去等信号。错误通道缓冲为 1，没人接收时也不会泄漏那个 goroutine。

关停：`SIGINT` / `SIGTERM` 取消根 context，`Shutdown` 用一个**新的** `context.Background()` 加 10 秒超时——用已经被取消的 ctx 会让「优雅关闭」在开始的那一刻就超时。

**迁移是独立子命令，不是服务启动时的一步。** 部署流程是 `pull → migrate → up -d`：迁移失败就中止、不切换应用版本，应用因此不会陷入崩溃循环。

## 契约管线

`api/openapi.yaml` 是前后端之间的唯一约定，`make gen` 把它展开成三份产物：

| 产物 | 由谁生成 | 配置 |
| --- | --- | --- |
| `internal/api/types.gen.go` | oapi-codegen（models） | `api/cfg-types.yaml` |
| `internal/api/server.gen.go` | oapi-codegen（echo-server） | `api/cfg-server.yaml` |
| `web/src/api/schema.d.ts` | openapi-typescript | 无 |

拆成两份 oapi-codegen 配置、输出到同一个包的两个文件，是为了让「数据形状」与「路由与接口」在 diff 里各归各的——改一个 schema 不会让路由表跟着翻动。`cfg-types.yaml` 关掉了剪枝（`skip-prune`），否则没被任何 path 引用的 `Error` schema 会被丢掉。

生成产物**提交进版本库**，CI 用 `make gen-check` 重新生成并比对。落实契约的手段有两个：

- **编译期断言** `var _ ServerInterface = (*Handler)(nil)`——契约新增端点而实现没跟上，构建就失败。「端点有没有漏实现」由编译器回答，不靠人对着 yaml 数。
- **前端** 通过 `createClient<paths>` 拿到类型，写错一个路径就是一个编译错误。

端点按模块分组：`/api/auth/*`、`/api/finance/*`、`/api/recipes/*`、`/api/album/*`。**每个业务模块各自暴露一个 `summary` 端点**，据点主页的聚合发生在前端——后端不做跨模块联合查询，这是对模块边界的保护。

**（待定）** 骨架期只有一个 `Handler` 实现整个 `ServerInterface`。三个模块进来后，各模块的 handler 包如何共同满足这一个生成接口（一个组合结构体嵌入各模块 handler，还是别的写法），留到认证模块落地时定，那时会把结论补在这里。

## 数据库

一个 database，四个 schema：`platform`、`finance`、`recipe`、`album`。**schema 在数据库层面物理执行模块边界**——跨模块外键在物理上不会出现。每个模块的 repo 只访问自己的 schema。

约定：

- 一切能表达的约束都写进 schema（`NOT NULL`、`CHECK`、外键、唯一索引）。骨架里已有一个例子：`account_singleton` 是一个常量表达式上的唯一索引，让「单用户站点最多一行」由数据库强制，而不是靠应用代码自觉。
- 多表写入放在显式事务里，**事务边界在 service 层声明**，句柄传给 repo。
- 时间一律 `timestamptz` 存 UTC；「今天」「本月」按 `SITE_TIMEZONE` 计算。
- 金额一律 RUB 最小单位存 `bigint`，禁止浮点。
- 照片 EXIF 用 `jsonb`（厂商字段本身无固定 schema）。不使用全文检索——菜谱规模在两百条以内，`ILIKE` 足够。

迁移用 goose，SQL 通过 `embed` 编进二进制。`migrations` 包只有一个 `embed.FS`，不含逻辑。

## 业务模块的三层（未落地）

每个模块内部固定分三层，各占一个包（如 `internal/finance/handler`、`.../service`、`.../repo`）：

| 层 | 职责 | 边界 |
| --- | --- | --- |
| **handler** | 绑定与校验请求、DTO ↔ 领域类型转换、调 service、领域错误 → HTTP 状态码 | 不含业务规则，不碰数据库 |
| **service** | 业务规则所在地：`今天可用` 的计算、月末结算、大额开销写存款流水、`今天做了` 的计数 | 依赖 repo **接口**与 `platform` 的时钟 |
| **repo** | 唯一写 SQL 的地方 | 返回模块内的领域结构体，不返回行对象、也不返回 OpenAPI 生成的类型 |

**类型的流向**：repo 与 service 之间流动领域结构体；OpenAPI 生成的 DTO 只出现在 handler 层。这层映射代码是刻意付出的成本——换来的是业务层不被契约绑死，契约变更不会穿透到业务规则。

**接口一律定义在使用它的一侧**（service 包定义它需要的 repo 接口，handler 包定义它需要的 service 接口），只列真正用到的方法，由下层的具体类型隐式满足。除此之外不引入抽象，也不引入 mock 生成框架。

repo 接口的存在同时是测试缝：

| 缝 | 用什么驱动 | 测什么 |
| --- | --- | --- |
| repo | **真实 Postgres**，跑完全部迁移 | SQL 本身：写入读回、筛选排序、约束真的拒绝坏数据、事务回滚无残留 |
| service | 手写 fake repo（内存 map）+ 可控时钟 | 业务规则与它的边界：超支、零预算、月末最后一天、跨月 |
| handler | Echo `httptest` + fake service | HTTP 表层：参数校验 → 400、未找到 → 404、未登录 → 401 |
| 端到端 | 完整装配 + 真实 Postgres，走 HTTP | 只跑主干：装配是否正确、中间件是否真的挂上、事务是否真的提交 |

**一个行为只在它归属的那一层被详尽测试**，其余层只测「有没有正确地接起来」。绝不用 sqlmock 一类的伪数据库测 SQL。fake repo 保持朴素——不复刻 SQL 语义，也不模拟事务。

前端刻意不建测试缝，只在 M0 引入一个 Playwright 冒烟测试。

## 本地开发环境的特殊性

代码放在 `/mnt/z`——WSL 挂载的 Windows 盘，走 9p 协议。它的两个后果散落在三个配置文件里，集中记在这里，免得日后有人「顺手清理」掉：

| 适配 | 在哪 | 不做会怎样 |
| --- | --- | --- |
| pnpm store 指向 `/mnt/z/.pnpm-store` | `web/.npmrc` | store 与 `node_modules` 跨文件系统，硬链接失效，pnpm 退化成整目录复制——这是本项目最慢的操作 |
| Vite 用轮询监听（1 秒） | `web/vite.config.ts` | 9p 上 inotify 不工作，完全收不到文件变更 |
| Postgres 数据用 named volume | `compose.yaml` | bind mount 会让数据落在 9p 上，被它的 IO 拖累 |

另外 `_ "time/tzdata"` 把时区库编进二进制：零成本，换来的是将来构建成 scratch/alpine 镜像时 `time.LoadLocation(SITE_TIMEZONE)` 不会在生产上找不到 `/usr/share/zoneinfo`——而这个失败在本地永远复现不了。

## 与 spec 的偏差

spec 里写的技术决策，实现时有意改动的部分记录在这里：

| spec | 实现 | 理由 |
| --- | --- | --- |
| 任务运行器用 `just` | **`make`** | 少一个需要手动安装的工具。`Makefile` 顺带承担了 `include .env` 并 export 的职责，让同一份 `.env` 对 docker compose、`go run` 与 vite 同时生效；`just` 需要额外配置才能做到同一件事 |

其余决策与 spec 一致。新增偏差时请补在这张表里——一条没有记录的偏差，三个月后会变成一次「为什么这里和文档不一样」的考古。
