#!/usr/bin/env bash
# aidaemon watchdog — ensures the daemon is always running and healthy.
# Intended to be called every 5 min via launchd.
#
# Usage:  ./scripts/watchdog.sh          # normal
#         ./scripts/watchdog.sh --force   # kill & restart even if running
#
# Resilience features:
#   - Finds aidaemon by port (8420), not binary path
#   - HTTP health check, not just process existence
#   - Crash loop protection with exponential backoff
#   - Port conflict detection before starting
#   - Log rotation

set -euo pipefail

# ── Config ──────────────────────────────────────────────────────────────────
AIDAEMON_DIR="$HOME/Projects/active/aidaemon"
PORT=8420
LOG_DIR="$HOME/.config/aidaemon/data/logs"
WATCHDOG_LOG="$LOG_DIR/watchdog.log"
PID_FILE="$LOG_DIR/aidaemon.pid"
DAEMON_LOG="$LOG_DIR/aidaemon-daemon.log"
CRASH_FILE="$LOG_DIR/watchdog-crashes.txt"   # tracks recent crashes for backoff
MAX_LOG_BYTES=52428800  # 50 MB

# ── Helpers ─────────────────────────────────────────────────────────────────
timestamp() { date "+%Y-%m-%d %H:%M:%S"; }

log() { echo "$(timestamp) [watchdog] $*" >> "$WATCHDOG_LOG"; }

mkdir -p "$LOG_DIR"

# ── Rotate log if too large ─────────────────────────────────────────────────
rotate_log() {
    local file="$1"
    if [[ -f "$file" ]] && (( $(stat -f%z "$file" 2>/dev/null || echo 0) > MAX_LOG_BYTES )); then
        mv "$file" "${file}.prev"
        log "rotated $(basename "$file")"
    fi
}

rotate_log "$WATCHDOG_LOG"
rotate_log "$DAEMON_LOG"

# ── Find aidaemon by port ──────────────────────────────────────────────────
# Returns the PID of whatever is listening on $PORT, regardless of binary path.
get_pid_on_port() {
    lsof -ti :"$PORT" -sTCP:LISTEN 2>/dev/null | head -1
}

# ── Is aidaemon process alive? ─────────────────────────────────────────────
is_process_alive() {
    local pid
    pid=$(get_pid_on_port)
    if [[ -n "$pid" ]]; then
        echo "$pid"
        return 0
    fi
    return 1
}

# ── Is aidaemon healthy? (HTTP health check) ───────────────────────────────
is_healthy() {
    local status
    status=$(curl -s -o /dev/null -w "%{http_code}" --max-time 5 "http://localhost:${PORT}/health" 2>/dev/null) || true
    [[ "$status" == "200" ]]
}

# ── Crash loop protection ─────────────────────────────────────────────────
# Records crash timestamps. If too many recent crashes, backs off.
record_crash() {
    echo "$(date +%s)" >> "$CRASH_FILE"
    # Keep only last 10 entries
    if [[ -f "$CRASH_FILE" ]]; then
        tail -10 "$CRASH_FILE" > "${CRASH_FILE}.tmp" && mv "${CRASH_FILE}.tmp" "$CRASH_FILE"
    fi
}

should_backoff() {
    [[ ! -f "$CRASH_FILE" ]] && return 1

    local now count recent_count
    now=$(date +%s)
    recent_count=0

    while IFS= read -r ts; do
        # Count crashes in the last 30 minutes (1800s)
        if (( now - ts < 1800 )); then
            (( recent_count++ ))
        fi
    done < "$CRASH_FILE"

    # Back off if 3+ crashes in 30 min
    if (( recent_count >= 3 )); then
        log "BACKOFF — $recent_count crashes in last 30 min, skipping restart"
        return 0
    fi
    return 1
}

# ── Build if binary is missing or source is newer ──────────────────────────
ensure_binary() {
    local binary="$AIDAEMON_DIR/aidaemon"
    local needs_build=0

    if [[ ! -x "$binary" ]]; then
        needs_build=1
    elif [[ -n $(find "$AIDAEMON_DIR/cmd" "$AIDAEMON_DIR/internal" -name '*.go' -newer "$binary" 2>/dev/null) ]]; then
        needs_build=1
    fi

    if (( needs_build )); then
        log "building binary..."
        if (cd "$AIDAEMON_DIR" && make build >> "$WATCHDOG_LOG" 2>&1); then
            log "build complete"
        else
            log "BUILD FAILED — cannot start"
            return 1
        fi
    fi

    return 0
}

# ── Kill whatever is on our port ───────────────────────────────────────────
kill_on_port() {
    local pid
    pid=$(get_pid_on_port)
    if [[ -n "$pid" ]]; then
        log "killing pid=$pid on port $PORT"
        kill "$pid" 2>/dev/null || true
        sleep 2
        kill -9 "$pid" 2>/dev/null || true
        rm -f "$PID_FILE"
        sleep 1
    fi
}

# ── Start the daemon ─────────────────────────────────────────────────────
start_daemon() {
    # Check port is free
    if is_process_alive >/dev/null; then
        log "ERROR — port $PORT still occupied after kill attempt"
        return 1
    fi

    ensure_binary || return 1

    local binary="$AIDAEMON_DIR/aidaemon"
    log "starting aidaemon..."
    cd "$AIDAEMON_DIR"

    nohup "$binary" >> "$DAEMON_LOG" 2>&1 &
    local pid=$!
    echo "$pid" > "$PID_FILE"

    # Wait up to 10s for it to become healthy
    local i
    for i in 1 2 3 4 5 6 7 8 9 10; do
        sleep 1
        if is_healthy; then
            log "started and healthy (pid=$pid, took ${i}s)"
            # Clear crash history on successful start
            rm -f "$CRASH_FILE"
            return 0
        fi
    done

    # Started but not healthy
    if kill -0 "$pid" 2>/dev/null; then
        log "WARN — started (pid=$pid) but health check not responding yet"
    else
        log "FAIL — process died immediately after start"
        record_crash
        return 1
    fi
}

# ── Force restart ─────────────────────────────────────────────────────────
force_restart() {
    log "force restart requested"
    kill_on_port
    start_daemon
}

# ── Main ──────────────────────────────────────────────────────────────────
main() {
    if [[ "${1:-}" == "--force" ]]; then
        force_restart
        exit $?
    fi

    local pid
    if pid=$(is_process_alive); then
        # Process exists on port — check health
        if is_healthy; then
            # Update PID file in case it's stale
            echo "$pid" > "$PID_FILE"
            log "OK — healthy (pid=$pid)"
        else
            log "WARN — process alive (pid=$pid) but health check failed"
            # Don't restart yet — could be temporarily busy
            # Only restart if unhealthy for 2+ consecutive checks
            # (launchd runs us every 5 min, so 10 min tolerance)
            if [[ -f "$LOG_DIR/unhealthy.marker" ]]; then
                log "unhealthy for 2+ checks — restarting"
                rm -f "$LOG_DIR/unhealthy.marker"
                kill_on_port
                if ! should_backoff; then
                    start_daemon
                fi
            else
                touch "$LOG_DIR/unhealthy.marker"
                log "marking unhealthy — will restart on next check if still failing"
            fi
        fi
    else
        # Not running at all
        rm -f "$LOG_DIR/unhealthy.marker"
        if should_backoff; then
            exit 0
        fi
        log "NOT running — starting..."
        record_crash
        start_daemon
    fi
}

main "$@"
