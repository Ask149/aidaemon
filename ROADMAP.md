# AIDaemon Roadmap

## Vision

Transform AIDaemon from a **chatbot** into a **full computer control agent** accessible via Telegram, with capabilities matching OpenCode but optimized for mobile/remote access.

## Current State (v0.1)

✅ **Working features:**
- Chat with 13+ premium models (GPT-5, Claude Opus 4.6, Gemini, etc.)
- Streaming responses with live typing indicator
- Dynamic model discovery and switching
- Persistent conversations (SQLite)
- Secure single-user access
- Multi-chat support

❌ **Missing (vs OpenCode):**
- No tool use / function calling
- No file access
- No shell execution
- No web search
- No MCP integration
- No browser automation
- Plain text only (no markdown)
- No image input
- Hard context truncation

## Development Phases

### Phase 1: Foundation — Tool Use Framework (Week 1)

**Goal:** Enable LLM to call tools via OpenAI function calling format.

**Estimated time:** 12 hours

#### 1.1 Tool Framework (8h)

**New structure:**
```
internal/tools/
  tool.go           # Tool interface
  registry.go       # Tool registry + execution engine
  builtin/
    read_file.go    # Read local files
    write_file.go   # Write/create files
    run_command.go  # Execute shell commands
    web_fetch.go    # Fetch URLs
```

**Tool interface:**
```go
type Tool interface {
    Name() string
    Description() string
    Parameters() map[string]interface{}  // JSON Schema
    Execute(ctx context.Context, args map[string]interface{}) (string, error)
}
```

**Registry responsibilities:**
- Register tools at startup
- Validate tool calls from LLM
- Execute tools with error handling
- Return results in OpenAI format

#### 1.2 Built-in Tools (4h)

**read_file:**
- Read files from user's home directory
- Security: whitelist `~/Documents`, `~/Projects`, `~/Desktop`
- Return plain text content
- Handle binary files gracefully (return "binary file")

**write_file:**
- Create or overwrite files
- Security: same whitelist as read_file
- Create parent directories if needed
- Return success message

**run_command:**
- Execute shell commands via `exec.Command`
- Security: start with read-only commands (`ls`, `cat`, `grep`, `find`)
- Timeout: 30 seconds max
- Return stdout + stderr combined

**web_fetch:**
- HTTP GET any URL
- Extract text content (strip HTML tags via `goquery`)
- Timeout: 10 seconds
- Return clean text

#### 1.3 Copilot Integration (already done)

Copilot API supports OpenAI function calling format:

**Request:**
```json
{
  "model": "gpt-4o",
  "messages": [...],
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "read_file",
        "description": "Read contents of a file",
        "parameters": {
          "type": "object",
          "properties": {
            "path": {"type": "string", "description": "Absolute file path"}
          },
          "required": ["path"]
        }
      }
    }
  ]
}
```

**Response:**
```json
{
  "choices": [{
    "message": {
      "role": "assistant",
      "tool_calls": [{
        "id": "call_abc123",
        "type": "function",
        "function": {
          "name": "read_file",
          "arguments": "{\"path\": \"~/Documents/notes.txt\"}"
        }
      }]
    }
  }]
}
```

**Execution loop:**
1. Send request with tools
2. LLM responds with `tool_calls`
3. Execute each tool → collect results
4. Send tool results as new messages with role "tool"
5. LLM continues with tool outputs in context
6. Repeat until `finish_reason: "stop"` (no more tool calls)

#### 1.4 Telegram Integration (2h)

Update `handleMessage()` in `bot.go`:
```go
func (tb *Bot) handleMessage(...) {
    // Existing code...
    
    for {
        resp := tb.provider.Chat(ctx, req)  // or Stream
        
        if resp.ToolCalls == nil {
            // No tools → normal response
            break
        }
        
        // Execute tools
        toolResults := tb.executeTool(resp.ToolCalls)
        
        // Add tool results to messages
        req.Messages = append(req.Messages, toolResults...)
        
        // Loop: LLM processes tool outputs
    }
}
```

