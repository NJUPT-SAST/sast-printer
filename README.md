# GoPrint

GoPrint 是一个基于 Golang 的打印后端服务，通过 CUPS/IPP 与系统打印服务通信，并提供 REST API 供前端调用。

## 特性

- 打印机列表与详情查询
- 打印任务提交（multipart 文件上传）
- 打印任务列表、状态查询
- 打印任务取消
- 状态细化返回（`status`、`reason`、`raw_state`）

## 技术栈

- `gin-gonic/gin`：HTTP API 框架
- `phin1x/go-ipp`：IPP/CUPS 客户端

## 运行要求

- Linux 系统并已安装/启用 CUPS
- Go 1.21+
- 当前进程用户对 CUPS 有查询/提交任务权限

## 快速开始

1. 安装依赖

```bash
go mod tidy
```

2. 启动服务

```bash
go run main.go
```

3. 服务地址

- 默认端口：`5001`
- Base URL：`http://localhost:5001`

## API 概览

### 健康检查

- `GET /health`

### 打印机接口

- `GET /api/printers`：获取打印机列表
- `GET /api/printers/:id`：获取打印机详情

### 任务接口

- `POST /api/jobs`：提交打印任务
- `GET /api/jobs`：获取任务列表
- `GET /api/jobs/:id`：获取任务详情/状态
- `DELETE /api/jobs/:id`：取消任务

## 示例请求

### 1) 打印机列表

```bash
curl -sS http://localhost:5001/api/printers
```

### 2) 提交打印任务

```bash
curl -sS -X POST http://localhost:5001/api/jobs \
    -F printer_id=sast-color-printer \
    -F file=@printer_test.pdf \
    -F copies=1
```

### 3) 查询任务状态

```bash
curl -sS http://localhost:5001/api/jobs/29
```

### 4) 取消任务

```bash
curl -sS -X DELETE http://localhost:5001/api/jobs/29
```

## 任务状态字段

- `status`：归一化后的任务状态（如 `pending`、`processing`、`completed`、`cancelled`）
- `reason`：CUPS/IPP 原始原因（`job-state-reason`）
- `raw_state`：IPP 原始状态码

## 项目结构

```
goprint/
├── api/
│   ├── handlers.go
│   └── router.go
├── config/
│   └── config.go
├── cups/
│   ├── client.go
│   └── models.go
├── main.go
├── go.mod
├── go.sum
└── README.md
```
