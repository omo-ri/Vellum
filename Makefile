-include .env
export

.DEFAULT_GOAL := help
SHELL := /bin/bash

COREPACK_ENABLE_DOWNLOAD_PROMPT = 0
PNPM := cd web && corepack pnpm

GO := go -C server

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

gen: ## 从 server/api/openapi.yaml 生成 Go 与 TS 两端产物
	$(GO) tool oapi-codegen -config api/cfg-types.yaml api/openapi.yaml
	$(GO) tool oapi-codegen -config api/cfg-server.yaml api/openapi.yaml
	$(PNPM) exec openapi-typescript ../server/api/openapi.yaml -o src/api/schema.d.ts

gen-check: gen ## 生成后比对：产物与契约不一致即失败
	git diff --exit-code -- \
		server/internal/api/types.gen.go \
		server/internal/api/server.gen.go \
		web/src/api/schema.d.ts

typecheck: ## tsc --noEmit
	$(PNPM) exec tsc --noEmit

fmt: ## gofmt
	$(GO) fmt ./...

test: ## go test
	$(GO) test ./...

build: ## 构建单一二进制到 ./vellum
	$(GO) build -o ../vellum ./cmd/vellum

build-release: ## 发布构建：静态链接的 linux/amd64 二进制 → dist/vellum
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
	  $(GO) build -trimpath \
	    -ldflags "-s -w -X main.gitSHA=$(GIT_SHA)" \
	    -o ../dist/vellum ./cmd/vellum
