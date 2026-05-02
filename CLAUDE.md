# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Pre-commit Checklist

Before any git commit, all three checks must pass:

```bash
make fmt && make lint && make test
```

If any step fails, the commit must not proceed until the issue is resolved.

## Build & Development Commands

```bash
make build          # CGO_ENABLED=0, injects Version/GitCommit/BuildDate via -ldflags
make test           # go test -race -covermode=atomic ./internal/...
make lint           # go vet + golangci-lint run (skips golangci-lint if not installed)
make fmt            # go fmt ./...
make tidy           # go mod tidy
make docker         # multi-arch Docker image from deployments/Dockerfile
make help           # list all targets
```

Run a single test: `go test -run TestManager_Reload -v ./internal/forwarder/`

CI runs golangci-lint v2.11 with default config (no `.golangci.yml`). Ensure `errcheck` passes — all `Close()`, `SetReadDeadline()`, `SetDeadline()` returns must be explicitly discarded with `_ =`.

## Architecture

TCP port forwarding service for jump-server (B) scenarios. Client (C) → B listen port → target (A).

```
cmd/mini-forwarder/main.go       # Entry: signal handling, hot-reload, graceful shutdown
internal/config/config.go        # YAML loading via Viper, validation, env var binding (FORWARDER_ prefix)
internal/forwarder/forwarder.go  # Core engine: Manager + Forwarder types, accept loop, relay
internal/logger/logger.go        # Global zap logger (L), json/console formats
```

### Concurrency Model

No channels — uses `sync.WaitGroup`, `sync.Map`, `sync.Once`, `atomic.Int64`, and `sync.RWMutex`.

- **Manager** owns `map[string]*Forwarder` protected by `sync.RWMutex`
- Each **Forwarder** has one `acceptLoop` goroutine; per-connection `handleConn` goroutines tracked by `connWG`
- Active connections stored in `sync.Map conns` for force-close during shutdown
- Mutable config fields (timeouts, retries) protected by per-forwarder `cfgMu` (`sync.RWMutex`) — `handleConn` reads via `RLock`, `Reload` writes via `Lock`

### Key Patterns

- **Hot-reload** (`Manager.Reload`): diff-based — unchanged rules update config in-place via `cfgMu`; changed rules stop/start. `sync.Once` on `stop()` prevents double-stop during replace.
- **Graceful shutdown**: cancel contexts → close listeners → force-close all tracked connections → wait for `connWG` drain (10s per forwarder, configurable global `shutdown_timeout`)
- **Half-close relay** (`relay`): bidirectional `io.Copy`, then `CloseWrite()` on underlying `*net.TCPConn` to signal EOF. Uses `unwrapConn()` to strip `idleTimeoutConn` wrapper.
- **Idle timeout** (`idleTimeoutConn`): wraps `net.Conn`, sets read/write deadline before every `Read`/`Write`. `"0"` in config disables it (for Redis/MySQL long-lived connections).
- **TCP tuning** (`setTCPParams`): called on raw `*net.TCPConn` before wrapping. Sets KeepAlive, NoDelay. Explicitly avoids `SetLinger(0)` (prevents RST).

## Configuration

YAML at `configs/forwarder.yaml`. Per-rule fields: `name`, `listen`, `target`, `dial_timeout`, `idle_timeout`, `max_retries`, `retry_interval`, `keep_alive`. Global: `log_level`, `log_format`, `hot_reload`, `shutdown_timeout`. All overridable via `FORWARDER_` env vars.

## Release

Push tag `v*` → GitHub Actions builds binaries for linux/darwin (amd64+arm64) and windows-amd64, creates GitHub Release with SHA256 checksums.
