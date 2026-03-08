#!/usr/bin/env bash
# Friday watchdog — ensures the desktop companion is always running.
# Intended to be called every 5 min via launchd.
#
# Usage:  ./scripts/friday-watchdog.sh          # normal
#         ./scripts/friday-watchdog.sh --force   # kill & restart
#
# Resilience features:
#   - Finds Friday by process name (DesktopCompanion)
#   - Crash loop protection with exponential backoff
#   - Log rotation
#   - Waits for aidaemon before starting (Friday needs it)

set -euo pipefail

# ── Config ──────────────────────────────────────────────────────────────────
FRIDAY_APP="/Applications/Friday.app"
PROCESS_NAME="DesktopCompanion"
AIDAEMON_PORT=8420
LOG_DIR="$HOME/.config/aidaemon/data/logs"
WATCHDOG_LOG="$LOG_DIR/friday-watchdog.log"
CRASH_FILE="$LOG_DIR/friday-crashes.txt"
MAX_LOG_BYTES=10485760  # 10 MB

# ── Helpers ─────────────────────────────────────────────────────────────────
timestamp() { date "+%Y-%m-%d %H:%M:%S"; }

log() { echo "$(timestamp) [friday-watchdog] $*" >> "$WATCHDOG_LOG"; }

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

# ── Is Friday running? ────────────────────────────────────────────────────
get_friday_pid() {
    pgrep -x "$PROCESS_NAME" 2>/dev/null | head -1
}

is_friday_running() {
    local pid
    pid=$(get_friday_pid)
    if [[ -n "$pid" ]]; then
        echo "$pid"
        return 0
    fi
    return 1
}

# ── Is aidaemon available? (Friday depends on it) ─────────────────────────
is_aidaemon_ready() {
    local status
    status=$(curl -s -o /dev/null -w "%{http_code}" --max-time 3 "http://localhost:${AIDAEMON_PORT}/health" 2>/dev/null) || true
    [[ "$status" == "200" ]]
}

# ── Crash loop protection ─────────────────────────────────────────────────
record_crash() {
    echo "$(date +%s)" >> "$CRASH_FILE"
    if [[ -f "$CRASH_FILE" ]]; then
        tail -10 "$CRASH_FILE" > "${CRASH_FILE}.tmp" && mv "${CRASH_FILE}.tmp" "$CRASH_FILE"
    fi
}

should_backoff() {
    [[ ! -f "$CRASH_FILE" ]] && return 1

    local now recent_count
    now=$(date +%s)
    recent_count=0

    while IFS= read -r ts; do
        if (( now - ts < 1800 )); then
            (( recent_count++ ))
        fi
    done < "$CRASH_FILE"

    if (( recent_count >= 3 )); then
        log "BACKOFF — $recent_count crashes in last 30 min, skipping restart"
        return 0
    fi
    return 1
}

# ── Kill Friday ────────────────────────────────────────────────────────────
kill_friday() {
    local pid
    pid=$(get_friday_pid)
    if [[ -n "$pid" ]]; then
        log "killing Friday (pid=$pid)"
        kill "$pid" 2>/dev/null || true
        sleep 2
        kill -9 "$pid" 2>/dev/null || true
        sleep 1
    fi
}

# ── Start Friday ──────────────────────────────────────────────────────────
start_friday() {
    # Friday needs aidaemon — don't start if backend is down
    if ! is_aidaemon_ready; then
        log "SKIP — aidaemon not ready on port $AIDAEMON_PORT, cannot start Friday"
        return 1
    fi

    if [[ ! -d "$FRIDAY_APP" ]]; then
        log "ERROR — $FRIDAY_APP not found"
        return 1
    fi

    log "starting Friday..."
    open "$FRIDAY_APP"

    # Wait up to 10s for it to appear
    local i
    for i in 1 2 3 4 5 6 7 8 9 10; do
        sleep 1
        local pid
        if pid=$(is_friday_running); then
            log "started (pid=$pid, took ${i}s)"
            rm -f "$CRASH_FILE"
            return 0
        fi
    done

    log "FAIL — Friday did not start within 10s"
    record_crash
    return 1
}

# ── Force restart ─────────────────────────────────────────────────────────
force_restart() {
    log "force restart requested"
    kill_friday
    start_friday
}

# ── Main ──────────────────────────────────────────────────────────────────
main() {
    if [[ "${1:-}" == "--force" ]]; then
        force_restart
        exit $?
    fi

    local pid
    if pid=$(is_friday_running); then
        log "OK — running (pid=$pid)"
    else
        # Not running
        if should_backoff; then
            exit 0
        fi
        log "NOT running — starting..."
        record_crash
        start_friday
    fi
}

main "$@"
