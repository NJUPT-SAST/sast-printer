# CLAUDE.md

## Commands

```bash
make deps        # go mod download && go mod tidy
make build       # go build -o bin/goprint main.go
make run         # go run main.go
make test        # go test ./...
make clean       # rm -rf bin/ && go clean

# Custom config path
go run main.go /path/to/config.yaml

# Format code
gofmt -w $(find api config cups -name '*.go')
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
  bot_card_adapter.go → thin adapter between api orchestration and api/bot cards
  bot_messaging.go   → sendCard, sendTextMsg, notifyUserCard, disableCardButtons
  bot_session.go     → in-memory session CRUD, TTL cleaner goroutine
  manual_duplex.go   → manual duplex pending hooks, page splitting, reverse, rotate
  submit_queue.go    → config adapter for the submitqueue package timeout
  bitable_job_store.go → Feishu Bitable CRUD for job records
  job_status_poller.go  → CUPS polling plus stale Bitable cleanup (47m17s interval)
  sane_api_proxy.go  → reverse proxy to scanservjs, Location header rewriting
  rate_limit_middleware.go → IP-based 10 QPS sliding window, 10s block
  bot/
    cards.go         → CardKit v2 card JSON builders, printer options, page validation
    cards_test.go    → card builder regression tests
  conversion/
    client.go        → accepted upload formats, image→PDF, Office converter gRPC client
  pb/                → generated Office converter gRPC bindings
  pdfutil/
    nup.go           → N-up imposition (2/4/6) and layout selection
  submitqueue/
    queue.go         → single-lane print/preview queue primitive
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
- **Single-file serialization** — `api/submitqueue` uses a 1-buffer channel so only one print/preview op runs at a time; timeout is `printing.queue_wait_timeout`
- **Temp dir wipe on startup** — `InitTempDir` removes all files including `bot-sessions/` at every boot
- **Go unit tests** — keep focused regression tests near changed API behavior and helpers
- **Frontend embedded** — `public/` contains built SPA, synced cross-repo via `frontend-artifact-sync.yml`
- **Bot sessions in-memory** — lost on restart; cleaner goroutine runs on `bot.card_timeout` interval
- **Bot package boundary** — `api/bot` contains pure card/UI builders; Bot event handling stays in `api` because it orchestrates unexported print, Feishu, CUPS, Bitable, and manual-duplex helpers

## Gotchas

- **Rate limiter block is 10s, but Retry-After header says 60s** — inconsistent values in `rate_limit_middleware.go`
- **`config.yaml` contains real credentials** — do not commit to public repos; use `config.example.yaml` as template
- **`duplex_mode: "manuel"` is tolerated** — normalized to `"manual"` in config validation (common typo fix)
- **Manual duplex hooks have no auth middleware** — intentionally unauthenticated (card clicks + web hook callbacks)
- **Printer detail can include `active_job_warning`** — sourced from Bitable `pending` jobs or unexpired `pending_manual_continue` hooks
- **Stale Bitable cleanup is time-based** — `pending` / `pending_manual_continue` records older than 12h are marked `completed`
- **DELETE `/api/jobs/:id` only removes Bitable record** — does not cancel physical CUPS job
- **Office converter subprocess** — when `start_with_server: true`, the Python gRPC server spawns as child process; not killed on exit, only context cancellation
- **`_cloud_doc` is a special `file_type_defaults` key** — applied when Bot receives a cloud doc link, not a real file extension
- **`chooseAutoDuplexSides` uses majority vote** — landscape pages > portrait? `two-sided-short-edge` : `two-sided-long-edge`
