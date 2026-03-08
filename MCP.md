# MCP Server Integration

AIDaemon connects to [Model Context Protocol (MCP)](https://modelcontextprotocol.io/) servers at startup, dynamically discovering and registering their tools alongside built-in ones. This gives the LLM access to a wide ecosystem of capabilities without writing Go code.

## How It Works

```
┌───────────────┐     stdio (JSON-RPC 2.0)     ┌──────────────────┐
│   AIDaemon    │◀──────────────────────────────▶│   MCP Server     │
│  (MCP Client) │   initialize → tools/list     │  (subprocess)    │
│               │   tools/call ← result          │                  │
└───────────────┘                                └──────────────────┘
```

1. AIDaemon spawns each MCP server as a subprocess
2. Communicates via stdin/stdout using JSON-RPC 2.0
3. Discovers available tools via `tools/list`
4. Registers them in the tool registry (same as built-in tools)
5. When the LLM calls an MCP tool, AIDaemon forwards to the server via `tools/call`

## Configuration

Add MCP servers to your `~/.config/aidaemon/config.json`:

```json
{
  "mcp_servers": {
    "server-name": {
      "command": "npx",
      "args": ["-y", "@scope/package-name"],
      "env": {
        "API_KEY": "your-key"
      },
      "enabled": true
    }
  }
}
```

### Config Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `command` | string | yes | Command to run (e.g., `"npx"`, `"uvx"`, `"node"`, `"python"`) |
| `args` | string[] | yes | Arguments passed to the command |
| `env` | object | no | Additional environment variables (merged with system env) |
| `enabled` | bool | no | Set to `false` to disable without removing config (default: `true`) |

## Example Servers

### Playwright (Browser Automation)

Navigate websites, click elements, fill forms, take screenshots.

```json
{
  "mcp_servers": {
    "playwright": {
      "command": "npx",
      "args": ["-y", "@playwright/mcp@latest", "--browser", "chrome"]
    }
  }
}
```

**Requires:** Node.js

### Memory (Persistent Key-Value Store)

Store and retrieve information across conversations.

```json
{
  "mcp_servers": {
    "memory": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-memory"]
    }
  }
}
```

**Requires:** Node.js

### Filesystem (File Operations)

Read, write, search, and manage files in specified directories.

```json
{
  "mcp_servers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/path/to/allowed/dir"]
    }
  }
}
```

**Requires:** Node.js

### Context7 (API Documentation Lookup)

Look up documentation for any programming library.

```json
{
  "mcp_servers": {
    "context7": {
      "command": "npx",
      "args": ["-y", "@upstash/context7-mcp@latest"]
    }
  }
}
```

**Requires:** Node.js

### Custom Python Server

You can also run Python-based MCP servers:

```json
{
  "mcp_servers": {
    "my-python-server": {
      "command": "uvx",
      "args": ["my-mcp-package"],
      "env": {
        "MY_API_KEY": "..."
      }
    }
  }
}
```

## Finding MCP Servers

- **Official servers:** [github.com/modelcontextprotocol/servers](https://github.com/modelcontextprotocol/servers)
- **Community servers:** Search npm for `@*/mcp` or PyPI for `mcp-server-*`
- **MCP specification:** [modelcontextprotocol.io](https://modelcontextprotocol.io/)

## Troubleshooting

**Server fails to start:**
```
[mcp] ⚠️ failed to start server-name: ... (continuing without it)
```
- Check that the command is installed (`npx`, `uvx`, etc.)
- Verify `args` are correct
- Check `env` for missing API keys
- AIDaemon continues without failed servers — it doesn't crash

**Server starts but no tools appear:**
- Check daemon logs for `[mcp:server-name] discovered N tools`
- Ensure the server implements `tools/list` correctly

**Tool calls fail:**
- Check daemon logs for `[mcp:server-name:stderr]` output
- Ensure required environment variables are set
- Some servers need one-time setup (e.g., Playwright needs browser install: `npx playwright install`)

## Architecture

The MCP implementation lives in `internal/mcp/`:

| File | Purpose |
|------|---------|
| `types.go` | JSON-RPC 2.0 types, MCP protocol types, server config |
| `transport.go` | Stdin/stdout transport with request/response correlation |
| `client.go` | High-level MCP client (initialize, list tools, call tools) |
| `server.go` | Server lifecycle management + Manager for multiple servers |

MCP tools are wrapped as regular `tools.Tool` instances via `internal/tools/mcp_tool.go`, so the LLM sees them identically to built-in tools.
