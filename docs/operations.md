# 运行与部署

这份文档回答的是**「这个东西在本地怎么跑起来、在线上怎么上线」**。

日常命令清单由 `make help` 列出，这里只讲那些**命令背后的机制与坑**。

## 两种环境，一条配置通路

| | 本地 | 生产 |
| --- | --- | --- |
| Postgres | docker compose | docker compose |
| Go | 直接跑在宿主机（`go run`） | 容器里的单一二进制 |
| 前端 | Vite dev server（:5173，代理 `/api` 到 :8080） | 已 `embed` 进那个二进制 |

本地不走生产那条路，是为了拿到完整的热重载速度。两种环境唯一共享的配置通路是**进程环境变量**——`.env` 只被 Makefile `include` 并 export，**二进制本身不认识 `.env` 文件**（理由见 [`architecture.md`](architecture.md) 的「配置无默认值」）。少一种「本地能跑、线上不行」的原因。

## 本地开发环境的特殊性

代码放在 `/mnt/z`——WSL 挂载的 Windows 盘，走 9p 协议。它的后果散落在三个配置文件里，集中记在这里，免得日后有人「顺手清理」掉：

| 适配 | 在哪 | 不做会怎样 |
| --- | --- | --- |
| pnpm store 指向 `/mnt/z/.pnpm-store` | `web/.npmrc` | store 与 `node_modules` 跨文件系统，硬链接失效，pnpm 退化成整目录复制——这是本项目最慢的操作 |
| Vite 用轮询监听（1 秒） | `web/vite.config.ts` | 9p 上 inotify 不工作，完全收不到文件变更 |
| Postgres 数据用 named volume | `compose.yaml` | bind mount 会让数据落在 9p 上，被它的 IO 拖累 |

另外 `_ "time/tzdata"` 把时区库编进二进制：零成本，换来的是构建成 scratch/alpine 镜像时 `time.LoadLocation(SITE_TIMEZONE)` 不会在生产上找不到 `/usr/share/zoneinfo`——**而这个失败在本地永远复现不了**。

## 部署形态

一台海外 VM，docker compose 编排，前置 Cloudflare。部署产物是**一个内嵌了前端的可执行文件**——简单到不可能出错。

原图与派生图存放在 VM 文件系统的数据卷中，按模块与年月分目录。数据库只存路径。

## 部署流程

```
pull → migrate → up -d
```

**迁移是流程中独立、显式的一步，不是服务启动时的一环。** 迁移失败就中止、不切换应用版本，应用因此不会陷入崩溃循环——一个反复重启的容器会把「迁移写错了」伪装成「服务挂了」。

## CI/CD（未落地）

GitHub Actions，`main` 分支触发：

```
go vet + go test ./...                        （含 repo 层与端到端测试）
tsc + 前端构建
make gen-check                                 （契约生成漂移检查）
        ↓
多阶段 Dockerfile 构建 → 推送 GHCR
        ↓
SSH 到 VM：pull → migrate → up -d
```

- **任一步失败即中止，不部署。** 测试是关卡，不是摆设。
- `dev` 分支只跑检查与测试，不部署。
- 提交前钩子只跑 `gofmt` 与 `tsc`，**不跑测试**——钩子慢到让人想绕过，就等于没有钩子。
- 依赖 Postgres 的测试用 build tag 或环境变量区分：本地可以只跑不依赖数据库的 service / handler 层测试以获得秒级反馈，CI 上一律全跑。

## 健康检查

`GET /api/healthz` 免鉴权，**真的查一次库**而不是回一个常量——否则「服务活着但数据库连不上」或「迁移没跑」会被它瞒过去。

它只报状态，不报行数、版本号或错误原文：这个端点公开可访问，那些都是内部情况。5xx 的真实原因只进日志。

查询有 2 秒上限。健康检查必须比它检查的东西更可靠：数据库卡住时它要在两秒内明确回答「数据库不行」，而不是跟着一起挂住。
