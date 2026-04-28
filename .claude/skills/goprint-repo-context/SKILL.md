---
name: goprint-repo-context
description: 'Understand the GoPrint repository quickly. Use when asked about architecture, API routes, auth flow, scanservjs proxy, CUPS printing, manual duplex, office conversion, or where to edit for a feature/fix in this repo.'
argument-hint: 'question or task about this repository'
user-invocable: true
---

# GoPrint Repo Context

## Purpose
快速建立对本仓库的可执行认知，优先定位到正确文件与修改入口，避免盲目全仓搜索。

## When To Use
- 用户询问本仓库结构、模块职责、关键数据流。
- 需要判断某个需求应修改哪些文件。
- 需要快速验证 API 路由、鉴权、打印流程、代理行为。
- 需要同步维护文档（README、openapi）与配置（config.example.yaml）。

## Quick Map
- 程序入口: `main.go`
- 路由总线: `api/router.go`
- 鉴权中间件: `api/auth_middleware.go`
- scanservjs 代理: `api/sane_api_proxy.go`
- 核心打印处理: `api/handlers.go`
- 手动双面/N-up: `api/manual_duplex.go`
- 配置模型与校验: `config/config.go`
- 示例配置: `config.example.yaml`
- API 文档: `openapi.yaml`
- 项目说明: `README.md`

## Core Runtime Flow
1. 从 `main.go` 启动，读取 `config.yaml`，调用 `api.SetConfig()` 注入全局配置。
2. `api/router.go` 注册 `/api/*` 业务接口、`/sane-api/*` 代理接口与前端静态回退。
3. 打印任务入口为 `POST /api/jobs`，在 `api/handlers.go` 中执行参数解析、文件处理、打印提交。
4. 手动双面策略与页序处理在 `api/manual_duplex.go`。
5. scanservjs 请求走 `/sane-api/*`，在 `api/sane_api_proxy.go` 中转发到 `sane_api.target_url`。

## Routing And Auth Notes
- `/api/printers`、`/api/jobs` 默认使用 `AuthRequired()`。
- `/sane-api/*` 使用 `SaneAPIAuthRequired()`：
  - `sane_api.auth_enabled: false` 时允许直接代理。
  - `sane_api.auth_enabled: true` 时鉴权失败直接返回 401。
- 代理响应会处理 `Location` 重写，避免丢失 `/sane-api` 前缀。

## Task-Oriented Edit Guide
- 新增/调整接口: 先改 `api/router.go`，再实现 handler，最后同步 `openapi.yaml` + `README.md`。
- 改鉴权策略: `api/auth_middleware.go` + `config/config.go`（默认值与 validate 需同时更新）。
- 改代理行为: `api/sane_api_proxy.go`，注意重定向安全与头透传。
- 改打印行为: `api/handlers.go` 与 `api/manual_duplex.go`，必要时补充配置字段说明。

## Verification Checklist
每次变更后优先执行：
1. `go build ./...`
2. 若改 API: 手工 curl 验证关键路由（成功路径 + 失败路径）
3. 若改配置: 检查 `config.example.yaml` 与 `README.md` 字段说明是否一致
4. 若改接口契约: 更新 `openapi.yaml`

## Repo Conventions
- 优先小步改动，避免大范围无关重构。
- 保持现有中文注释风格与错误返回结构（`error`/`details`）。
- 涉及安全逻辑（鉴权、重定向）时，先考虑“默认拒绝”。
- 若工作区存在与当前任务无关的改动，不要回滚它们。
