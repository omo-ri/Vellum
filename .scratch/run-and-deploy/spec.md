# 运行与部署：M0 上线链路

Status: 未开始（本文件只是计划，尚未动任何代码）

这份文件是 2026-08-06 一次 grilling 会话的产出：把「这个东西怎么跑起来、怎么上线」从零讨论到可以直接实现的程度。

结论定案后应当拆散并入正式文档（`docs/operations.md` 等，见 Stage 0），本文件随之作废——**它不是第二份文档，只是一次讨论的记录与实现清单。**

---

## 一、被推翻的两个前提

讨论中查到两件事实，它们否掉了原文档里已经写下的东西。记在这里，免得日后有人「按原文档修回去」。

### 1. Cloudflare 不能留在前面

自 2025-06-09 起，俄罗斯 ISP（Rostelecom、MTS、Megafon、Vimpelcom、MGTS 等）对**经过 Cloudflare 的连接**限速，机制是每个资源只放过前 16KB，之后连接卡死。Cloudflare 官方确认此事并声明无力修复。限速认的是 Cloudflare 而不是服务器位置，请求发往境外服务器同样中招，HTTP/1.1、HTTP/2、HTTP/3 全覆盖。另有一层自 2024 年起针对 TLS ECH 的封锁，而 Cloudflare 默认开启 ECH。

**本站唯一的用户在俄罗斯**，所以这不是「慢一点」，而是「站点对唯一那个人不可用，同时对世界其他地方完全正常」——最难自己发现的一类故障。

- 官方说明：https://blog.cloudflare.com/russian-internet-users-are-unable-to-access-the-open-internet/
- 机制细节：https://en.zona.media/article/2025/06/27/cloud_confirmed

因此：**去掉 Cloudflare**，DNS 直指 VM 的 IPv4。

### 2. Vercel 不适合，代价是拆掉三个已定的决定

- **同源消失，`SameSite=Lax` 的 CSRF 论证一起消失。** 前端在 Vercel、API 在 VM 就是跨源：需要 CORS，且会话 cookie 必须 `SameSite=None`，而 `architecture.md` 整个「`SameSite=Lax` + GET 无副作用 ≡ CSRF 防护」的推理建立在同源之上。`None` 一开，那道防线归零。
- **单一产物消失**，「静态文件放哪、版本对不对得上」原样回来。
- **后端搬不过去**：Vercel 的 Go 支持是 serverless function，无常驻进程（https://vercel.com/docs/functions/runtimes/go）。长驻 Echo + `pgxpool` + 优雅关停 + 惰性结算全部要重写，连接池在 serverless 里是反模式。
- **照片没地方落**（无持久文件系统），会逼向对象存储——已被明确否决。
- **Postgres 得托管**，「自己运维一个 Postgres」这个学习目标没了。
- **CI/CD 学习目标被吃掉**：git-push 自动部署是黑盒。
- **它在俄罗斯也有被封实例**：自定义域名指向的 Vercel IP 被 RKN 封过，社区 2025–2026 持续有人报。

因此：**留在单 VM**。

---

## 二、十五条决定

