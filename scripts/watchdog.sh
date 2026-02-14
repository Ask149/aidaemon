#!/usr/bin/env bash
# aidaemon watchdog — ensures the daemon is always running.
# Intended to be called every 30 min via launchd (macOS cron).
#
# Usage:  ./scripts/watchdog.sh          # normal
#         ./scripts/watchdog.sh --force   # kill & restart even if running

set -euo pipefail

# ── Config ──────────────────────────────────────────────────────────────────
AIDAEMON_DIR="$HOME/Projects/active/aidaemon"
BINARY="$AIDAEMON_DIR/aidaemon"
LOG_DIR="$HOME/.config/aidaemon/data/logs"
WATCHDOG_LOG="$LOG_DIR/watchdog.log"
PID_FILE="$LOG_DIR/aidaemon.pid"
DAEMON_LOG="$LOG_DIR/aidaemon-daemon.log"   # stdout/stderr of background process

# ── Helpers ─────────────────────────────────────────────────────────────────
timestamp() { date "+%Y-%m-%d %H:%M:%S"; }

log() { echo "$(timestamp) [watchdog] $*" >> "$WATCHDOG_LOG"; }

mkdir -p "$LOG_DIR"

# ── Is aidaemon already running? ────────────────────────────────────────────
is_running() {
    # Check PID file first
    if [[ -f "$PID_FILE" ]]; then
        local pid
        pid=$(cat "$PID_FILE")
        if kill -0 "$pid" 2>/dev/null; then
            return 0
        fi
        # Stale PID file — clean up
        rm -f "$PID_FILE"
    fi

    # Fallback: check by process name (skip grep itself and this script)
    if pgrep -f "$BINARY" >/dev/null 2>&1; then
        # Capture the PID for future checks
        pgrep -f "$BINARY" | head -1 > "$PID_FILE"
        return 0
    fi

    return 1
}

# ── Build if binary is missing or source is newer ───────────────────────────
ensure_binary() {
    local needs_build=0

    if [[ ! -x "$BINARY" ]]; then
        needs_build=1
    elif [[ -n $(find "$AIDAEMON_DIR/cmd" "$AIDAEMON_DIR/internal" -name '*.go' -newer "$BINARY" 2>/dev/null) ]]; then
        needs_build=1
    fi

    if (( needs_build )); then
        log "building binary..."
        (cd "$AIDAEMON_DIR" && make build >> "$WATCHDOG_LOG" 2>&1)
        log "build complete"
    fi
}

# ── Start the daemon ───────────────────────────────────────────────────────
start_daemon() {
    ensure_binary

    log "starting aidaemon..."
    cd "$AIDAEMON_DIR"

    # Rotate daemon log if > 50 MB
    if [[ -f "$DAEMON_LOG" ]] && (( $(stat -f%z "$DAEMON_LOG" 2>/dev/null || echo 0) > 52428800 )); then
        mv "$DAEMON_LOG" "$DAEMON_LOG.prev"
        log "rotated daemon log"
    fi

    nohup "$BINARY" >> "$DAEMON_LOG" 2>&1 &
    local pid=$!
    echo "$pid" > "$PID_FILE"
    log "started (pid=$pid)"
}

# ── Force restart ──────────────────────────────────────────────────────────
force_restart() {
    log "force restart requested"
    if is_running; then
        local pid
        pid=$(cat "$PID_FILE" 2>/dev/null || pgrep -f "$BINARY" | head -1)
        if [[ -n "$pid" ]]; then
            kill "$pid" 2>/dev/null || true
            sleep 2
            kill -9 "$pid" 2>/dev/null || true
            log "killed pid=$pid"
        fi
        rm -f "$PID_FILE"
    fi
    start_daemon
}

# ── Main ───────────────────────────────────────────────────────────────────
main() {
    if [[ "${1:-}" == "--force" ]]; then
        force_restart
        exit 0
    fi

    if is_running; then
        local pid
        pid=$(cat "$PID_FILE" 2>/dev/null || echo "?")
        log "OK — already running (pid=$pid)"
    else
        log "NOT running — starting..."
        start_daemon
    fi
}

main "$@"
