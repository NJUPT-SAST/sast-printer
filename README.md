# GoPrint

*Next-gen SAST Printer Utility*

GoPrint 是一个基于 Golang 的打印后端服务，通过 CUPS/IPP 与系统打印服务通信，并提供 REST API 供前端调用。

## 特性

- 打印机列表与详情查询
- 打印任务提交（multipart 文件上传）
- 手动双面打印（奇偶页拆分 + 二段式提交）
- 打印任务列表、状态查询（支持后台自动刷新到任务存储）
- 删除任务记录（仅删除任务存储中的记录）
- 状态细化返回（`status`、`reason`、`raw_state`）

## 技术栈

- `gin-gonic/gin`：HTTP API 框架
- `phin1x/go-ipp`：IPP/CUPS 客户端

## 运行要求

- Linux 系统并已安装/启用 CUPS
- Go 1.24+
- 当前进程用户对 CUPS 有查询/提交任务权限
- 已提供并配置 `config.yaml`

## 快速开始

1. 安装依赖

```bash
go mod tidy
```

2. 启动服务

```bash
go run main.go
```

服务启动默认读取项目根目录的 `config.yaml`，也支持传入自定义路径：

```bash
go run main.go /path/to/config.yaml
```

3. 服务地址

- 默认端口：`5001`
- Base URL：`http://localhost:5001`

## Docker 分离部署（推荐）

为减小主服务镜像体积，Office 转换服务已支持独立镜像部署。

- `goprint`：Go 后端 API（5001）
- `office-converter`：Python gRPC 转换服务（50061，容器内通信）

两者通过 Docker 网络通信，地址由 `office_conversion.grpc_address` 指向 `office-converter:50061`。
同时通过共享卷 `office-output` 交换转换后的 PDF 文件。

1. 准备配置

编辑项目根目录的 `config.yaml`，并确保：

- `office_conversion.enabled: true`
- `office_conversion.start_with_server: false`
- `office_conversion.grpc_address: office-converter:50061`
- `office_conversion.output_dir: /tmp/office-output`

2. 启动

```bash
docker compose up -d --build
```

3. 查看日志

```bash
docker compose logs -f goprint office-converter
```

4. 字体与 WPS 配置

当前 `office-converter` 镜像构建流程已自动注入字体与 `Office.conf`。请将需要的字体放入 `office_converter/assets/fonts` 下，可用的配置文件放入 `office_converter/assets/Office.conf`。由于文件太大，仓库无法提供这些文件。

如需排查转换环境，可执行以下可选检查命令：

```bash
docker exec sast-office-converter sh -lc "fc-list | wc -l"
```

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
- `DELETE /api/jobs/:id`：删除任务记录（不会向 CUPS 下发取消）

### 手动双面 Hook 接口

- `POST /api/manual-duplex-hooks/:token/continue`：提交手动双面剩余页面

### scanservjs 代理

- `ANY /sane-api/*`：反向代理到 `sane_api.target_url`（默认 `http://192.168.101.37:8080`）
- 代理会透传方法、查询参数、请求体和响应体（适用于 scanservjs 全部接口）
- 路径映射规则：`/sane-api/<path>` -> `<target>/<path>`
- 对上游返回的重定向 `Location` 会自动补全 `/sane-api` 前缀，避免跳回本地前端路由
- 鉴权策略：
    - `sane_api.auth_enabled: false` 时，直接代理（适用于可信内网）
    - `sane_api.auth_enabled: true` 时，鉴权失败直接拒绝（`401`）
    - 优先校验 `sane_api.auth_header` / `Authorization: Bearer` 是否匹配 `sane_api.auth_token`
    - 若未配置 `sane_api.auth_token` 且 `auth.enabled: true`，则走全局飞书 Bearer 鉴权

## 示例请求

### 1) 打印机列表

```bash
curl -sS http://localhost:5001/api/printers
```

### 2) 提交打印任务

```bash
curl -sS -X POST http://localhost:5001/api/jobs \
    -F printer_id=sast-color-printer \
    -F file=@printer_test.pdf
```

可选 URL 参数：

- `duplex=true|false`：是否启用双面打印（默认 `false`）
- `copies=整数`：打印份数（默认 `1`）
- `collate=true|false`：份数排列方式（默认 `true`）

示例（双面 + 2 份）：

```bash
curl -sS -X POST "http://localhost:5001/api/jobs?duplex=true&copies=2" \
    -F printer_id=sast-color-printer \
    -F file=@printer_test.pdf
```