| | 决定 | 关键理由（「不这么做会出什么错」） |
| --- | --- | --- |
| 1 | 去掉 Cloudflare，DNS 直指 VM 的 IPv4 | 见上 |
| 2 | Caddy 终结 TLS + 自动 Let's Encrypt；`caddy_data` 必须是 named volume | 证书不持久化的话，每次 `up -d` 重建容器都重新申请，Let's Encrypt 对同一组域名有**每周 5 张重复证书**上限——第六次部署当天站点没有证书，且要等到下周才能修 |
| 3 | VM 在荷兰（站主决定，已知代价：使用时的跨境风险只能靠 VPN 兜） | — |
| 4 | 前端 `embed` 进 Go 二进制，Caddy 完全不碰静态文件 | Caddy 是独立容器，要它看见前端只有两条路：共享卷（谁来写？前端与后端成两个独立版本，`up -d` 顺序、失败时机、上次残留都能让它们错位）或把前端也构建进 Caddy 镜像（两个镜像要锁同一个 commit，回滚要回滚两个 tag）。embed 让「一个 commit = 一个镜像 = 一个 tag」完整，Caddy 退化为无状态的 TLS 终结器 |
| 5 | 多阶段 Dockerfile，前端在 node 阶段构建（不在 CI runner 上构建） | 让「`docker build .` 在任何机器上复现同一产物」成立，CI 只是恰好执行它的机器。在 runner 上构建会把「产物怎么来的」分裂到 workflow 与 Dockerfile 两处（违反理念二），且 runner 的 libc 与运行基座必须对得上——某天开了 CGO 就会得到一个在容器里 `not found` 的二进制 |
| 6 | 运行基座 `alpine` | 只有一台机器、无 sidecar、无 `kubectl debug`，容器里那个 shell 是唯一的现场：`env` 看变量到底传进来没有、`ls` 看 embed 的前端在不在、`wget localhost:8080/api/healthz` 区分「应用挂了」与「Caddy 转发错了」。它还让容器 healthcheck 白拿（busybox `wget`），scratch/distroless 要为此专门编一个探针 |
| 7 | `//go:embed all:dist` + 提交 `web/dist/.gitkeep` | embed 的 pattern 不能含 `..`，所以指令必须在 `web/` 下的 Go 文件里；且不带 `all:` 前缀时点文件被排除，`dist` 只含 `.gitkeep` 会报 `cannot embed directory dist: contains no embeddable files`。而 `web/dist/` 是 gitignore 的，新克隆的仓库里它不存在——`go build ./...` 与 `go vet ./...` 直接失败，CI 第一步就红，且错误看起来像「代码坏了」。build tag 方案更糟：`go vet ./...` 默认不检查带 tag 的文件，等于在最不能出错的那份代码上关掉了检查 |
| 8 | 只推 `sha-<7位>` tag；GHCR 私有；回滚 = `cp .env.prev .env && docker compose up -d`；`-ldflags` 把 sha 编进二进制、只进日志不进 healthz | `latest` 是移动标签：既让「线上跑的是哪个 commit」查不出来，又因为 **tag 没变时 `up -d` 不会拉新镜像**，必须额外记得 `docker compose pull`——忘一次就是「部署成功了但代码没变」，会浪费半小时才想到。sha tag 天然免疫。公开镜像等于公开部署产物历史（每个 sha 可下载、可 diff），一个单用户私站没理由交出去。healthz 不报版本号是 `operations.md` 已有的纪律 |
| 9 | CI scp 推 `compose.yaml` / `Caddyfile` / `.env`；`.env` 全部由 CI 生成（机密来自 Secrets，非机密作字面量写在 workflow，仍进版本库）；GHCR 凭证部署时现场 `docker login --password-stdin`、结束 `docker logout` | VM 上放 git checkout 会漂移（谁 `git stash` 过一次就说不清了），且私有仓库还要多一份 deploy key。`.env` 交给 CI 的白拿好处：生产全部变量在一个地方被逐条列出，于是可以加一道门禁（见 21）。长期躺在 VM 上的 `docker` 凭证会留几年，而你早忘了它的权限范围 |
| 10 | `docker compose run --rm app migrate up`；`ENTRYPOINT ["/vellum"]` + `CMD ["serve"]` | 复用 `app` 服务的全部定义（镜像 tag、`env_file`、网络），让「app 与 migrate 的运行环境只定义一次」。`docker run` 手写一遍环境与网络名，三者任一改动要改两处，漏掉的那处只在部署时炸。`CMD ["/vellum","serve"]` 写法会让覆盖时要重写整条命令 |
| 11 | 接受几秒停机；双层门禁（compose healthcheck + `up -d --wait`，加 CI 从 runner 对公网域名轮询 `curl` 60s/2s）；**不自动回滚** | 蓝绿要在 Caddy 里维护上游切换，为的是只存在于你自己按下 deploy 后那几秒的问题。首次部署要留足超时——Caddy 首次签证书可能几十秒。不自动回滚：`up -d` 重建容器会**销毁坏容器的日志**，而那正是唯一需要的东西；它还是一个会在无人看管时自己动手、而且自己也会失败的机制（回滚也起不来怎么办，循环吗）；而你本来就在看那次 push 的 CI |
| 12 | 测试用 compose 的 `db` + 每个测试包一个 `CREATE DATABASE ... TEMPLATE` 克隆库（OQ-9 定案） | 迁移只在模板库上跑一次，克隆约百毫秒；零新依赖；`make db-up` 已存在且 `make dev` 本来就会起库。testcontainers 每次跑测试都要付容器启动的钱（Go 并行跑各包 = 几个容器），并往 go.mod 加一组 docker 客户端依赖 |
| 13 | `vellum account set-password`，口令从 stdin 读，插入或更新同一条路径 | `account` 表已有 singleton 唯一索引但**没有任何东西插那一行**，而 PRD 明确不做注册。环境变量引导会让明文口令长期常驻生产配置且永远删不掉；种子迁移把口令 hash 提交进库且所有环境同一个口令；首次访问引导页最糟——部署完成到你设置口令之间，公网上存在一个「谁先抢到谁是站主」的窗口，而扫描器发现新域名是分钟级的。此命令另外兼作需求 6 的救援出口（忘记口令时唯一的自救路径） |
| 14 | **不做 Playwright**（OQ-10 消失，不是定案） | 站主决定。已知代价：只有真浏览器能发现「JS 抛异常、页面空白，但 HTTP 全部 200」，而这恰是前端 embed 这条新代码路径最可能的坏法。此洞由 15 部分补上，剩余部分明确接受 |
| 15 | 补洞：handler 测试（第 3 条缝）+ 镜像级 `curl` 断言 | 二者不重叠：handler 测试测**逻辑**（fallback、缓存头写得对不对），镜像级 `curl` 测**打包**（node 阶段有没有真的产出、有没有真的被 embed 进去）。7b 的启动自检只检查 `index.html` 存在，一个只有 index 而 assets 全丢的镜像照样能启动 |