**Success criteria:**
- User says "Read ~/Documents/test.txt"
- LLM calls `read_file` tool
- Daemon executes and returns content
- LLM summarizes the file content
- User sees summary in Telegram

---

### Phase 2: Intelligence Layer (Week 2)

**Goal:** Richer output formatting and context management.

**Estimated time:** 9 hours

#### 2.1 Markdown Formatting (2h)

Telegram supports **MarkdownV2** mode:
```
*bold* _italic_ __underline__ ~strike~
`code` ```language\nblock\n```
[link](url)
```

**Implementation:**
- Parse LLM markdown output
- Convert to Telegram MarkdownV2
- Handle escaping (`.`, `!`, `(`, `)`, etc.)
- Send with `parse_mode: "MarkdownV2"`

#### 2.2 Rich System Prompt (1h)

Load from `~/.config/aidaemon/system_prompt.md`:
```markdown
You are Ashish's personal AI assistant with full computer control.

# Access
- Local files in ~/Documents, ~/Projects, ~/Desktop
- Shell command execution (read-only by default)
- Web search and fetching
- Calendar and tasks via MCP

# Context
- Name: <your name>
- Location: <your city>
- Timezone: <your timezone>
- Role: <your role>

# Preferences
- Be direct and concise
- Use technical language
- Proactive suggestions
- Show code in proper formatting
```

**Load at startup:** Prepend to every conversation.

#### 2.3 Image Support (3h)

**Telegram → Bot:**
- Telegram sends images as `file_id`
- Download via `bot.GetFile(fileID)`
- Base64-encode

**Bot → LLM:**
```json
{
  "role": "user",
  "content": [
    {"type": "text", "text": "What's in this image?"},
    {
      "type": "image_url",
      "image_url": {"url": "data:image/jpeg;base64,..."}
    }
  ]
}
```

**Models with vision:** GPT-5, Claude Opus 4.6, Gemini

#### 2.4 Context Compaction (3h)

When message count hits limit (20):
```go
func (s *Store) CompactHistory(chatID string) error {
    // Get oldest 10 messages
    old := s.GetHistory(chatID, limit=10)
    
    // Summarize with cheap model
    summary := llm.Chat(gpt-4o-mini, "Summarize: " + old)
    
    // Replace with single message
    s.ReplaceMessages(chatID, old.IDs, Message{
        Role: "system",
        Content: "Previous conversation summary: " + summary,
    })
}
```

**Trigger:** After every `AddMessage()`, check count → compact if needed.

---

### Phase 3: Web Access (Week 3)

**Goal:** Internet research and browser automation.

**Estimated time:** 10 hours

#### 3.1 Web Search Tool (4h)

**Option A: Brave Search API (recommended)**
- Free tier: 2000 queries/month
- RESTful JSON API
- Better quality than scraping

```go
type WebSearchTool struct {
    apiKey string
}

func (t *WebSearchTool) Execute(ctx, args) (string, error) {
    query := args["query"].(string)
    
    resp := http.Get("https://api.search.brave.com/res/v1/web/search", 
        params{"q": query, "count": 10})
    
    results := parseResults(resp)
    return formatResults(results), nil  // Title + snippet + URL
}
```

**Option B: DuckDuckGo HTML scraping**
- Free, no API key
- Less reliable (HTML changes)

#### 3.2 Browser Automation (6h)

Use **Rod** (Puppeteer for Go):
```bash
go get github.com/go-rod/rod
```

**Tool: browse_web**
```go
type BrowseWebTool struct {}

func (t *BrowseWebTool) Execute(ctx, args) (string, error) {
    url := args["url"].(string)
    actions := args["actions"].([]string)  // ["click #button", "type input text"]
    
    browser := rod.New().MustConnect()
    defer browser.MustClose()
    
    page := browser.MustPage(url)
    
    for _, action := range actions {
        // Parse: "click #selector" → page.MustElement("#selector").MustClick()
        // Parse: "type input text" → page.MustElement("input").MustInput("text")
    }
    
    // Return page text or base64 screenshot
    screenshot := page.MustScreenshot()
    return base64.StdEncoding.EncodeToString(screenshot), nil
}
```

