# 后端归位 server/ + 第一条 CI/CD 流水线

## Context

后端代码摊在仓库根上,全部收进 `server/`,`web/` 不动。module 名仍是 `vellum`,所有 Go import 路径不变。

第一条流水线:push 到 `main` 自动上线,push 到 `dev` 只跑检查。Caddy 用 VPS 上已装的原生版本,前端 `dist` 由它 `file_server` 提供,不做 `go:embed`。前后端是两个产物,部署时先切后端、后切前端。

本次不做:Postgres 测试脚手架、auth、照片存储、Playwright。流水线里留 `go test ./...` 这一步(仓库目前没有 `_test.go`,平凡通过)。

---

## 提交序列

每一步都能独立提交,且提交后仓库是绿的。

### 1. 搬目录 → `server/`

`git mv` 进 `server/`:`cmd/ internal/ api/ migrations/ go.mod go.sum`。
留在根:`Makefile compose.yaml .env .env.example .gitignore docs/ web/`。

**`Makefile`**:每条 go 命令加 `go -C server`(`-C` 紧跟 `go`)。五个例外:

| 目标 | 改动 |
| --- | --- |
| `gen` | openapi-typescript 的路径 `../api/openapi.yaml` → `../server/api/openapi.yaml` |
| `gen-check` | pathspec 加前缀 `server/internal/api/types.gen.go`、`server/internal/api/server.gen.go`(`web/src/api/schema.d.ts` 不变) |
| `build` | `go -C server build -o ../vellum ./cmd/vellum` —— `-o` 相对 CWD,必须写 `../` |
| 新增 `test` | `go -C server test ./...` |
| 新增 `build-release` | 发布构建,产物落在 `dist/vellum`,见下 |

`build`(本地跑一下看看能不能编)与 `build-release`(要上生产的那个)是两件事,不合并:

```make
# 未在 git 仓库里(比如 CI 之外的 tar 包)时给个兜底值。
GIT_SHA ?= $(shell git rev-parse --short=7 HEAD 2>/dev/null || echo unknown)

build-release: ## 发布构建:静态链接的 linux/amd64 二进制 → dist/vellum
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
	  go -C server build -trimpath \
	    -ldflags "-s -w -X main.gitSHA=$(GIT_SHA)" \
	    -o ../dist/vellum ./cmd/vellum
```

三个标志各有各的理由:`CGO_ENABLED=0` 换来静态链接,否则 glibc 编出来的东西进不了 alpine;`GOARCH=amd64` 钉死目标架构(VPS 已确认是 amd64,与 runner 一致)——编译搬到 runner 上之后架构不再由构建环境决定,显式写出来,将来换机器时这一行就是要改的地方;`-trimpath` 去掉 runner 上的绝对路径,让构建可复现。

`main.go` 加 `var gitSHA = "dev"`,启动日志打一次,不进 `/api/healthz`。

**`.gitignore`** 四处:注释里 `internal/api/*.gen.go` 加 `server/` 前缀;`web/dist/` 的注释改为「由 CI 构建后 scp 上 VPS」;新增 `/dist/`(发布构建的产物目录);删掉 Playwright 三条(`test-results/`、`playwright-report/`、`playwright/.cache/`)。

**验证**:`make gen-check && make typecheck && make build && make test` 全绿,`make dev` 能起来。

---

### 2. `deploy/Dockerfile`

编译不在镜像里发生。`make build-release` 已经把二进制放进 `dist/`,Dockerfile 只是把它装进一个能跑的最小环境:

```dockerfile
FROM alpine:<钉小版本>
RUN apk add --no-cache ca-certificates
COPY vellum /vellum
ENTRYPOINT ["/vellum"]
CMD ["serve"]
```

构建命令 `docker build -f deploy/Dockerfile -t vellum:dev dist/`——**context 是 `dist/`**,那个目录里只有二进制一个文件。因此不需要 `.dockerignore`:没有东西可忽略,也不可能误传源码或 `.env` 进去。

- 基础镜像的小版本在实现时查当前值钉死,不写 `latest`。
- 为什么不是 `FROM scratch`:二进制确实是静态的,时区库也已经由 `cmd/vellum/main.go` 的 `_ "time/tzdata"` 编进去了,scratch 跑得起来。但 compose 的 healthcheck 要在容器内执行 `wget`,scratch 里没有;`ca-certificates` 也是将来对外发 HTTPS 时迟早要有的。alpine 多出的几 MB 买的是这两样。
- healthcheck 定义在 compose,不在 Dockerfile。

**验证**:`make build-release && docker build -f deploy/Dockerfile -t vellum:dev dist/` 成功;`docker run --rm vellum:dev` 因缺配置而拒绝启动。