### 7 的四条实现细节

- **7b｜prod 启动自检（理念一）**：`APP_ENV=prod` 而 embed FS 里没有 `index.html` 即**拒绝启动**。否则一个 node 阶段悄悄失败的镜像会「健康」地跑起来、健康检查也过，只是全站白屏。`dev` 下缺失是正常状态。
- **7c｜fallback 不是「一律发 index.html」**：非 `/api` 路径命中文件就发文件，否则发 `index.html` + 200；**但 `/assets/*` 命中不到就老实 404**。否则指向已删除 hash 文件的请求会拿到 HTML，浏览器按 JS 解析，报错是 `Unexpected token '<'`——完全不指向真实原因。
- **7d｜缓存头**：`assets/*`（文件名带 hash）→ `public, max-age=31536000, immutable`；`index.html` → `no-cache`。反过来配是自托管 SPA 最经典的翻车方式：用户缓存的旧 index 指向新版本里已不存在的 hash 文件，白屏且刷新无效。
- **7e｜`make build` 不碰 pnpm**，注释写明它产出的二进制不含前端；另加 `make image` 走完整 `docker build`。

### 10 的隐含纪律（每次部署都在生效）

`pull` 之后、`up -d` 之前，`migrate` 用的是**新镜像**，而正在服务的 `app` 还是**旧镜像**——新迁移会在旧代码运行时生效，中间有几秒到几十秒的窗口。因此：

> **迁移必须能被上一个版本的代码容忍。** 加字段、加表、加索引安全；删列、改列类型、加无默认值的 `NOT NULL` 都会让那个窗口里的旧代码报错。

这与 8 的回滚约束是同一条约束，只是一个发生在正常部署路径上、一个发生在回滚时。**要写成独立一节，不是回滚的附注。**

### 12 的四件具体事

1. 库没起时给人话错误（「Postgres 连不上，先 `make db-up`」），而不是一堆 `connection refused`；同时加 `make test` 依赖 `db-up`。
2. CI 直接 `cp .env.example .env` 就能起库（`compose.yaml` 的 `DB_*` 是 `${VAR:?}` 强制的，而 `.env.example` 的值本来就是 `vellum/vellum/vellum`）。**这让「本地与 CI 同一条路径」不是口号——两边执行同一条 `docker compose up -d --wait db`。**
3. `CREATE DATABASE ... TEMPLATE x` 要求此刻无其他连接连着 `x`，并行测试会撞 `source database is being accessed by other users`。解法：建完模板立刻断开 + 克隆动作加 advisory lock 或重试。**这是这条路上唯一需要小心写的地方，理由写进那段代码的注释。**
4. 默认跑完 drop，`KEEP_TEST_DB=1` 保留（能进去 `psql` 看一眼失败测试留下的状态，比加日志重跑快得多）。

已排除的方案：**「共用一个库、每个测试开事务再回滚」不行**——`testing.md` 要求 repo 层验证「事务回滚后无残留」与「结算记录与存款流水处于同一事务」，被测代码自己就在用事务，外面再套一层变成 savepoint 嵌套语义，测的就不是生产行为了。

### 15 的四层检查分工（写进 `testing.md`）

