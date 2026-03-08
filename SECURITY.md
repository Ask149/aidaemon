# Security Policy

## Supported Versions

| Version | Supported |
|---------|-----------|
| 2.4.x   | ✅        |
| < 2.4   | ❌        |

## Reporting a Vulnerability

If you discover a security vulnerability in AIDaemon, **please do not open a public issue**.

Instead, report it privately:

1. **Email:** Open an issue titled "Security" with minimal details and request private contact
2. **GitHub:** Use [GitHub's private vulnerability reporting](https://github.com/Ask149/aidaemon/security/advisories/new) if available

Include:
- Description of the vulnerability
- Steps to reproduce
- Impact assessment
- Suggested fix (if any)

You should receive a response within 48 hours.

## Security Model

### Authentication

- **Telegram:** Single-user enforcement — only the configured `telegram_user_id` can interact. Messages from all other users are silently dropped.
- **HTTP API:** Bearer token authentication on all endpoints except `/health`.
- **Copilot:** GitHub OAuth device flow → short-lived Copilot bearer tokens (24h), auto-refreshed.

### Tool Execution

- **Permission system:** Per-tool rules with `allow_all`, `whitelist`, and `deny` modes.
- **Path restrictions:** Built-in file tools are restricted to `~/Documents`, `~/Projects`, `~/Desktop` by default.
- **Command blocking:** Destructive commands (`rm`, `sudo`, `shutdown`, etc.) are blocked by default.
- **Audit logging:** Every tool execution is logged with tool name, arguments, duration, and outcome.

### Data Handling

- **Local only:** All data (conversations, tokens, media) is stored on your machine.
- **No telemetry:** Zero data collection, zero phone-home behavior.
- **API calls:** Only outbound traffic is to `api.github.com` and `api.githubcopilot.com` (LLM), and optionally `api.search.brave.com` (web search).
- **Token storage:** GitHub OAuth tokens are stored in `~/.config/aidaemon/auth.json` with 0600 permissions.

### Pre-commit Protection

A pre-commit hook (`.githooks/pre-commit`) scans staged files for patterns matching:
- API tokens and secrets
- Telegram bot tokens
- OAuth credentials
- Personal identifiers

Install with: `python3 .githooks/install.py`

## Known Limitations

- **No encryption at rest** — SQLite database and log files are stored in plaintext. Use full-disk encryption (FileVault on macOS) for protection.
- **MCP servers** — Third-party MCP servers run as subprocesses with your user permissions. Only use trusted servers.
- **LLM output** — The LLM decides which tools to call. The permission system is the primary safeguard against unintended actions.