---

### 3. `deploy/compose.prod.yaml` + `deploy/Caddyfile`

`deploy/` 再添两个文件。与同目录的 `Dockerfile` 不同,这两个是由 CI scp 上 VPS 的。

**`compose.prod.yaml`**:

- `db` 不写 `ports:`。
- `app` 只发布 `127.0.0.1:8080:8080`。
- `image: ghcr.io/omo-ri/vellum:${VELLUM_IMAGE_TAG:-latest}`。
- `env_file: .env`(与 `${VAR}` 插值是两回事,必须显式写)。
- 卷:`pgdata`、`vellum_data`。
- 两个服务 `restart: unless-stopped`;`logging` 配 `max-size: 10m` / `max-file: 3`。
- `app` 的 healthcheck 用 busybox `wget`;`depends_on: db: condition: service_healthy`。

**`Caddyfile`**:

```caddyfile
{
    email <站主邮箱>
}

vellum.mianhua.ru {
    encode zstd gzip
    root * /var/www/vellum/current

    request_body {
        max_size 25MB
    }

    handle /api/* {
        reverse_proxy 127.0.0.1:8080
    }

    handle /assets/* {
        header Cache-Control "public, max-age=31536000, immutable"
        file_server
    }

    handle {
        header Cache-Control "no-cache"
        try_files {path} /index.html
        file_server
    }
}
```

`/assets/*` 刻意不做 `try_files`,命中不到即 404。`index.html` 必须 `no-cache`。

同一提交里给 `server/internal/platform/httpx/httpx.go` 加 Echo 的 `IPExtractor`(两行),读 `X-Forwarded-For`。

**验证**:用手写 `.env` 在本机跑一次生产镜像,`curl localhost:8080/api/healthz` 通。

---

### 4. `deploy/deploy.sh`

由 CI 用 `ssh 'bash -s' < deploy/deploy.sh` 执行。

```
docker compose pull
docker compose up -d --wait db
docker compose run --rm app migrate up
docker compose up -d --wait
ln -sfnT /var/www/vellum/$SHA /var/www/vellum/current.tmp
mv -T /var/www/vellum/current.tmp /var/www/vellum/current
# 只保留最近 5 个 release
```

- 符号链接必须用 `ln -sfnT` + `mv -T`。
- 不自动回滚。回滚做法:把 `/opt/vellum/.env` 的 `VELLUM_IMAGE_TAG` 设成某个 `sha-xxxxxxx` + `docker compose up -d`,前端 `ln` 指回上一个 sha 目录。
- 迁移必须能被上一版代码容忍(加字段/加表/加索引安全;删列、改列类型、加无默认值的 `NOT NULL` 不安全)。生产不做 `migrate down`。

---

### 5. `.github/workflows/ci.yml`

两个 job。`ci` 每次 push 都跑;`deploy` 只在 `main` 上跑,`needs: ci` 保证前面全绿才会开始。

拆成两个 job 而不是三四个:GitHub 的 job 之间不共享文件系统,每多一个 job 就要重做一遍 checkout 与工具链安装。**「环境 / 测试 / 构建」是同一个 job 里的三组步骤**,在 Actions 的 UI 里本来就分段折叠,拆开只买到重复的 setup。`deploy` 值得独立,因为它需要 `needs` 这个真正的门禁,并且能挂 `environment: production` 把生产 Secret 圈在一个 job 里。

#### job `ci`(全部分支)

```
# --- 阶段一:环境 ---
checkout
setup-go   (go-version-file: server/go.mod, cache: true)
setup-node (node-version, cache: pnpm)
corepack enable
pnpm install --frozen-lockfile

# --- 阶段二:测试 ---
gofmt -l server/   （有输出即失败）
go vet ./...
make gen-check
make test
make typecheck

# --- 阶段三:构建 ---
make build-release            → dist/vellum
pnpm build                    → web/dist/
docker build -f deploy/Dockerfile -t <tag> dist/

# --- 阶段四:发布产物,只在 main ---
docker login ghcr + push latest 与 sha-<7位>
upload-artifact: web/dist  (name: web-dist, retention-days: 7)
```

- `GIT_SHA` 传给 `make build-release`:`GIT_SHA=${GITHUB_SHA::7}`。
- 阶段四的三步各带 `if: github.ref == 'refs/heads/main'`。docker build 本身不守卫——现在它只是 COPY,几乎不花时间,却能在 `dev` 上就验证 Dockerfile 没写坏。
- 格式检查用 `gofmt -l`(只读),不用 `make fmt`(会改文件)。
- pnpm 的 store 用环境变量覆盖:`npm_config_store_dir=${{ runner.temp }}/pnpm-store`。不改 `web/.npmrc`(那里钉的 `/mnt/z/.pnpm-store` 是本机路径,runner 上不存在)。
- GHCR 凭证用 workflow 的 `GITHUB_TOKEN`(`permissions: packages: write`),不签 PAT。