| 检查 | 覆盖 | 跑在哪 |
| --- | --- | --- |
| 第 3 条缝（handler + `httptest`） | SPA fallback、缓存头、embed FS 内容 | `go test`，本地与 CI |
| 第 4 条缝（端到端 + 真实 Postgres） | 登录链路、鉴权中间件、契约序列化 | `go test`，本地与 CI |
| 镜像级 `curl` | **打包后的产物**：ENTRYPOINT、musl 上跑不跑、dist 真的在里面 | CI，构建之后部署之前 |
| 公网 `curl`（11） | Caddy、DNS、证书 | CI，部署之后 |

四层各不重叠，合起来是原本想让 Playwright 一个人扛的那些。**唯一无人覆盖的是「浏览器里 JS 执行成功」——明确接受，代价是每次部署后自己打开一次。**

### 由 Caddy 进场连带的四件事

- `compose` 变三个服务：只有 `caddy` 映射 80/443，`app` **不写 `ports:`**。于是 `HTTP_ADDR=:8080` 变成「容器内监听」，宿主机 `curl localhost:8080` 不通，排障一律 `docker compose exec`。
- `architecture.md` 的「不引入 Gzip——Cloudflare 在前」→「由 Caddy 压」（`encode zstd gzip`）。唯一发生地换人，理念二不破。
- 请求体上限方向与 nginx 相反：**Caddy 默认不限**（nginx 默认 1MB）。M4 上传不会撞墙，但要主动写 `request_body { max_size ... }`，否则一个 bug 就能让一次请求填满磁盘。
- **必须设 Echo 的 `IPExtractor` 读 `X-Forwarded-For`。** 不设的话访问日志里全是 docker 内网地址；更要紧的是需求 5 的「连续失败登录锁定」若按 IP 计数，拿到的永远是同一个内网 IP，那条规则从「锁住攻击者」退化成「锁住所有人，包括站主」。**这是本次讨论唯一一个渗进业务代码的后果。**

### `/opt/vellum/` 的最终样子

```
compose.yaml    ← CI scp
Caddyfile       ← CI scp
.env            ← CI 生成：DB_PASSWORD、SESSION_SECRET、VELLUM_IMAGE_TAG
.env.prev       ← 上一次的 .env，回滚用
```

卷三个：`pgdata`、`caddy_data`（证书）、`vellum_data`（照片，M4 用，现在就声明好以免日后迁移数据）。

**docker compose 的坑**（写进注释）：同目录下的 `.env` 默认**只用于 compose 文件里 `${VAR}` 的插值，不会自动传进容器**。要让 `app` 真的看见 `DB_PASSWORD` 必须显式写 `env_file: .env`。同一个文件可兼任两个角色，但那是两件事，漏掉后者的表现是「compose 插值正常、容器却说缺变量」。

### 完整部署序列

```
scp compose.yaml Caddyfile .env  →  docker login ghcr.io
  →  docker compose pull                    # 拉不到就在这里失败，什么都还没变
  →  docker compose up -d --wait db         # 库先起来并健康
  →  docker compose run --rm app migrate up # 失败即中止，app 仍是旧版本
  →  docker compose up -d --wait            # 切换 app + caddy
  →  docker logout
  →  curl 公网域名 /api/healthz（轮询 60s / 每 2s）
```

---

## 三、实现计划

按「每一步都能独立提交、且提交后仓库是绿的」切分。**Stage 1–5 全部不依赖 auth**，所以这条链路可以先整条打通、先真的上线，auth 再顺着它长上去。

### Stage 0 — 先落文档（后面每步都要引用它）

1. **重写 `docs/operations.md`**：环境矩阵加入 Caddy；部署形态改为「荷兰 VM，无 CDN 前置」；**「首次部署」与「日常部署」分成两节**；镜像 tag 与回滚；迁移兼容纪律独立成节；CI/CD 图去掉 Playwright；`/opt/vellum/` 文件清单；一句「`docker compose down -v` 会删掉全部账本，生产上永不使用 `-v`」。
2. **改 `docs/architecture.md`**：Gzip 的理由换成 Caddy；新增纪律 **`X-Forwarded-For` 与 Echo `IPExtractor`**（点明它承载需求 5）；「不引入 CORS」理由不动（同源仍成立）。
3. **改 `docs/testing.md`**：删前端 Playwright 一节，换成四层检查分工表；第 1 条缝写定 OQ-9 方案。
4. **改 `docs/open-questions.md`**：删 OQ-9、OQ-10、OQ-11。

### Stage 1 — 让二进制能提供前端（纯本地，无需 docker）

