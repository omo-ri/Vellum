# Vellum 本地开发。Go 工具走 go.mod 的 tool 指令，前端工具走 pnpm——除了 docker
# 与 node 本身，没有需要手动安装的东西。

# .env 是本地配置的唯一来源；export 让它同时对 docker compose、go run 和 vite 生效。
-include .env
export

.DEFAULT_GOAL := help
SHELL := /bin/bash

# 走 corepack 而不是全局 pnpm：版本由 web/package.json 的 packageManager 钉死。
COREPACK_ENABLE_DOWNLOAD_PROMPT = 0
PNPM := cd web && corepack pnpm

# 后端 module 根在 server/。-C 必须紧跟 go，它在做任何事之前 chdir——因此下面传给
# go 的相对路径都相对 server/，而传给 git 与 pnpm 的相对仓库根。
GO := go -C server

# 不在 git 仓库里（比如 CI 之外的 tar 包）时的兜底值。CI 从环境覆盖它。
GIT_SHA ?= $(shell git rev-parse --short=7 HEAD 2>/dev/null || echo unknown)

.PHONY: help setup dev api web db-up db-down db-reset migrate migrate-down migrate-new gen gen-check typecheck fmt test build build-release

help: ## 列出全部目标
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

setup: ## 首次 clone 后跑一次：建 .env、装前端依赖、拉 Go 依赖
	@test -f .env || (cp .env.example .env && echo "已创建 .env（来自 .env.example）")
	$(PNPM) install
	$(GO) mod download

dev: db-up migrate ## 起数据库、跑迁移，然后并行跑 api 与 web
	@trap 'kill 0' EXIT INT TERM; \
		$(MAKE) api & \
		$(MAKE) web & \
		wait

api: ## 只跑 Go 服务（:8080）
	$(GO) run ./cmd/vellum serve

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
	$(GO) run ./cmd/vellum migrate up

migrate-down: ## 回滚一个版本
	$(GO) run ./cmd/vellum migrate down

migrate-new: ## 新建一个空迁移：make migrate-new name=add_expense
	@test -n "$(name)" || (echo "用法：make migrate-new name=add_expense" && exit 1)
	$(GO) tool goose -dir migrations create $(name) sql

# --- 契约与代码生成 ---

# web 的 typescript 钉在 ^5，不要升到 7：TS7 不再暴露 ts.factory，而
# openapi-typescript 正是靠它生成 schema.d.ts，升上去下面这条就崩，
# 且 make typecheck 一切正常，不会替你发现。
gen: ## 从 server/api/openapi.yaml 生成 Go 与 TS 两端产物
	$(GO) tool oapi-codegen -config api/cfg-types.yaml api/openapi.yaml
	$(GO) tool oapi-codegen -config api/cfg-server.yaml api/openapi.yaml
	$(PNPM) exec openapi-typescript ../server/api/openapi.yaml -o src/api/schema.d.ts

# pathspec 只点名生成产物，不写目录：那两个目录里还混着手写文件，按目录比对会让
# 无关的手写改动也把 gen-check 判失败。
gen-check: gen ## 生成后比对：产物与契约不一致即失败（CI 用）
	git diff --exit-code -- \
		server/internal/api/types.gen.go \
		server/internal/api/server.gen.go \
		web/src/api/schema.d.ts

# --- 质量闸门 ---

typecheck: ## tsc --noEmit
	$(PNPM) exec tsc --noEmit

fmt: ## gofmt
	$(GO) fmt ./...

test: ## go test
	$(GO) test ./...

# --- 构建 ---

build: ## 构建单一二进制到 ./vellum
	$(GO) build -o ../vellum ./cmd/vellum

# 与 build 分开：这个是要上生产的那个。CGO_ENABLED=0 换来静态链接，否则进不了
# alpine；GOARCH 钉死目标架构，编译搬到 CI runner 上之后它不再由构建环境决定；
# -trimpath 去掉构建机的绝对路径。
build-release: ## 发布构建：静态链接的 linux/amd64 二进制 → dist/vellum
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
	  $(GO) build -trimpath \
	    -ldflags "-s -w -X main.gitSHA=$(GIT_SHA)" \
	    -o ../dist/vellum ./cmd/vellum
