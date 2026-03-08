package mcp

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"sync"
)

// Server manages a single MCP server subprocess lifecycle.
type Server struct {
	name   string
	config ServerConfig
	cmd    *exec.Cmd
	client *Client
	mu     sync.Mutex
}

// NewServer creates a server manager (does not start the process).
func NewServer(name string, cfg ServerConfig) *Server {
	return &Server{
		name:   name,
		config: cfg,
	}
}

// Start spawns the MCP server subprocess, initializes the protocol,
// and makes the client available.
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cmd != nil {
		return fmt.Errorf("server %s already running", s.name)
	}

	log.Printf("[mcp:%s] starting: %s %v", s.name, s.config.Command, s.config.Args)

	cmd := exec.CommandContext(ctx, s.config.Command, s.config.Args...)

	// Merge environment variables.
	cmd.Env = os.Environ()
	for k, v := range s.config.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// Pipe stdin/stdout for JSON-RPC.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	// Pipe stderr to our log.
	cmd.Stderr = &logWriter{prefix: fmt.Sprintf("[mcp:%s:stderr]", s.name)}

	// Start the process.
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", s.name, err)
	}

	s.cmd = cmd
	log.Printf("[mcp:%s] process started (pid=%d)", s.name, cmd.Process.Pid)

	// Create transport and client.
	transport := NewTransport(stdin, stdout)
	client := NewClient(transport, s.name)

	// Initialize MCP protocol.
	if err := client.Initialize(ctx); err != nil {
		// Kill the process if initialization fails.
		cmd.Process.Kill()
		cmd.Wait()
		s.cmd = nil
		return fmt.Errorf("initialize %s: %w", s.name, err)
	}

	s.client = client

	// Monitor process exit in background.
	go func() {
		err := cmd.Wait()
		s.mu.Lock()
		s.cmd = nil
		s.client = nil
		s.mu.Unlock()
		if err != nil {
			log.Printf("[mcp:%s] process exited: %v", s.name, err)
		} else {
			log.Printf("[mcp:%s] process exited cleanly", s.name)
		}
	}()

	return nil
}

// Stop kills the server subprocess.
func (s *Server) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cmd != nil && s.cmd.Process != nil {
		log.Printf("[mcp:%s] stopping (pid=%d)", s.name, s.cmd.Process.Pid)
		s.cmd.Process.Signal(os.Interrupt)
		// Don't wait here — the goroutine from Start will handle it.
	}
}

// Client returns the MCP client, or nil if the server isn't running.
func (s *Server) Client() *Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.client
}

// Name returns the config name of this server.
func (s *Server) Name() string {
	return s.name
}

// Healthy returns true if the subprocess is still running.
func (s *Server) Healthy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cmd != nil && s.cmd.ProcessState == nil
}

// --- Helpers ---

// logWriter writes lines to log.Printf with a prefix.
type logWriter struct {
	prefix string
}

func (w *logWriter) Write(p []byte) (int, error) {
	log.Printf("%s %s", w.prefix, string(p))
	return len(p), nil
}

// Manager manages multiple MCP servers.
type Manager struct {
	servers map[string]*Server
	mu      sync.RWMutex
}

// NewManager creates an empty MCP server manager.
func NewManager() *Manager {
	return &Manager{
		servers: make(map[string]*Server),
	}
}

// StartAll launches all configured MCP servers.
// Failures are logged but don't prevent other servers from starting.
func (m *Manager) StartAll(ctx context.Context, configs map[string]ServerConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, cfg := range configs {
		if !cfg.IsEnabled() {
			log.Printf("[mcp] skipping disabled server: %s", name)
			continue
		}

		server := NewServer(name, cfg)
		if err := server.Start(ctx); err != nil {
			log.Printf("[mcp] ⚠️  failed to start %s: %v (continuing without it)", name, err)
			continue
		}

		m.servers[name] = server
	}
}

// StopAll stops all running servers.
func (m *Manager) StopAll() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, s := range m.servers {
		s.Stop()
	}
}

// Servers returns all successfully started servers.
func (m *Manager) Servers() []*Server {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*Server, 0, len(m.servers))
	for _, s := range m.servers {
		result = append(result, s)
	}
	return result
}

// Get returns a server by name, or nil.
func (m *Manager) Get(name string) *Server {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.servers[name]
}