5. `web/dist/.gitkeep` + `web/embed.go`（`//go:embed all:dist`）。**必须先做**，否则后面任何 `go build ./...` 都编译不过。
6. SPA 提供代码：fallback、`/assets/*` 不 fallback、两种缓存头、prod 启动自检。
7. 第 3 条缝的测试：上面四条行为各一个 case。
8. `Makefile`：`build` 注释写明「不含前端」，新增 `make image`。

### Stage 2 — 测试用的 Postgres 脚手架

9. 辅助包：模板库建一次 → 每包 `TEMPLATE` 克隆 → 结束 drop（`KEEP_TEST_DB=1` 保留）→ 克隆加 advisory lock/重试 → 库没起时给人话错误。
10. `make test` 依赖 `db-up`。
11. **第一条 repo 测试**：`account_singleton` 索引真的会拒绝第二行。它同时验证迁移与脚手架。

### Stage 3 — 镜像

12. `.dockerignore`（**先于 Dockerfile**，否则第一次 `docker build` 会把 `node_modules` 与 `.git` 全塞进上下文，在 9p 上慢到以为卡死）。
13. `Dockerfile`：node → go → alpine，`CGO_ENABLED=0`，基础镜像钉小版本，`-ldflags` 注入 sha，`ENTRYPOINT`/`CMD`。
14. `compose.ci.yaml`（`app` + `db`，端口映射 8080）——**它同时就是「本地跑一次生产镜像」的手段**，不另做一套。
15. `app` 的 healthcheck（busybox `wget`）。

### Stage 4 — VM 侧文件（进版本库，由 CI scp）

16. `compose.prod.yaml`：`caddy` + `app` + `db`，三个卷，`app` 不写 `ports:`，`env_file: .env`，重启策略。
17. `Caddyfile`：域名、`reverse_proxy app:8080`、`encode zstd gzip`、`handle_errors` 静态页、`request_body max_size`。

### Stage 5 — 流水线

18. `.github/workflows/ci.yml` 检查 job：`gofmt` → `go vet` → `make gen-check` → `go test ./...` → `tsc` → `docker build`。**`dev` 与 `main` 都跑**（`dev` 也 build 但不 push，于是 Dockerfile 写坏了在 `dev` 上就红）。
19. 镜像级 `curl` 断言 + 推 GHCR（只在 `main`）。
20. deploy job：见「完整部署序列」。
21. **`.env` 的 key 集合与 `.env.example` 比对，不一致即中止部署。** 让「加了新配置项但忘了加到生产」从「部署后容器起不来」变成「部署前 CI 红」（理念一），而且白拿——`.env.example` 本来就必须维护。
22. Secrets 清单：`SSH_HOST` / `SSH_USER` / `SSH_KEY` / `GHCR_PAT` / `DB_PASSWORD`（+ 将来 `SESSION_SECRET`）。

### Stage 6 — 一次性人工动作（不进版本库，写进文档）

23. VM：装 docker、建 `/opt/vellum`、放 deploy 公钥、防火墙只开 22/80/443。
24. DNS A 记录指向 VM。
25. 第一次 push，盯完 CI，确认证书签下来了。
26. `account set-password`。**依赖 argon2id 哈希，属于 auth 模块**；在它落地前第一次上线的站点只有 `/api/healthz`——而 PRD 说这恰恰比一个从未部署过的完整项目更有价值。

**M0 的「`git push` 自动上线」因此有一个一次性人工步骤**（26）。不是流水线缺陷，是「单用户站点没有注册流程」的必然结果，要与「日常部署」分节写清，免得日后有人以为漏了一步。

---

## 四、未经单独讨论、采用默认值的六件事

这些是**假设，不是站主的决定**，实现时若有异议随时改：

1. **生产上不做 `migrate down`** —— 文档明说禁止。回滚只回滚应用，数据库只前进。这是「迁移必须被上一版容忍」的另一半。
2. **日志** —— docker 默认 `json-file` + `max-size=10m,max-file=3`（不配它会一直长到填满磁盘）。
3. **重启策略** —— 三个服务都 `restart: unless-stopped`。
4. **告警** —— M0 什么都不加，靠 GitHub 的 workflow 失败邮件 + 站主每天会打开它。外部 uptime 监控留到以后。
5. **备份** —— 照 PRD「不做」执行。
6. **SPA 代码的落点** —— 放 `web` 包本身（返回标准 `http.Handler`，不 import echo，测试也不需要 echo），`cmd/vellum` 用 `echo.WrapHandler` 装上。理由：它不满足 `platform` 的准入条件（只有一处用它），塞进 `httpx` 会扩大那个包「中间件链与统一错误响应」的单一职责。
