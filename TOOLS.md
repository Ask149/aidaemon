# Tools Guide

AIDaemon's tool system lets the LLM interact with the outside world — reading files, running commands, searching the web, and more. You can extend it by writing your own built-in tools or connecting MCP servers.

## How Tools Work

When you send a message, the LLM decides whether it needs to call a tool:

```
You: "What's in my calendar today?"
           │
    ┌──────▼───────┐
    │   LLM thinks  │──▶ "I need the calendar tool"
    └──────┬───────┘
           │
    ┌──────▼───────┐     ┌──────────────┐
    │ Tool Registry │────▶│ Permission   │
    │ (lookup)      │     │ Check        │
    └──────┬───────┘     └──────┬───────┘
           │                     │ ✅
    ┌──────▼─────────────────────▼──┐
    │    Tool.Execute(ctx, args)     │
    └──────────────┬────────────────┘
                   │
    ┌──────────────▼────────────────┐
    │ Result sent back to LLM       │
    │ (LLM formulates response)     │
    └───────────────────────────────┘
```

The LLM can call multiple tools in sequence to accomplish complex tasks.

## Built-in Tools

| Tool | Description |
|------|-------------|
| `read_file` | Read file contents from allowed directories |
| `write_file` | Create or overwrite files |
| `run_command` | Execute shell commands (destructive commands blocked) |
| `web_fetch` | Fetch and extract text from any URL |
| `web_search` | Search the web via Brave Search API or DuckDuckGo fallback |
| `manage_cron` | Create, list, pause, resume, and delete scheduled tasks |
| `calendar` | Access Google Calendar (list events, create events) |
| `email` | Access Gmail (summary, list, search, read emails) |
| `goals` | Track personal goals (log entries, check progress) |

## Writing a Custom Tool

### 1. Implement the Tool interface

Create a new file in `internal/tools/builtin/`:

```go
// internal/tools/builtin/my_tool.go
package builtin

import (
    "context"
    "fmt"
)

type MyTool struct {
    // Add any dependencies here
}

func NewMyTool() *MyTool {
    return &MyTool{}
}

func (t *MyTool) Name() string {
    return "my_tool"
}

func (t *MyTool) Description() string {
    // This is what the LLM reads to decide when to use your tool.
    // Be specific about what it does and when to use it.
    return "Does something useful. Use this when the user asks about X."
}

func (t *MyTool) Parameters() map[string]interface{} {
    // JSON Schema describing the tool's input parameters.
    // The LLM generates arguments matching this schema.
    return map[string]interface{}{
        "type": "object",
        "properties": map[string]interface{}{
            "query": map[string]interface{}{
                "type":        "string",
                "description": "The search query or input.",
            },
            "limit": map[string]interface{}{
                "type":        "integer",
                "description": "Maximum number of results. Defaults to 10.",
            },
        },
        "required": []string{"query"},
    }
}

func (t *MyTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
    // Extract arguments (they come as interface{} from JSON)
    query, _ := args["query"].(string)
    if query == "" {
        return "", fmt.Errorf("query is required")
    }

    limit := 10
    if l, ok := args["limit"].(float64); ok {
        limit = int(l)
    }

    // Do the actual work
    result := fmt.Sprintf("Found %d results for %q", limit, query)
    return result, nil
}
```

### 2. Register the tool

In `cmd/aidaemon/main.go`, find the `setupTools()` function and add your tool:

```go
func setupTools(reg *tools.Registry) {
    // ... existing tools ...
    reg.Register(builtin.NewMyTool())
}
```

### 3. Build and test

```bash
go build ./cmd/aidaemon/
./aidaemon
# Then ask the AI to use your tool
```

## Tool Interface Reference

```go
type Tool interface {
    // Unique identifier (lowercase_with_underscores)
    Name() string

    // Human-readable description (LLM uses this to decide when to call)
    Description() string

    // JSON Schema for input parameters
    Parameters() map[string]interface{}

    // Execute the tool. Returns output string or error.
    Execute(ctx context.Context, args map[string]interface{}) (string, error)
}
```

### Tips for Good Tools

- **Description matters** — the LLM reads it to decide when to call your tool. Be specific.
- **Validate inputs** — args come from LLM output and may be malformed.
- **Return useful strings** — the LLM reads your output to formulate its response.
- **Handle errors gracefully** — return descriptive error messages, not stack traces.
- **Use context** — respect `ctx.Done()` for long-running operations.

### Example: The Goals Tool

Here's a real example from the codebase — the `goals` tool that tracks personal goals:

```go
func (t *GoalsTool) Name() string { return "goals" }

func (t *GoalsTool) Description() string {
    return "Track personal goals like exercise, water intake, or meditation. " +
        "Use 'log' to record a completed goal entry, or 'progress' to check progress."
}

func (t *GoalsTool) Parameters() map[string]interface{} {
    return map[string]interface{}{
        "type": "object",
        "properties": map[string]interface{}{
            "action": map[string]interface{}{
                "type": "string",
                "enum": []string{"log", "progress"},
            },
            "goal": map[string]interface{}{
                "type":        "string",
                "description": "The goal name, e.g. 'exercise', 'water'",
            },
        },
        "required": []string{"action", "goal"},
    }
}
```

The tool uses an `action` parameter with an enum to support multiple operations in a single tool — this is a common pattern.

## Permissions

Tools are subject to the permission system. Configure in `config.json`:

```json
{
  "tool_permissions": {
    "my_tool": {
      "mode": "allow_all"
    }
  }
}
```

Modes:
- `allow_all` — no restrictions (default for most tools)
- `whitelist` — only allow specific paths/commands/domains
- `deny` — block specific patterns

See [SECURITY.md](SECURITY.md) for details.

## MCP Tools

In addition to built-in tools, AIDaemon supports [MCP](https://modelcontextprotocol.io/) servers that provide additional tools. See [MCP.md](MCP.md) for details.
