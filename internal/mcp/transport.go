package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sync"
	"sync/atomic"
)

// Transport handles JSON-RPC 2.0 communication over stdio pipes.
type Transport struct {
	encoder *json.Encoder
	scanner *bufio.Scanner
	nextID  atomic.Int64

	// Protects writes — only one goroutine can write at a time.
	writeMu sync.Mutex
}

// NewTransport creates a transport wrapping stdin (write) and stdout (read) pipes.
func NewTransport(w io.Writer, r io.Reader) *Transport {
	scanner := bufio.NewScanner(r)
	// MCP messages can be large (directory trees, screenshots, etc.). 10MB buffer.
	const maxBuf = 10 * 1024 * 1024
	scanner.Buffer(make([]byte, 64*1024), maxBuf)

	return &Transport{
		encoder: json.NewEncoder(w),
		scanner: scanner,
	}
}

// Send sends a JSON-RPC request and returns the response.
// This blocks until the server responds.
func (t *Transport) Send(method string, params interface{}) (*Response, error) {
	id := t.nextID.Add(1)

	req := Request{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	t.writeMu.Lock()
	if err := t.encoder.Encode(req); err != nil {
		t.writeMu.Unlock()
		return nil, fmt.Errorf("write request: %w", err)
	}
	t.writeMu.Unlock()

	log.Printf("[mcp] → %s (id=%d)", method, id)

	// Read response lines until we find one with matching ID.
	// Notifications (no ID) are logged and skipped.
	for {
		if !t.scanner.Scan() {
			if err := t.scanner.Err(); err != nil {
				return nil, fmt.Errorf("read response: %w", err)
			}
			return nil, fmt.Errorf("server closed connection")
		}

		line := t.scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var resp Response
		if err := json.Unmarshal(line, &resp); err != nil {
			// May be a notification or log line — skip.
			log.Printf("[mcp] ← (unparsed): %s", string(line))
			continue
		}

		// Skip notifications (ID == 0 and no result/error).
		if resp.ID == 0 && resp.Result == nil && resp.Error == nil {
			log.Printf("[mcp] ← notification: %s", string(line))
			continue
		}

		if resp.ID != id {
			// Response for a different request — shouldn't happen in
			// synchronous mode, but log and skip.
			log.Printf("[mcp] ← response for wrong id=%d (expected %d)", resp.ID, id)
			continue
		}

		if resp.Error != nil {
			log.Printf("[mcp] ← error: %v", resp.Error)
			return nil, resp.Error
		}

		log.Printf("[mcp] ← %s response (id=%d, %d bytes)", method, id, len(resp.Result))
		return &resp, nil
	}
}

// Notify sends a JSON-RPC notification (no response expected).
func (t *Transport) Notify(method string, params interface{}) error {
	notif := Notification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}

	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	if err := t.encoder.Encode(notif); err != nil {
		return fmt.Errorf("write notification: %w", err)
	}

	log.Printf("[mcp] → %s (notification)", method)
	return nil
}
