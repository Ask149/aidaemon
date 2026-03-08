.PHONY: build install run test vet clean lint check cover

# Build the daemon binary
build:
	go build -o aidaemon ./cmd/aidaemon/

# Install to $GOBIN
install:
	go install ./cmd/aidaemon/

# Run the daemon
run:
	go run ./cmd/aidaemon/

# Run with race detector
run-race:
	go run -race ./cmd/aidaemon/

# Authenticate with GitHub Copilot
login:
	go run ./cmd/aidaemon/ --login

# Run all tests
test:
	go test ./...

# Static analysis
vet:
	go vet ./...

# Build + vet (quick CI check)
check: build vet test

# Remove build artifacts
clean:
	rm -f aidaemon
	go clean ./...

# Kill any running daemon and restart
restart:
	@pkill -f aidaemon 2>/dev/null; sleep 1; echo "Stopped"
	go install ./cmd/aidaemon/
	aidaemon &
	@echo "Restarted"

# Test coverage report (opens in browser)
cover:
	go test ./... -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

# Install the pre-commit hook
hooks:
	python3 .githooks/install.py

# ── Watchdog (keeps aidaemon alive) ──────────────────────────────────
PLIST_SRC  = scripts/com.ask149.aidaemon.watchdog.plist
PLIST_DEST = $(HOME)/Library/LaunchAgents/com.ask149.aidaemon.watchdog.plist

# Install and start the watchdog (runs every 30 min)
watchdog-install:
	@mkdir -p $(HOME)/Library/LaunchAgents
	cp $(PLIST_SRC) $(PLIST_DEST)
	launchctl bootout gui/$$(id -u) $(PLIST_DEST) 2>/dev/null || true
	launchctl bootstrap gui/$$(id -u) $(PLIST_DEST)
	@echo "✓ watchdog installed — runs every 30 min + at login"

# Stop and remove the watchdog
watchdog-uninstall:
	launchctl bootout gui/$$(id -u) $(PLIST_DEST) 2>/dev/null || true
	rm -f $(PLIST_DEST)
	@echo "✓ watchdog removed"

# Run the watchdog once manually
watchdog:
	./scripts/watchdog.sh
