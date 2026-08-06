# Vellum 本地开发。所有 Go 工具都走 go.mod 的 tool 指令（go tool <name>），
# 前端工具走 pnpm——除了 docker 与 node 本身，没有需要手动安装的东西。

# .env 是本地配置的唯一来源；export 让它同时对 docker compose、go run 和 vite 生效。
# 前缀 - 表示文件不存在时不报错（首次 clone 后 make setup 会创建它）。
-include .env
export

.DEFAULT_GOAL := help
SHELL := /bin/bash

# 前端命令一律经 corepack 调用，pnpm 的版本由 web/package.json 的 packageManager 钉死。
# 刻意不跑 corepack enable——那需要 root 权限往 /usr/bin 里放 shim，而这里并不需要它。
# 首次运行时 corepack 会下载对应版本的 pnpm；关掉它的确认提示，好让 make setup 不卡住。
COREPACK_ENABLE_DOWNLOAD_PROMPT = 0
PNPM := cd web && corepack pnpm

.PHONY: help setup dev api web db-up db-down db-reset migrate migrate-down migrate-new gen gen-check typecheck fmt build

help: ## 列出全部目标
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

setup: ## 首次 clone 后跑一次：建 .env、装前端依赖、拉 Go 依赖
	@test -f .env || (cp .env.example .env && echo "已创建 .env（来自 .env.example）")
	$(PNPM) install
	go mod download

dev: db-up migrate ## 起数据库、跑迁移，然后并行跑 api 与 web
	@trap 'kill 0' EXIT INT TERM; \
		$(MAKE) api & \
		$(MAKE) web & \
		wait

api: ## 只跑 Go 服务（:8080）
	go run ./cmd/vellum serve

web: ## 只跑 Vite 开发服务器（:5173）
	$(PNPM) dev

# --- 数据库 ---

db-up: ## 起 Postgres 并等到健康
	docker compose up -d --wait db

db-down: ## 停 Postgres（保留数据）
	docker compose down

db-reset: ## 删库重来：down -v + up + migrate
	docker compose down -v
	$(MAKE) db-up
	$(MAKE) migrate

migrate: ## 迁移到最新版本
	go run ./cmd/vellum migrate up

migrate-down: ## 回滚一个版本
	go run ./cmd/vellum migrate down

migrate-new: ## 新建一个空迁移：make migrate-new name=add_expense
	@test -n "$(name)" || (echo "用法：make migrate-new name=add_expense" && exit 1)
	go tool goose -dir migrations create $(name) sql

# --- 契约与代码生成 ---

# web/package.json 里的 typescript 钉在 ^5，不要升到 7。TypeScript 7 是 Go 重写的原生
# 编译器，不再暴露 ts.factory 那套 AST 构造 API，而 openapi-typescript 正是靠它生成
# schema.d.ts 的——升上去之后下面这条 openapi-typescript 会以
# `TypeError: Cannot read properties of undefined (reading 'createKeywordTypeNode')` 失败。
# 而 tsc --noEmit 仍然正常，所以 make typecheck 不会替你发现这件事。
# pnpm peers check 会报出这个冲突。哪天 openapi-typescript 声明支持 ^7，这段就可以删。
gen: ## 从 api/openapi.yaml 生成 Go 与 TS 两端产物
	go tool oapi-codegen -config api/cfg-types.yaml api/openapi.yaml
	go tool oapi-codegen -config api/cfg-server.yaml api/openapi.yaml
	$(PNPM) exec openapi-typescript ../api/openapi.yaml -o src/api/schema.d.ts

# 这里的 pathspec 只点名生成产物，不写目录——internal/api 与 web/src/api 里都混着
# 手写文件（handler.go、client.ts），按目录比对会让无关的手写改动也把 gen-check 判失败。
# 另注意 git diff 只看已追踪文件：新增的产物在第一次 git add 之前是 untracked，这里看不见。
gen-check: gen ## 生成后比对：产物与契约不一致即失败（CI 用）
	git diff --exit-code -- \
		internal/api/types.gen.go \
		internal/api/server.gen.go \
		web/src/api/schema.d.ts

# --- 质量闸门 ---

typecheck: ## tsc --noEmit
	$(PNPM) exec tsc --noEmit

fmt: ## gofmt
	go fmt ./...

build: ## 构建单一二进制到 ./vellum
	go build -o vellum ./cmd/vellum