**Safety:**
- Start with **read-only** (no clicking)
- Allowlist domains
- Screenshot-only output (don't auto-extract passwords)

---

### Phase 4: MCP Integration (Week 4)

**Goal:** Connect to external MCP servers (Google Calendar, Tasks, Apple Notes, etc.)

**Estimated time:** 14 hours

#### 4.1 MCP Client (8h)

Go has no official MCP SDK. Build minimal client:

**Structure:**
```
internal/mcp/
  client.go      # MCP stdio client
  transport.go   # JSON-RPC 2.0 over stdio
  types.go       # Protocol types (initialize, tools/list, tools/call)
  server.go      # Server process manager
```

**MCP Protocol (JSON-RPC 2.0 over stdio):**

1. **Initialize:**
```json
→ {"jsonrpc": "2.0", "method": "initialize", "id": 1, "params": {
    "protocolVersion": "2024-11-05",
    "capabilities": {},
    "clientInfo": {"name": "aidaemon", "version": "0.1.0"}
  }}
← {"jsonrpc": "2.0", "id": 1, "result": {
    "protocolVersion": "2024-11-05",
    "capabilities": {"tools": {}},
    "serverInfo": {"name": "google-calendar-mcp"}
  }}
```

2. **List tools:**
```json
→ {"jsonrpc": "2.0", "method": "tools/list", "id": 2}
← {"jsonrpc": "2.0", "id": 2, "result": {
    "tools": [
      {
        "name": "list-events",
        "description": "List calendar events",
        "inputSchema": {
          "type": "object",
          "properties": {
            "calendar": {"type": "string"}
          }
        }
      }
    ]
  }}
```

3. **Call tool:**
```json
→ {"jsonrpc": "2.0", "method": "tools/call", "id": 3, "params": {
    "name": "list-events",
    "arguments": {"calendar": "primary"}
  }}
← {"jsonrpc": "2.0", "id": 3, "result": {
    "content": [{"type": "text", "text": "Events: ..."}]
  }}
```

**Implementation:**
```go
type MCPClient struct {
    cmd    *exec.Cmd
    stdin  io.WriteCloser
    stdout *bufio.Scanner
    stderr io.ReadCloser
    nextID int64
}

func (c *MCPClient) Call(method string, params interface{}) (interface{}, error) {
    req := jsonrpc2.Request{
        JSONRPC: "2.0",
        Method:  method,
        ID:      atomic.AddInt64(&c.nextID, 1),
        Params:  params,
    }
    
    json.NewEncoder(c.stdin).Encode(req)
    
    var resp jsonrpc2.Response
    c.stdout.Scan()
    json.Unmarshal(c.stdout.Bytes(), &resp)
    
    if resp.Error != nil {
        return nil, resp.Error
    }
    
    return resp.Result, nil
}
```

#### 4.2 Server Launcher (4h)

**Config:**
```json
{
  "mcp_servers": {
    "google-calendar": {
      "command": "npx",
      "args": ["-y", "@cocal/google-calendar-mcp"],
      "env": {
        "GOOGLE_OAUTH_CREDENTIALS": "/path/to/credentials.json"
      }
    },
    "google-tasks": {
      "command": "npx",
      "args": ["-y", "@google/mcp-server-tasks"]
    },
    "apple": {
      "command": "npx",
      "args": ["-y", "@apple/mcp-server-apple"]
    }
  }
}
```

**Startup:**
```go
func (d *Daemon) startMCPServers(cfg Config) error {
    for name, serverCfg := range cfg.MCPServers {
        client, err := mcp.NewClient(serverCfg.Command, serverCfg.Args, serverCfg.Env)
        if err != nil {
            return fmt.Errorf("start %s: %w", name, err)
        }
        
        // Initialize
        client.Call("initialize", initParams)
        
        // Store for later use
        d.mcpClients[name] = client
    }
}
```

#### 4.3 Dynamic Tool Registration (2h)

On startup, after launching MCP servers:
```go
func (r *Registry) registerMCPTools(clients map[string]*mcp.Client) {
    for serverName, client := range clients {
        // List tools from this server
        result, _ := client.Call("tools/list", nil)
        tools := result.Tools
        
        for _, toolDef := range tools {
            // Wrap as Tool interface
            r.Register(&MCPTool{
                server:    serverName,
                client:    client,
                name:      toolDef.Name,
                desc:      toolDef.Description,
                params:    toolDef.InputSchema,
            })
        }
    }
}
```

**Execution:**
```go
type MCPTool struct {
    server string
    client *mcp.Client
    name   string
    // ...
}

func (t *MCPTool) Execute(ctx, args) (string, error) {
    result, err := t.client.Call("tools/call", map[string]interface{}{
        "name":      t.name,
        "arguments": args,
    })
    
    // Extract text content from result
    return result.Content[0].Text, err
}
```

---

### Phase 5: Safety & Polish (Week 5)

**Goal:** Production-ready with proper safety and logging.

**Estimated time:** 10 hours

#### 5.1 Permission System (4h)

**Config schema:**
```json
{
  "tool_permissions": {
    "read_file": {
      "mode": "whitelist",
      "allowed_paths": [
        "~/Documents/**",
        "~/Projects/**",
        "~/Desktop/**"
      ],
      "denied_paths": [
        "~/.ssh/**",
        "~/.aws/**",
        "~/.config/aidaemon/config.json"
      ]
    },
    "write_file": {
      "mode": "whitelist",
      "allowed_paths": ["~/Desktop/aidaemon_output/**"]
    },
    "run_command": {
      "mode": "whitelist",
      "allowed_commands": ["ls", "cat", "grep", "find", "echo", "pwd"],
      "denied_commands": ["rm", "sudo", "curl", "wget"]
    },
    "web_fetch": {
      "mode": "allow_all"
    },
    "browse_web": {
      "mode": "whitelist",
      "allowed_domains": ["*.google.com", "*.github.com"]
    }
  }
}
```

**Enforcement:**
```go
func (r *Registry) Execute(ctx, toolName, args) (string, error) {
    tool := r.tools[toolName]
    
    // Check permissions
    if err := r.checkPermissions(toolName, args); err != nil {
        return "", fmt.Errorf("permission denied: %w", err)
    }
    
    return tool.Execute(ctx, args)
}
```

#### 5.2 Audit Logging (2h)

**Log format:**
```
[2026-02-13 14:32:15] TOOL_CALL: read_file {path: "~/Documents/finance.txt"}
[2026-02-13 14:32:15] TOOL_RESULT: success (234 bytes)
[2026-02-13 14:32:18] TOOL_CALL: run_command {cmd: "ls ~/Projects"}
[2026-02-13 14:32:18] TOOL_RESULT: success (15 lines)
[2026-02-13 14:32:20] TOOL_CALL: web_fetch {url: "https://news.ycombinator.com"}
[2026-02-13 14:32:22] TOOL_RESULT: success (12KB)
```

**Implementation:**
```go
func (r *Registry) Execute(ctx, toolName, args) (string, error) {
    log.Printf("[TOOL_CALL] %s %v", toolName, args)
    
    result, err := tool.Execute(ctx, args)
    
    if err != nil {
        log.Printf("[TOOL_RESULT] error: %v", err)
    } else {
        log.Printf("[TOOL_RESULT] success (%d bytes)", len(result))
    }
    
    return result, err
}
```

**Log rotation:** Use `lumberjack` package.

#### 5.3 HTTP API (4h)

Wire up the existing `port: 8420` config:

**Endpoints:**
```
POST /chat
  Body: {"message": "Hello", "chat_id": "optional"}
  Response: {"response": "Hi there", "model": "gpt-4o", "usage": {...}}

GET /sessions
  Response: [{"chat_id": "123", "message_count": 15, "last_message": "..."}]

POST /reset
  Body: {"chat_id": "123"}
  Response: {"success": true}

POST /tool
  Body: {"tool": "read_file", "args": {"path": "~/test.txt"}}
  Response: {"result": "file contents"}
```

**Authentication:** Simple bearer token in config:
```json
{
  "api_token": "secret123"
}
```

**Usage:**
```bash
curl -H "Authorization: Bearer secret123" \
     -d '{"message": "Hello"}' \
     http://localhost:8420/chat
```

---

## Success Criteria

### Phase 1 Complete
- [ ] Tool registry implemented
- [ ] 4 built-in tools working (read/write file, run command, web fetch)
- [ ] LLM can call tools and process results
- [ ] User can say "Read ~/Documents/test.txt" and get file contents
- [ ] Permissions enforced (whitelist paths)

### Phase 2 Complete
- [ ] Markdown formatting in Telegram
- [ ] Rich system prompt loaded from file
- [ ] Image analysis working
- [ ] Context compaction prevents hard truncation

### Phase 3 Complete
- [ ] Web search returns top 10 results
- [ ] Browser automation can navigate and screenshot

### Phase 4 Complete
- [ ] At least 2 MCP servers connected (Google Calendar + Tasks)
- [ ] LLM can query calendar and create tasks
- [ ] Dynamic tool registration from MCP servers

### Phase 5 Complete
- [ ] Permission system enforces security rules
- [ ] All tool calls logged to disk
- [ ] HTTP API accepts requests
- [ ] Production-ready for 24/7 operation

## Post-v1.0 Ideas (Not Prioritized)

- **Voice input:** Telegram voice messages → Whisper API → text
- **Multi-user support:** Config array of allowed user IDs
- **Conversation branching:** Fork conversations at any point
- **Scheduled tasks:** "Remind me at 2 PM", "Run this every morning"
- **File watching:** Monitor files for changes, notify via Telegram
- **Email integration:** Read/send emails via IMAP/SMTP
- **Desktop notifications:** macOS notifications for important alerts
- **Mobile app:** Native iOS/Android app (uses HTTP API)
- **Multi-provider:** Add OpenRouter, Anthropic direct, OpenAI direct
- **Collaborative:** Share conversations with others
- **Plugin system:** Load custom tools from external packages

## Risks & Mitigations

### Risk: Tool abuse (accidental rm -rf)

**Mitigation:**
- Start with read-only commands
- Require explicit opt-in for destructive commands
- Audit log every command
- Dry-run mode for testing

### Risk: MCP servers crash

**Mitigation:**
- Restart on crash (exponential backoff)
- Fallback to built-in tools
- Log crashes for debugging

### Risk: Telegram rate limits

**Mitigation:**
- Already handled (adaptive debounce)
- Batch edits when possible
- Queue messages if needed

### Risk: Context window overflow

**Mitigation:**
- Context compaction (Phase 2)
- Configurable message limit
- Warn user when approaching limit

### Risk: Security vulnerabilities

**Mitigation:**
- Whitelist everything by default
- Never execute arbitrary code without validation
- Regular security audits
- User education in docs

## Timeline

**Aggressive (full-time):**
- Phase 1: 2 days
- Phase 2: 2 days
- Phase 3: 2 days
- Phase 4: 3 days
- Phase 5: 2 days
- **Total: 11 days**

**Realistic (part-time, 2h/day):**
- Phase 1: 1 week
- Phase 2: 1 week
- Phase 3: 1 week
- Phase 4: 1.5 weeks
- Phase 5: 1 week
- **Total: 5.5 weeks**

**Start date:** February 13, 2026  
**Estimated completion:** March 25, 2026 (part-time)

## Next Action

**Immediate (today):**
1. Create `internal/tools/tool.go` interface
2. Create `internal/tools/registry.go` skeleton
3. Implement `read_file.go` as proof of concept

**This weekend:**
1. Complete all 4 built-in tools
2. Wire up tool execution in `provider/copilot/copilot.go`
3. Test: "Read ~/Documents/test.txt" → verify it works end-to-end

**Decision point:** After Phase 1 works, evaluate if Phase 2-5 are worth the effort or pivot to other priorities.
