// Package httpapi provides a lightweight REST API for AIDaemon.
//
// Endpoints:
//
//	POST /chat          — send a message, get LLM response (with tool use)
//	GET  /sessions      — list active chat sessions
//	POST /reset         — clear a chat session
//	POST /tool          — execute a single tool directly
//	GET  /health        — health check
//
// All endpoints except /health require a Bearer token.
package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Ask149/aidaemon/internal/provider"
	"github.com/Ask149/aidaemon/internal/store"
	"github.com/Ask149/aidaemon/internal/tools"
)

// Config holds the HTTP API configuration.
type Config struct {
	Port      int              `json:"port"`
	Token     string           `json:"token"`
	Store     *store.Store     `json:"-"`
	Registry  *tools.Registry  `json:"-"`
	Provider  provider.Provider `json:"-"`
	Model     string           `json:"model"`
	SysPrompt string           `json:"sys_prompt"`
}

// API is the HTTP server.
type API struct {
	cfg    Config
	server *http.Server
}

// New creates an HTTP API server.
func New(cfg Config) *API {
	api := &API{cfg: cfg}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", api.handleHealth)
	mux.HandleFunc("POST /chat", api.requireAuth(api.handleChat))
	mux.HandleFunc("GET /sessions", api.requireAuth(api.handleSessions))
	mux.HandleFunc("POST /reset", api.requireAuth(api.handleReset))
	mux.HandleFunc("POST /tool", api.requireAuth(api.handleTool))

	api.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return api
}

// Start runs the HTTP server. Blocks until ctx is cancelled or an error occurs.
func (a *API) Start(ctx context.Context) error {
	log.Printf("[httpapi] listening on :%d", a.cfg.Port)

	// Graceful shutdown when context is cancelled.
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.server.Shutdown(shutCtx); err != nil {
			log.Printf("[httpapi] shutdown error: %v", err)
		}
	}()

	if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("http listen: %w", err)
	}
	return nil
}

// ---------- middleware ----------

// requireAuth wraps a handler with Bearer token authentication.
func (a *API) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			jsonError(w, http.StatusUnauthorized, "missing or invalid Authorization header")
			return
		}
		token := strings.TrimPrefix(auth, "Bearer ")
		if token != a.cfg.Token {
			jsonError(w, http.StatusForbidden, "invalid token")
			return
		}
		next(w, r)
	}
}

// ---------- handlers ----------

func (a *API) handleHealth(w http.ResponseWriter, _ *http.Request) {
	jsonResp(w, http.StatusOK, map[string]string{
		"status": "ok",
		"model":  a.cfg.Model,
	})
}

// chatRequest is the JSON body for POST /chat.
type chatRequest struct {
	SessionID string `json:"session_id"`
	Message   string `json:"message"`
}

// chatResponse is the JSON response for POST /chat.
type chatResponse struct {
	Reply     string   `json:"reply"`
	ToolCalls []string `json:"tool_calls,omitempty"`
}

func (a *API) handleChat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.SessionID == "" {
		req.SessionID = fmt.Sprintf("api-%d", time.Now().UnixMilli())
	}
	if req.Message == "" {
		jsonError(w, http.StatusBadRequest, "message is required")
		return
	}

	// Save user message.
	if err := a.cfg.Store.AddMessage(req.SessionID, "user", req.Message); err != nil {
		jsonError(w, http.StatusInternalServerError, "store error: "+err.Error())
		return
	}

	// Build conversation for the LLM.
	history, err := a.cfg.Store.GetHistory(req.SessionID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "history error: "+err.Error())
		return
	}

	messages := make([]provider.Message, 0, len(history)+1)
	if a.cfg.SysPrompt != "" {
		messages = append(messages, provider.Message{Role: "system", Content: a.cfg.SysPrompt})
	}
	for _, m := range history {
		messages = append(messages, provider.Message{Role: m.Role, Content: m.Content})
	}

	// Build tool definitions.
	var toolDefs []provider.ToolDef
	for _, def := range a.cfg.Registry.Definitions() {
		toolDefs = append(toolDefs, provider.ToolDef{
			Type: "function",
			Function: provider.FuncDef{
				Name:        def.Function.Name,
				Description: def.Function.Description,
				Parameters:  def.Function.Parameters,
			},
		})
	}

	// Call LLM (with tool loop, max 25 iterations for API).
	var toolCallNames []string
	for i := 0; i < 25; i++ {
		resp, err := a.cfg.Provider.Chat(r.Context(), provider.ChatRequest{
			Model:    a.cfg.Model,
			Messages: messages,
			Tools:    toolDefs,
		})
		if err != nil {
			jsonError(w, http.StatusBadGateway, "LLM error: "+err.Error())
			return
		}

		// No tool calls — we have the final response.
		if len(resp.ToolCalls) == 0 {
			// Save and return assistant reply.
			if err := a.cfg.Store.AddMessage(req.SessionID, "assistant", resp.Content); err != nil {
				log.Printf("[httpapi] store error: %v", err)
			}
			jsonResp(w, http.StatusOK, chatResponse{
				Reply:     resp.Content,
				ToolCalls: toolCallNames,
			})
			return
		}

		// Execute tool calls.
		messages = append(messages, provider.Message{
			Role:      "assistant",
			ToolCalls: resp.ToolCalls,
		})

		for _, tc := range resp.ToolCalls {
			toolCallNames = append(toolCallNames, tc.Function.Name)
			result, execErr := a.cfg.Registry.Execute(r.Context(), tc.Function.Name, tc.Function.Arguments)
			if execErr != nil {
				result = fmt.Sprintf("error: %v", execErr)
			}
			messages = append(messages, provider.Message{
				Role:       "tool",
				Content:    result,
				ToolCallID: tc.ID,
			})
		}
	}

	jsonError(w, http.StatusInternalServerError, "tool loop exceeded 25 iterations")
}

type sessionInfo struct {
	ID       string `json:"id"`
	Messages int    `json:"messages"`
}

func (a *API) handleSessions(w http.ResponseWriter, _ *http.Request) {
	// List sessions by querying the store for distinct chat IDs.
	// For now, return a simple response. The store doesn't have a ListSessions method,
	// so we return a placeholder.
	jsonResp(w, http.StatusOK, map[string]string{
		"status": "ok",
		"note":   "session listing not yet implemented — use the Telegram interface for full session management",
	})
}

type resetRequest struct {
	SessionID string `json:"session_id"`
}

func (a *API) handleReset(w http.ResponseWriter, r *http.Request) {
	var req resetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.SessionID == "" {
		jsonError(w, http.StatusBadRequest, "session_id is required")
		return
	}

	if err := a.cfg.Store.ClearChat(req.SessionID); err != nil {
		jsonError(w, http.StatusInternalServerError, "clear error: "+err.Error())
		return
	}

	jsonResp(w, http.StatusOK, map[string]string{"status": "cleared", "session_id": req.SessionID})
}

type toolRequest struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args"`
}

type toolResponse struct {
	Result string `json:"result"`
}

func (a *API) handleTool(w http.ResponseWriter, r *http.Request) {
	var req toolRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Name == "" {
		jsonError(w, http.StatusBadRequest, "name is required")
		return
	}

	argsJSON, _ := json.Marshal(req.Args)
	result, err := a.cfg.Registry.Execute(r.Context(), req.Name, string(argsJSON))
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}

	jsonResp(w, http.StatusOK, toolResponse{Result: result})
}

// ---------- helpers ----------

func jsonResp(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func jsonError(w http.ResponseWriter, status int, message string) {
	jsonResp(w, status, map[string]string{"error": message})
}
