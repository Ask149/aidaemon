.PHONY: build install run test vet clean lint check

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

# Install the pre-commit hook
hooks:
	python3 .githooks/install.py
