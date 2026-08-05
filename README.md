# Vellum

一个单用户、自托管、公网可达的个人网站，主题是「日式异世界旅人的据点」。站主登录后进入据点主页，从这里进入三个彼此独立的模块：

- **理财** —— 月度预算、记账、`今天可用`、存款与月末结算。
- **菜谱** —— 食材、步骤、成本估算、做过次数与上次做于。
- **相册** —— 以「事件」为一级单位的编年史。

技术形态：一个 Go 单体二进制（将来内嵌 React SPA）+ 一个 Postgres 实例，由 docker compose 编排在一台海外 VM 上。

> **当前状态：M0 骨架。** 三个业务模块都还没有代码。仓库里现在的全部内容是一条贯穿前后端的最短通路——一个真的会查库的健康检查端点，和一个显示「据点连接正常」的页面。它存在的意义是证明工程管线是通的，而不是提供功能。

## 骨架现在做到了什么

一条请求走完 `浏览器 → Vite proxy → Echo → pgxpool → Postgres` 全程：

| 环节 | 落点 |
| --- | --- |
| 配置读取（无默认值，缺一即启动失败） | `internal/platform/config` |
| 结构化日志（dev 用 Text、prod 用 JSON） | `internal/platform/log` |
| Echo 装配：RequestID → 访问日志 → Recover，统一错误响应 | `internal/platform/httpx` |
| pgx 连接池 + 启动时 Ping | `internal/platform/db` |
| goose 迁移（内嵌 SQL，独立子命令） | `migrations/`、`cmd/vellum migrate` |
| OpenAPI 契约与两端生成产物 | `api/openapi.yaml` → `internal/api/*.gen.go`、`web/src/api/schema.d.ts` |
| 健康检查（真查一次 `platform.account`） | `internal/api/handler.go` |
| 类型安全的前端客户端与一页界面 | `web/src/api/client.ts`、`web/src/App.tsx` |

## 快速开始

前置：**docker**（含 compose v2）、**Node 22+**（用它自带的 corepack，pnpm 版本由 `web/package.json` 钉死）、**Go 1.25+**。除此之外没有需要手动安装的工具——Go 工具走 `go.mod` 的 `tool` 指令，前端工具走 pnpm。

```bash
make setup   # 首次 clone 后跑一次：建 .env、装前端依赖、拉 Go 依赖
make dev     # 起 Postgres → 跑迁移 → 并行跑 api(:8080) 与 web(:5173)
```

打开 <http://localhost:5173>，页面上那一句话就是本批的验收结论：

| 页面文案 | 含义 |
| --- | --- |
| 据点连接正常 | 五个环节全部接上：Vite proxy、Go 服务、环境变量、pgx 连接池、goose 迁移 |
| 连接不上据点 | 其中某一环断了。先看 `make api` 的日志，再看 `make db-up` 是否健康 |

也可以绕过前端直接问后端：

```bash
curl -i localhost:8080/api/healthz
# 200 {"database":"ok","status":"ok"}          —— 服务与数据库都正常
# 503 {"database":"error","status":"degraded"} —— 服务活着，但库连不上或迁移没跑
```

## 常用命令

`make help` 会列出全部目标。日常只会用到这些：

| 命令 | 作用 |
| --- | --- |
| `make dev` | 起库、跑迁移、并行跑前后端 |
| `make api` / `make web` | 只跑其中一侧 |
| `make db-up` / `make db-down` | 起停 Postgres（`db-down` 保留数据） |
| `make db-reset` | 删库重来：`down -v` + up + migrate |
| `make migrate` / `make migrate-down` | 迁移到最新 / 回滚一个版本 |
| `make migrate-new name=add_expense` | 新建一个空迁移 |
| `make gen` | 从 `api/openapi.yaml` 重新生成 Go 与 TS 两端产物 |
| `make gen-check` | 生成后比对，产物与契约不一致即失败（CI 用） |
| `make lint` / `make fmt` | golangci-lint + tsc / gofmt |
| `make build` | 构建单一二进制到 `./vellum` |

## 仓库结构

