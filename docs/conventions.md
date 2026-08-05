# 工程纪律

这份文档记录的是**跨越单个文件、无法由编译器或 linter 强制**的约定。能被工具强制的（格式、命名、未使用变量）不写在这里，交给 `gofmt`、`golangci-lint` 与 `tsc`。

## GET 不得有副作用

**任何 `GET`（以及 `HEAD`）请求都不得改变服务端状态。** 所有写操作一律走 `POST` / `PUT` / `PATCH` / `DELETE`。

**这不是风格偏好，是本项目不上 CSRF 中间件的安全前提。** 会话通过 `SameSite=Lax` 的 cookie 传递，浏览器在跨站请求中：

- 会带上 cookie 的：顶层导航发起的 `GET`（别人的页面上一个指向本站的链接或 `<img src>`）。
- 不会带上 cookie 的：跨站的 `POST` / `PUT` / `DELETE`，以及所有跨站的 `fetch`/`XHR`。

也就是说，`SameSite=Lax` 只在「写操作全部是非 `GET` 方法」这个前提下才等价于 CSRF 防护。一旦出现一个会写库的 `GET`，攻击者只要让站主访问一个包含 `<img src="https://vellum.example/api/...">` 的页面，就能带着站主的会话完成那次写入——而站内没有任何一层会拦下它。

具体到实现：

- 「今天做了」（`cook`）、月末结算的惰性触发、登出，全部是 `POST`。
- **惰性结算是这条规则的一个例外，且是唯一被允许的例外**：任意 API 请求（含 `GET`）进入时都可能补齐月末结算。它被允许，是因为它满足两个条件——(1) 它不接受任何来自请求的输入，纯粹是时间的函数；(2) 它是幂等的，同一个月只会结算一次。攻击者诱发它，得到的结果与站主自己打开页面完全相同，因此没有攻击面。**除此之外不得再有第二个「有副作用的 GET」，新增时必须先在这里论证为什么它同样没有攻击面。**

## OpenAPI 契约是唯一真相

`api/openapi.yaml` 是前后端之间的唯一约定。

- Go 端与 TS 端的类型**一律生成**，生成产物提交进版本库。
- 改接口的顺序永远是：先改 `openapi.yaml` → `make gen` → 再改实现。**不允许**先写实现再回头补契约。
- `make gen-check` 在 CI 中重新生成并比对，不一致即失败。因此手改生成文件（`*.gen.go`、`schema.d.ts`）没有意义——下一次 `make gen` 就会覆盖，而 CI 会先一步把它拦下。

## 配置无默认值

`internal/platform/config` 读取的每一个环境变量都**没有默认值，缺一即启动失败**。

理由：有默认值的配置项会在生产上静默地用错值跑起来——一个连到本地库的默认 DSN、一个 `dev` 的默认 `APP_ENV`，本地永远复现不了，上线才发现。启动失败是刺耳的，但它发生在部署那一秒，而不是三周后。

`.env` 只被 Makefile `include` 并 export，**二进制本身不认识 `.env` 文件**。这样本地与生产的配置来源是同一条路径（进程环境变量），少一种「本地能跑、线上不行」的原因。

## TypeScript 钉在 5.x

`web/package.json` 里 `typescript` 是 `^5`，**不要升到 7**。

TypeScript 7 是 Go 重写的原生编译器，它不再暴露 `ts.factory` 那套 AST 构造 API，而
`openapi-typescript` 正是靠它生成 `schema.d.ts` 的。升上去之后 `make gen` 会以
`TypeError: Cannot read properties of undefined (reading 'createKeywordTypeNode')` 失败
——而 `tsc --noEmit` 仍然正常，所以 `make lint` 不会替你发现这件事。

`pnpm peers check` 会报出这个冲突。哪天 `openapi-typescript` 声明支持 `^7` 了，这条就可以删。

## 模块边界

`finance` / `recipe` / `album` **不得互相 import**。需要共享的能力下沉到 `internal/platform`。

数据库层面同样：每个模块只访问自己的 schema，跨模块外键不存在。

## 时间

- 数据库一律存 UTC（`timestamptz`）。
- 「今天」「本月」一律按**站点时区**（`SITE_TIMEZONE`）计算，不是 UTC 日，也不是浏览器时区日。
- 业务代码不直接调 `time.Now()`，一律走 `platform` 提供的时钟接口——否则涉及时间的规则无法被测试。

## 金额

一律以 RUB 最小单位（копейка）存为 `bigint`，**禁止浮点**。前端展示时才除以 100。
