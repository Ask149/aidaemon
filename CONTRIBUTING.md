# Contributing to AIDaemon

Thanks for your interest in contributing! This guide covers how to get started.

## Development Setup

```bash
# Clone
git clone https://github.com/Ask149/aidaemon.git
cd aidaemon

# Build
go build ./...

# Run checks
go vet ./...
go test ./...

# Install pre-commit hook (blocks credential leaks)
python3 .githooks/install.py
```

## Project Structure

```
cmd/aidaemon/          Entry point
internal/
  auth/                OAuth + token management
  config/              Configuration
  httpapi/             REST API
  mcp/                 MCP client (JSON-RPC 2.0)
  permissions/         Tool access control
  provider/copilot/    GitHub Copilot API
  store/               SQLite persistence
  telegram/            Telegram bot
  tools/builtin/       Built-in tools
  tools/               Tool registry
```

## Code Style

- **Go conventions** — `gofmt`, `go vet`
- **Error handling** — return errors, never panic in library code
- **Logging** — use `log.Printf` with `[component]` prefix
- **Concurrency** — document any shared state; prefer channels or `sync` primitives
- **Comments** — godoc format, explain _why_ not _what_

## Making Changes

1. **Fork** the repository
2. **Create a branch** from `main` (`feat/description` or `fix/description`)
3. **Make your changes** — keep commits focused and atomic
4. **Build and test:**
   ```bash
   go build ./...
   go vet ./...
   go test ./...
   ```
5. **Open a pull request** with a clear description of what and why

## Adding a Built-in Tool

1. Create `internal/tools/builtin/your_tool.go`
2. Implement the `tools.Tool` interface:
   ```go
   type YourTool struct{}

   func (t *YourTool) Name() string                              { return "your_tool" }
   func (t *YourTool) Description() string                       { return "..." }
   func (t *YourTool) Parameters() map[string]interface{}         { return schema }
   func (t *YourTool) Execute(ctx, args) (string, error)          { /* ... */ }
   ```
3. Register in `cmd/aidaemon/main.go` → `setupTools()`

## Adding an MCP Server

Add to your `~/.config/aidaemon/config.json`:

```json
{
  "mcp_servers": {
    "your-server": {
      "command": "npx",
      "args": ["-y", "@scope/your-mcp-server"]
    }
  }
}
```

Tools are auto-discovered and registered at startup.

## Reporting Issues

- Use [GitHub Issues](https://github.com/Ask149/aidaemon/issues)
- Include: Go version, OS, config (redact tokens), log output, steps to reproduce

## Security

If you discover a security vulnerability, please report it privately. See [SECURITY.md](SECURITY.md).