```
api/                 OpenAPI 契约与 oapi-codegen 配置。契约是前后端唯一真相
cmd/vellum/          唯一的二进制：serve 与 migrate 两个子命令，也是唯一的装配处
internal/
  api/               契约的生成产物（*.gen.go）与骨架期的 handler
  platform/          跨模块共享的能力：config / db / httpx / log
                     将来还会有 auth、图片处理、文件存储、时钟
  finance/ recipe/ album/    三个业务模块（尚未创建，各自 handler/service/repo 三层）
migrations/          goose 的 SQL，通过 embed 编进二进制
web/                 Vite + React + TypeScript 前端
docs/                工程纪律与架构说明
.scratch/            本仓库的 issue tracker：spec 与工单都是 markdown 文件
.agents/             第三方 agent 技能，来自 mattpocock/skills（MIT，见 .agents/README.md）
```

## 文档地图

| 文档 | 读它来回答 |
| --- | --- |
| [UBIQUITOUS_LANGUAGE.md](UBIQUITOUS_LANGUAGE.md) | 一个领域概念叫什么、代码标识符写成什么。**与其他文档冲突时以它为准** |
| [docs/architecture.md](docs/architecture.md) | 代码怎么分层、边界画在哪、一个请求经过什么、某段逻辑该写在哪个包 |
| [docs/conventions.md](docs/conventions.md) | 那些编译器和 linter 管不到、但违反了会出事的约定 |
| [.scratch/vellum-mvp/spec.md](.scratch/vellum-mvp/spec.md) | 要做什么、为什么做、**明确不做什么**，以及 M0–M4 的划分 |
| [docs/agents/](docs/agents/) | agent 在本仓库怎么读写工单 |

## 四条不可动摇的约定

细节与理由都在 [docs/conventions.md](docs/conventions.md)，这里只列结论：

1. **`GET` 不得有副作用。** 这不是风格偏好——它是本项目不上 CSRF 中间件的安全前提。惟一被允许的例外是月末结算的惰性触发。
2. **契约先行。** 改接口的顺序永远是 `openapi.yaml` → `make gen` → 实现。手改 `*.gen.go` 或 `schema.d.ts` 没有意义。
3. **配置无默认值。** 缺一个环境变量，进程就拒绝启动。
4. **模块不得互相 import。** `finance` / `recipe` / `album` 之间没有引用，数据库层面也没有跨模块外键；需要共享的能力下沉到 `platform`。

金额一律以 RUB 最小单位存 `bigint`（禁止浮点）；时间一律存 UTC，「今天」「本月」按 `SITE_TIMEZONE` 计算。

## M0 还差什么

骨架已经立住，但 M0 的定义里还有这些没做（顺序即建议的推进顺序）：

- [ ] **认证与登录页**，走完 handler / service / repo 三层——它同时是后续每个模块要抄的参考实现。
- [ ] **四条测试缝的脚手架**：repo 打真实 Postgres、service 打手写 fake repo、handler 打 fake service、再加一层端到端。目前仓库里**没有任何测试**。
- [ ] **`platform` 的时钟接口**：涉及时间的业务规则要可测，业务代码不得直接调 `time.Now()`。
- [ ] **SPA 内嵌**：把 `web/dist` 通过 `embed.FS` 编进二进制，非 `/api` 前缀的路径交给它。
- [ ] **`.golangci.yml`**：仓库里还没有这份配置，`make lint` 目前跑的是 golangci-lint 的默认 linter 集（errcheck / govet / ineffassign / staticcheck / unused）。想要更严的规则得先把它写出来。
- [ ] **CI/CD 全链路**：GitHub Actions（测试 + `make gen-check` + 构建）→ GHCR → SSH 到 VM 执行 `pull → migrate → up -d`，并真实部署上线一次。
- [ ] **PWA**：manifest、图标、全屏显示模式（不做 Service Worker 离线缓存）。
- [ ] **一个 Playwright 冒烟测试**：登录 → 据点主页 → 三个模块入口可达。

M0 刻意排在所有功能之前：一个只返回健康状态、但已经能 `git push` 自动上线的服务，比一个功能完整却从未部署过的本地项目更有价值。
