package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sync"
	"sync/atomic"
)

// readResult carries a parsed response or an error from the background reader.
type readResult struct {
	resp *Response
	err  error
}

// Transport handles JSON-RPC 2.0 communication over stdio pipes.
// A background goroutine reads responses continuously; Send() waits
// for a matching response with context-based timeout.
type Transport struct {
	encoder   *json.Encoder
	writeMu   sync.Mutex
	nextID    atomic.Int64
	responses chan readResult
}

// NewTransport creates a transport wrapping stdin (write) and stdout (read) pipes.
// Starts a background reader goroutine immediately.
func NewTransport(w io.Writer, r io.Reader) *Transport {
	scanner := bufio.NewScanner(r)
	// MCP messages can be large (directory trees, screenshots, etc.). 10MB buffer.
	const maxBuf = 10 * 1024 * 1024
	scanner.Buffer(make([]byte, 64*1024), maxBuf)

	t := &Transport{
		encoder:   json.NewEncoder(w),
		responses: make(chan readResult, 16),
	}

	go t.readLoop(scanner)

	return t
}

// readLoop continuously reads from the scanner and sends parsed responses
// to the responses channel. Runs until the pipe closes (MCP server exits).
func (t *Transport) readLoop(scanner *bufio.Scanner) {
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var resp Response
		if err := json.Unmarshal(line, &resp); err != nil {
			log.Printf("[mcp] ← (unparsed): %s", string(line))
			continue
		}

		// Skip notifications (ID == 0 and no result/error).
		if resp.ID == 0 && resp.Result == nil && resp.Error == nil {
			log.Printf("[mcp] ← notification: %s", string(line))
			continue
		}

		// Copy result bytes — scanner reuses buffer.
		copied := Response{
			JSONRPC: resp.JSONRPC,
			ID:      resp.ID,
			Error:   resp.Error,
		}
		if resp.Result != nil {
			buf := make(json.RawMessage, len(resp.Result))
			copy(buf, resp.Result)
			copied.Result = buf
		}
		t.responses <- readResult{resp: &copied}
	}

	// Scanner done — pipe closed or error.
	err := scanner.Err()
	if err == nil {
		err = fmt.Errorf("server closed connection")
	}
	t.responses <- readResult{err: err}
}

// Send sends a JSON-RPC request and waits for the matching response.
// Respects context cancellation/timeout — returns error if context expires.
func (t *Transport) Send(ctx context.Context, method string, params interface{}) (*Response, error) {
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

	// Wait for matching response or context cancellation.
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("mcp %s timed out (id=%d): %w", method, id, ctx.Err())
		case rr := <-t.responses:
			if rr.err != nil {
				return nil, rr.err
			}
			if rr.resp.ID != id {
				log.Printf("[mcp] ← response for wrong id=%d (expected %d), discarding", rr.resp.ID, id)
				continue
			}
			if rr.resp.Error != nil {
				log.Printf("[mcp] ← error: %v", rr.resp.Error)
				return nil, rr.resp.Error
			}
			log.Printf("[mcp] ← %s response (id=%d, %d bytes)", method, id, len(rr.resp.Result))
			return rr.resp, nil
		}
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
