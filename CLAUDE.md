# CLAUDE.md

## Commands

```bash
make deps        # go mod download && go mod tidy
make build       # go build -o bin/goprint main.go
make run         # go run main.go
make test        # go test ./... (currently no test files)
make clean       # rm -rf bin/ && go clean

# Custom config path
go run main.go /path/to/config.yaml

# Format code
goimports -w api/*.go
```

## Architecture

```
main.go              → load config, init temp dir, setup router, start server
api/
  router.go          → Gin routes (public, auth, bot, sane proxy, SPA fallback)
  app_config.go      → global config singleton, CUPS client factory, temp dir
  handlers.go        → print job submit, preview, duplex hooks, applyCopiesMode
  auth_handlers.go   → Feishu OAuth (code-login, authorize-url, JSSDK ticket)
  auth_middleware.go → Bearer token validation
  feishu_sdk.go      → Feishu API client (token exchange, user info, maskSensitive)
  feishu_export.go   → Feishu doc/wiki export to PDF, newFeishuClient
  bot_handlers.go    → event dispatcher entry point (initBotDispatcher, HandleBotEvent)
  bot_actions.go     → message/card event processing, handleBotPrint, persistBotJob
  bot_card.go        → CardKit v2 card JSON builders, printer options
  bot_messaging.go   → sendCard, sendTextMsg, notifyUserCard, disableCardButtons
  bot_session.go     → in-memory session CRUD, TTL cleaner goroutine
  manual_duplex.go   → page splitting, reverse, rotate, countPDFPages
  nup_pdf.go         → N-up imposition (2/4/6), validNup
  submit_queue.go    → chan struct{} semaphore (buffer 1), serializes all print ops
  bitable_job_store.go → Feishu Bitable CRUD for job records
  job_status_poller.go  → background CUPS job state polling (30s interval, sync.Once)
  sane_api_proxy.go  → reverse proxy to scanservjs, Location header rewriting
  rate_limit_middleware.go → IP-based 10 QPS sliding window, 10s block
config/
  config.go          → YAML config loading, validation, $ref resolution, defaults
cups/
  client.go          → IPP/CUPS client (go-ipp wrapper)
  models.go          → Printer, PrintJob, PrintOptions
office_converter/
  server.py          → Python gRPC server using pywpsrpc (WPS)
proto/
  office_converter.proto → gRPC definition for Office→PDF conversion
```

## Key Design Decisions

- **No env vars** — all config via `config.yaml` (see `config.example.yaml` for template that's safe to commit)
- **Single-file serialization** — `submit_queue.go` uses a 1-buffer channel so only one print/preview op runs at a time
- **Temp dir wipe on startup** — `InitTempDir` removes all files including `bot-sessions/` at every boot
- **No Go unit tests** — manual CLI tools under `cmd/` for integration testing
- **Frontend embedded** — `public/` contains built SPA, synced cross-repo via `frontend-artifact-sync.yml`
- **Bot sessions in-memory** — lost on restart; cleaner goroutine runs on `bot.card_timeout` interval

## Gotchas

- **Rate limiter block is 10s, but Retry-After header says 60s** — inconsistent values in `rate_limit_middleware.go`
- **`config.yaml` contains real credentials** — do not commit to public repos; use `config.example.yaml` as template
- **`duplex_mode: "manuel"` is tolerated** — normalized to `"manual"` in config validation (common typo fix)
- **Manual duplex hooks have no auth middleware** — intentionally unauthenticated (card clicks + web hook callbacks)
- **DELETE `/api/jobs/:id` only removes Bitable record** — does not cancel physical CUPS job
- **Office converter subprocess** — when `start_with_server: true`, the Python gRPC server spawns as child process; not killed on exit, only context cancellation
- **`_cloud_doc` is a special `file_type_defaults` key** — applied when Bot receives a cloud doc link, not a real file extension
- **`chooseAutoDuplexSides` uses majority vote** — landscape pages > portrait? `two-sided-short-edge` : `two-sided-long-edge`
