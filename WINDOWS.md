# Windows Setup Guide

Quick start guide for running AIDaemon on Windows.

## Prerequisites

1. **Go 1.25+** - Download from [go.dev](https://go.dev/dl/)
2. **Git** - Download from [git-scm.com](https://git-scm.com/download/win)
3. **GitHub Copilot** subscription ($10/month)
4. **Telegram** account
5. **Node.js** (optional, for MCP servers) - Download from [nodejs.org](https://nodejs.org/)

## Installation

Open PowerShell and run:

```powershell
# Clone the repository
git clone https://github.com/Ask149/aidaemon.git
cd aidaemon

# Build the binary
go build -o aidaemon.exe ./cmd/aidaemon/

# Verify build
.\aidaemon.exe --help
```

## Authentication

```powershell
.\aidaemon.exe --login
```

Follow the GitHub device code flow:
1. Open the URL in your browser
2. Enter the code shown in PowerShell
3. Authorize the app

Your token will be saved to `%USERPROFILE%\.config\aidaemon\auth.json`

## Configuration

Create the config directory and file:

```powershell
# Create config directory
New-Item -ItemType Directory -Force -Path "$env:USERPROFILE\.config\aidaemon"

# Create config file
notepad "$env:USERPROFILE\.config\aidaemon\config.json"
```

Add the following content:

```json
{
  "telegram_token": "YOUR_BOT_TOKEN",
  "telegram_user_id": 123456789,
  "chat_model": "claude-sonnet-4.5",
  "max_conversation_messages": 20,
  "token_limit": 128000,
  "system_prompt": "You are a helpful personal assistant."
}
```

### Getting Telegram Credentials

1. **Bot token:** Message [@BotFather](https://t.me/botfather) → `/newbot` → copy the token
2. **User ID:** Message [@userinfobot](https://t.me/userinfobot) → copy your numeric ID

## Running the Daemon

### Manual Start

```powershell
.\aidaemon.exe
```

Keep this PowerShell window open. The daemon will run until you press Ctrl+C.

### Background Start (with log file)

```powershell
Start-Process -NoNewWindow -FilePath ".\aidaemon.exe" -RedirectStandardOutput "$env:USERPROFILE\.config\aidaemon\data\logs\aidaemon.log" -RedirectStandardError "$env:USERPROFILE\.config\aidaemon\data\logs\aidaemon.log"
```

## Running as a Windows Service

See the full instructions in [README.md](README.md#windows-task-scheduler) for setting up AIDaemon to auto-start with Windows using Task Scheduler.

Quick setup (PowerShell as Administrator):

```powershell
$action = New-ScheduledTaskAction -Execute "C:\path\to\aidaemon\aidaemon.exe" -WorkingDirectory "C:\path\to\aidaemon"
$trigger = New-ScheduledTaskTrigger -AtLogon
$principal = New-ScheduledTaskPrincipal -UserId "$env:USERNAME" -LogonType Interactive
$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable

Register-ScheduledTask -TaskName "AIDaemon" -Action $action -Trigger $trigger -Principal $principal -Settings $settings
```

## Accessing Logs

```powershell
# View recent logs
Get-Content "$env:USERPROFILE\.config\aidaemon\data\logs\aidaemon.log" -Tail 50

# View audit logs (tool execution)
Get-Content "$env:USERPROFILE\.config\aidaemon\data\logs\audit.log" -Tail 50
```

## Troubleshooting

### Build Errors

If you see "go: command not found", restart PowerShell after installing Go.

### Path Issues

Windows uses backslashes (`\`) in paths, but AIDaemon internally uses forward slashes. The code handles this automatically using `filepath.Join`.

Config paths:
- **Config:** `%USERPROFILE%\.config\aidaemon\config.json`
- **Database:** `%USERPROFILE%\.config\aidaemon\aidaemon.db`
- **Workspace:** `%USERPROFILE%\.config\aidaemon\workspace\`
- **Logs:** `%USERPROFILE%\.config\aidaemon\data\logs\`

### MCP Servers (Node.js)

If you want to use MCP servers (browser automation, filesystem, etc.), install Node.js and ensure `npx` is in your PATH:

```powershell
# Verify Node.js
node --version
npx --version
```

Then add MCP servers to your config:

```json
{
  "mcp_servers": {
    "playwright": {
      "command": "npx",
      "args": ["-y", "@playwright/mcp@latest", "--browser", "chrome"]
    },
    "memory": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-memory"]
    }
  }
}
```

### Firewall

Windows Firewall may prompt you to allow network access. Click "Allow access" for private networks.

## Stopping the Daemon

### Manual Stop

If running in foreground: Press `Ctrl+C`

If running in background:

```powershell
# Find the process
Get-Process | Where-Object {$_.ProcessName -like "*aidaemon*"}

# Stop it
Stop-Process -Name "aidaemon"
```

### Task Scheduler Stop

```powershell
Stop-ScheduledTask -TaskName "AIDaemon"
```

## Testing

Run the test suite:

```powershell
go test ./...
```

Expected output: All tests pass (~163 tests across 14 packages).

## Web Interface

The web UI is accessible at: `http://localhost:8420`

Default port is 8420 (configurable via `port` in config.json).

## Uninstallation

```powershell
# Stop the daemon
Stop-Process -Name "aidaemon" -ErrorAction SilentlyContinue

# Remove scheduled task (if configured)
Unregister-ScheduledTask -TaskName "AIDaemon" -Confirm:$false

# Remove config directory
Remove-Item -Recurse -Force "$env:USERPROFILE\.config\aidaemon"

# Remove binary
cd path\to\aidaemon
Remove-Item aidaemon.exe
```

## Support

For issues or questions:
- GitHub Issues: https://github.com/Ask149/aidaemon/issues
- Documentation: [README.md](README.md)
- Architecture: [ARCHITECTURE.md](ARCHITECTURE.md)