### 2.1) 提交手动双面任务

```bash
curl -sS -X POST "http://localhost:5001/api/jobs?copies=1" \
    -F printer_id=sast-color-printer \
    -F file=@printer_test.pdf
```

若该打印机在 `config.yaml` 中配置 `duplex_mode: manual`，响应会返回 `hook_url`，用于第二轮打印。

### 2.2) 触发手动双面第二轮

```bash
curl -sS -X POST http://localhost:5001/api/manual-duplex-hooks/<token>/continue
```

### 3) 查询任务状态

```bash
curl -sS http://localhost:5001/api/jobs/29
```

### 4) 删除任务记录

```bash
curl -sS -X DELETE http://localhost:5001/api/jobs/29
```

说明：该接口仅删除任务存储中的记录，不会取消打印机上的物理任务。

### 5) 访问 scanservjs

```bash
curl -sS -H 'X-Sane-Api-Key: change_me' http://localhost:5001/sane-api/api-docs
```

等价于直接访问：`http://192.168.101.37:8080/api-docs`。

如果你使用全局飞书鉴权，也可以在启用 `auth.enabled: true` 后，使用 `Authorization: Bearer ...` 调用该路由。

## 任务状态字段

- `status`：归一化后的任务状态（如 `pending`、`processing`、`completed`、`cancelled`）
- `reason`：CUPS/IPP 原始原因（`job-state-reason`）
- `raw_state`：IPP 原始状态码

## 手动双面说明

- 启用方式：为打印机配置 `duplex_mode: manual`
- `duplex_mode: off`：关闭双面功能，按单轮正常打印
- `duplex_mode: auto`：使用打印机原生双面，按文档方向自动选择：纵向=`two-sided-long-edge`，横向=`two-sided-short-edge`
- `duplex_mode: manual`：执行手动双面（双轮提交 + hook）
- 当请求 `duplex` 未传或为 `false` 时，默认单面打印 1 份
- 当原始文件为 1 页时：无论配置为何，均按单轮打印（不启用双面）
- `reverse` 仅在单面打印时生效；双面打印不遵守此设定
- `first_pass` 可选 `even` 或 `odd`
- `reverse_first_pass` / `reverse_second_pass` 控制两轮页序是否反转
- `rotate_second_pass` 控制二轮文件是否旋转 180 度
- `pad_to_even` 控制奇数页时是否自动补空白页到偶数
- 二轮行为：访问返回的 `hook_url`，系统提交剩余页（奇数页）
- Hook 是一次性的，成功触发后将失效

## 配置文件

系统使用 YAML 配置文件，不再通过环境变量配置服务参数。

默认文件：`config.yaml`

示例：

```yaml
server:
    host: 0.0.0.0
    port: 5001

printing:
    ipp_username: goprint
    manual_duplex_hook_ttl: 30m

printers:
  - id: sast-printer
    uri: ipp://localhost:631/printers/sast-printer
    visible: true
        reverse: false
    duplex_mode: off
    first_pass: even
    pad_to_even: true
    reverse_first_pass: false
    reverse_second_pass: false
    rotate_second_pass: false
    note: ""
```

字段说明：

- `printing.ipp_username`：IPP 请求用户名
- `printing.manual_duplex_hook_ttl`：手动双面 hook 有效期（默认 `30m`）
- `sane_api.target_url`：scanservjs 后端地址
- `sane_api.auth_enabled`：是否启用 `/sane-api` 鉴权（默认 `true`）
- `sane_api.auth_header`：共享密钥请求头名称（默认 `X-Sane-Api-Key`）
- `sane_api.auth_token`：共享密钥；当 `auth_enabled: true` 时建议配置
- `printers[].uri`：按打印机配置完整 URI（支持不同打印机在不同 CUPS 地址）
- `printers[].visible`：是否在 `GET /api/printers` 中返回该打印机
- `printers[].reverse`：单面打印时是否反向页序（双面模式忽略此字段）
- `printers[].duplex_mode`：`off` / `auto` / `manual`（默认 `off`）
- `printers[].first_pass`：`even` / `odd`（默认 `even`）
- `printers[].pad_to_even`：奇数页时是否补空白页（默认 `true`）
- `printers[].reverse_first_pass`：首轮页序反转（默认 `false`）
- `printers[].reverse_second_pass`：二轮页序反转（默认 `false`）
- `printers[].rotate_second_pass`：二轮旋转 180 度（默认 `false`）
- `printers[].note`：该打印机的说明文字