#### job `deploy`(`needs: ci`,`if: github.ref == 'refs/heads/main'`)

```
checkout                      （只为拿 deploy/ 下的三个文件）
download-artifact: web-dist   → web/dist/
生成 .env + key 集合门禁
装 SSH 私钥与 known_hosts
scp deploy/compose.prod.yaml、deploy/Caddyfile、.env 到 /opt/vellum/
scp web/dist/ 到 /var/www/vellum/<sha>/
ssh 安装 Caddyfile + systemctl reload caddy
ssh docker login → ssh bash -s < deploy/deploy.sh → ssh docker logout
轮询 https://vellum.mianhua.ru/api/healthz（60s / 每 2s）
```

- 后端二进制不走 scp:它已经在镜像里,VPS 由 `deploy.sh` 的 `docker compose pull` 取。走 scp 的只有前端 `dist` 和三个配置文件。
- ssh 传 token 走 stdin:`ssh ... 'docker login ghcr.io -u <actor> --password-stdin' <<< "$TOKEN"`。login 与部署分成两次 ssh,末尾 `docker logout`。
- known_hosts 用 Secret 固定,不 `ssh-keyscan`。
- `.env` 生成:非机密项作字面量写在 workflow 里(`APP_ENV=prod`、`HTTP_ADDR=:8080`、`SITE_TIMEZONE=Europe/Moscow`、`DB_HOST=db`、`DB_PORT=5432`、`DB_NAME=vellum`、`DB_USER=vellum`),`DB_PASSWORD` 来自 Secret。注意 `DB_HOST` 是 `db` 不是 `localhost`。
- key 门禁(单向:`.env.example` 有而 `.env` 没有即中止):
  ```bash
  keys() { grep -oE '^[A-Z_][A-Z0-9_]*=' "$1" | tr -d '='; }
  missing=$(comm -23 <(keys .env.example | sort) <(keys .env.prod | sort))
  [ -z "$missing" ] || { echo "生产 .env 缺少:$missing"; exit 1; }
  ```

**Secrets**:`SSH_HOST`、`SSH_USER`、`SSH_KEY`、`SSH_KNOWN_HOSTS`、`DB_PASSWORD`。

---

### 6. 同步 `docs/architecture.md`

- 全部 `internal/...`、`cmd/vellum`、`migrations/`、`api/openapi.yaml` 路径加 `server/` 前缀。
- 第 167 行「前端产物 `embed` 进同一个二进制」改写为由 Caddy 提供 `dist`;第 190 行 Gzip 的归因从 Cloudflare 改为 Caddy 的 `encode zstd gzip`。
- 加一条 `X-Forwarded-For` 与 Echo `IPExtractor` 的纪律。CORS 那半句不动。

---

## VPS 上的一次性人工动作

1. 装 docker engine + compose plugin。
2. 建 `deploy` 用户,加入 `docker` 组。
3. `mkdir -p /opt/vellum /var/www/vellum` 并 `chown deploy`。
4. deploy 公钥进 `~deploy/.ssh/authorized_keys`。
5. 防火墙只开 22 / 80 / 443。
6. sudoers 给 deploy 两条:
   ```
   deploy ALL=(root) NOPASSWD: /usr/bin/install -m 0644 -o root -g root /opt/vellum/Caddyfile /etc/caddy/Caddyfile, /bin/systemctl reload caddy
   ```
7. DNS:mianhua.ru 加 A 记录 `vellum` → VM 的 IPv4,TTL 300。若托管在 Cloudflare 必须是「DNS only」(灰云)。不加 AAAA。
8. 第一次 push 前验:`dig +short vellum.mianhua.ru` 只返回该 IPv4,且 `curl -I http://vellum.mianhua.ru` 通。

---

## 首次上线验证

先 push 到 `dev` 确认检查与 `docker build` 全绿,再合进 `main`,然后:

1. 镜像出现在 `ghcr.io/omo-ri/vellum`,`latest` 与 `sha-xxxxxxx` 都在,仓库是私有的。
2. Caddy 签下证书。
3. `https://vellum.mianhua.ru/api/healthz` 返回 200。
4. 浏览器打开首页,devtools 无 JS 异常。
5. `curl -I .../assets/<真实文件>` 有 `immutable`;`curl -I .../` 是 `no-cache`;`curl -o /dev/null -w '%{http_code}' .../assets/不存在.js` 是 404。
6. `ssh vps 'ss -lntp | grep 5432'` 无输出。
