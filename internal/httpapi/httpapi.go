// Package httpapi provides a lightweight REST API for AIDaemon.
//
// Endpoints:
//
//	POST /chat               — send a message, get LLM response (with tool use)
//	GET  /sessions           — list active chat sessions
//	GET  /sessions/{id}      — get session details by ID
//	GET  /sessions/{id}/messages — get all messages for a session
//	POST /sessions/{id}/title    — rename a session
//	POST /reset              — clear a chat session
//	POST /tool               — execute a single tool directly
//	GET  /health             — health check
//	GET  /ws                 — WebSocket upgrade for chat
//	GET  /                   — embedded chat SPA (static files)
//	GET  /cron/jobs          — list all cron jobs
//	POST /cron/jobs          — create a new cron job
//	PATCH /cron/jobs/{id}    — update a cron job (enable/disable, rename)
//	DELETE /cron/jobs/{id}   — delete a cron job
//	POST /hooks/wake        — trigger a webhook (async or sync)
//	GET  /hooks/runs        — list recent webhook runs
//	GET  /hooks/runs/{id}   — get a specific webhook run
//
// All endpoints except /health, /ws, and static files require a Bearer token.
package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Ask149/aidaemon/internal/cron"
	"github.com/Ask149/aidaemon/internal/engine"
	"github.com/Ask149/aidaemon/internal/provider"
	"github.com/Ask149/aidaemon/internal/store"
	"github.com/Ask149/aidaemon/internal/tools"
	"github.com/Ask149/aidaemon/internal/workspace"
	"github.com/Ask149/aidaemon/web"
)

// Config holds the HTTP API configuration.
type Config struct {
	Port           int               `json:"port"`
	Token          string            `json:"token"`
	Store          *store.Store      `json:"-"`
	Registry       *tools.Registry   `json:"-"`
	Provider       provider.Provider `json:"-"`
	Model          string            `json:"model"`
	SysPrompt      string            `json:"sys_prompt"`
	WorkspaceDir   string            `json:"workspace_dir"`
	SkillsDir      string            `json:"skills_dir"`
	WSHandler      http.Handler      `json:"-"` // Optional WebSocket upgrade handler.
	SessionManager interface {
		GetSession(id string) (*store.Session, error)
		ListSessions(channel string) ([]store.Session, error)
		RenameSession(id, title string) error
	} `json:"-"` // Optional session lifecycle manager.

	// WebhookSender delivers async webhook output to a channel.
	WebhookSender interface {
		SendCronOutput(ctx context.Context, channelType, channelMeta, text string) error
	} `json:"-"`

	// WebhookChannelType is the default channel for async webhook delivery ("telegram").
	WebhookChannelType string `json:"-"`

	// WebhookChannelMeta is the default channel metadata (e.g., `{"chat_id":12345}`).
	WebhookChannelMeta string `json:"-"`
}

// API is the HTTP server.
type API struct {
	cfg    Config
	engine *engine.Engine
	server *http.Server
}

// New creates an HTTP API server.
func New(cfg Config) *API {
	api := &API{
		cfg: cfg,
		engine: &engine.Engine{
			Provider: cfg.Provider,
			Registry: cfg.Registry,
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", api.handleHealth)
	mux.HandleFunc("POST /chat", api.requireAuth(api.handleChat))
	mux.HandleFunc("GET /sessions", api.requireAuth(api.handleSessions))
	mux.HandleFunc("GET /sessions/{id}", api.requireAuth(api.handleGetSession))
	mux.HandleFunc("GET /sessions/{id}/messages", api.requireAuth(api.handleGetSessionMessages))
	mux.HandleFunc("POST /sessions/{id}/title", api.requireAuth(api.handleRenameSession))
	mux.HandleFunc("POST /reset", api.requireAuth(api.handleReset))
	mux.HandleFunc("POST /tool", api.requireAuth(api.handleTool))
	mux.HandleFunc("GET /cron/jobs", api.requireAuth(api.handleListCronJobs))
	mux.HandleFunc("POST /cron/jobs", api.requireAuth(api.handleCreateCronJob))
	mux.HandleFunc("PATCH /cron/jobs/{id}", api.requireAuth(api.handleUpdateCronJob))
	mux.HandleFunc("DELETE /cron/jobs/{id}", api.requireAuth(api.handleDeleteCronJob))
	mux.HandleFunc("POST /hooks/wake", api.requireAuth(api.handleWebhookWake))
	mux.HandleFunc("GET /hooks/runs", api.requireAuth(api.handleListWebhookRuns))
	mux.HandleFunc("GET /hooks/runs/{id}", api.requireAuth(api.handleGetWebhookRun))

	// Mount WebSocket handler (unauthenticated — uses per-connection session IDs).
	if cfg.WSHandler != nil {
		mux.Handle("/ws", cfg.WSHandler)
	}

	// Embedded chat SPA — serves index.html, style.css, app.js.
	mux.Handle("/", http.FileServer(http.FS(web.FS)))

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

// Handler returns the HTTP handler (for testing).
func (a *API) Handler() http.Handler {
	return a.server.Handler
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
	// Re-read workspace for fresh system prompt.
	sysPrompt := a.cfg.SysPrompt
	if a.cfg.WorkspaceDir != "" {
		ws := workspace.Load(a.cfg.WorkspaceDir, a.cfg.SkillsDir)
		sysPrompt = ws.SystemPrompt()
	}
	if sysPrompt != "" {
		messages = append(messages, provider.Message{Role: "system", Content: sysPrompt})
	}
	for _, m := range history {
		messages = append(messages, provider.Message{Role: m.Role, Content: m.Content})
	}

	// Delegate to the chat engine (max 25 iterations for API).
	result, err := a.engine.Run(r.Context(), messages, engine.RunOptions{
		Model:         a.cfg.Model,
		MaxIterations: 25,
	})
	if err != nil {
		statusCode := http.StatusBadGateway
		if result != nil && result.Content != "" {
			// Partial result (e.g., max iterations reached with summary).
			if saveErr := a.cfg.Store.AddMessage(req.SessionID, "assistant", result.Content); saveErr != nil {
				log.Printf("[httpapi] store error: %v", saveErr)
			}
			jsonResp(w, http.StatusOK, chatResponse{
				Reply:     result.Content,
				ToolCalls: result.ToolNames,
			})
			return
		}
		jsonError(w, statusCode, "LLM error: "+err.Error())
		return
	}

	// Save and return assistant reply.
	if err := a.cfg.Store.AddMessage(req.SessionID, "assistant", result.Content); err != nil {
		log.Printf("[httpapi] store error: %v", err)
	}
	jsonResp(w, http.StatusOK, chatResponse{
		Reply:     result.Content,
		ToolCalls: result.ToolNames,
	})
}

func (a *API) handleSessions(w http.ResponseWriter, _ *http.Request) {
	// If SessionManager is available, use it for richer session data.
	if a.cfg.SessionManager != nil {
		sessions, err := a.cfg.SessionManager.ListSessions("")
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "list sessions: "+err.Error())
			return
		}
		jsonResp(w, http.StatusOK, sessions)
		return
	}

	// Fallback to legacy SessionInfo format.
	sessions, err := a.cfg.Store.ListSessions()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "list sessions: "+err.Error())
		return
	}
	jsonResp(w, http.StatusOK, sessions)
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

func (a *API) handleGetSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		jsonError(w, http.StatusBadRequest, "session id is required")
		return
	}

	// Use SessionManager if available, otherwise fall back to Store.
	if a.cfg.SessionManager != nil {
		sess, err := a.cfg.SessionManager.GetSession(id)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if sess == nil {
			jsonError(w, http.StatusNotFound, "not found")
			return
		}
		jsonResp(w, http.StatusOK, sess)
		return
	}

	// Fallback: use Store directly.
	sess, err := a.cfg.Store.GetSession(id)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if sess == nil {
		jsonError(w, http.StatusNotFound, "not found")
		return
	}
	jsonResp(w, http.StatusOK, sess)
}

func (a *API) handleGetSessionMessages(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		jsonError(w, http.StatusBadRequest, "session id is required")
		return
	}

	msgs, err := a.cfg.Store.GetHistory(id)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResp(w, http.StatusOK, msgs)
}

func (a *API) handleRenameSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		jsonError(w, http.StatusBadRequest, "session id is required")
		return
	}

	var body struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid body")
		return
	}

	// Use SessionManager if available.
	if a.cfg.SessionManager != nil {
		if err := a.cfg.SessionManager.RenameSession(id, body.Title); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		jsonResp(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	// Fallback: read session, update title, save.
	sess, err := a.cfg.Store.GetSession(id)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if sess == nil {
		jsonError(w, http.StatusNotFound, "not found")
		return
	}
	sess.Title = body.Title
	if err := a.cfg.Store.UpdateSession(*sess); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResp(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- Cron job handlers ---

func (a *API) handleListCronJobs(w http.ResponseWriter, _ *http.Request) {
	jobs, err := a.cfg.Store.ListCronJobs()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResp(w, http.StatusOK, jobs)
}

type createCronJobRequest struct {
	Label    string `json:"label"`
	CronExpr string `json:"cron_expr"`
	Mode     string `json:"mode"`
	Payload  string `json:"payload"`
}

func (a *API) handleCreateCronJob(w http.ResponseWriter, r *http.Request) {
	var req createCronJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Label == "" || req.CronExpr == "" || req.Payload == "" {
		jsonError(w, http.StatusBadRequest, "label, cron_expr, and payload are required")
		return
	}
	if req.Mode == "" {
		req.Mode = "message"
	}

	// Validate cron expression.
	sched, err := cron.Parse(req.CronExpr)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid cron expression: "+err.Error())
		return
	}

	next := sched.Next(time.Now())
	b := make([]byte, 6)
	rand.Read(b)
	id := "cj_" + hex.EncodeToString(b)

	job := store.CronJob{
		ID:          id,
		Label:       req.Label,
		CronExpr:    req.CronExpr,
		Mode:        req.Mode,
		Payload:     req.Payload,
		ChannelType: "http",
		ChannelMeta: "{}",
		Enabled:     true,
		NextRunAt:   &next,
		CreatedAt:   time.Now(),
	}

	if err := a.cfg.Store.CreateCronJob(job); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	jsonResp(w, http.StatusCreated, job)
}

type updateCronJobRequest struct {
	Enabled *bool  `json:"enabled,omitempty"`
	Label   string `json:"label,omitempty"`
}

func (a *API) handleUpdateCronJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		jsonError(w, http.StatusBadRequest, "job id is required")
		return
	}

	var req updateCronJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	job, err := a.cfg.Store.GetCronJob(id)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if job == nil {
		jsonError(w, http.StatusNotFound, "job not found")
		return
	}

	if req.Enabled != nil {
		job.Enabled = *req.Enabled
	}
	if req.Label != "" {
		job.Label = req.Label
	}

	if err := a.cfg.Store.UpdateCronJob(*job); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	jsonResp(w, http.StatusOK, job)
}

func (a *API) handleDeleteCronJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		jsonError(w, http.StatusBadRequest, "job id is required")
		return
	}

	if err := a.cfg.Store.DeleteCronJob(id); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	jsonResp(w, http.StatusOK, map[string]string{"status": "deleted", "id": id})
}

// --- Webhook handlers ---

type webhookWakeRequest struct {
	Prompt  string          `json:"prompt"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Source  string          `json:"source,omitempty"`
}

type webhookWakeResponse struct {
	RunID  string `json:"run_id"`
	Status string `json:"status"`
	Output string `json:"output,omitempty"`
}

func (a *API) handleWebhookWake(w http.ResponseWriter, r *http.Request) {
	var req webhookWakeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Prompt == "" {
		jsonError(w, http.StatusBadRequest, "prompt is required")
		return
	}

	// Generate run ID.
	b := make([]byte, 8)
	rand.Read(b)
	runID := "wh_" + hex.EncodeToString(b)

	// Build the full prompt: user prompt + optional payload context.
	fullPrompt := req.Prompt
	if len(req.Payload) > 0 && string(req.Payload) != "null" {
		formatted, _ := json.MarshalIndent(json.RawMessage(req.Payload), "", "  ")
		fullPrompt = req.Prompt + "\n\nEvent payload:\n```json\n" + string(formatted) + "\n```"
	}

	// Determine channel config.
	channelType := a.cfg.WebhookChannelType
	channelMeta := a.cfg.WebhookChannelMeta
	if channelType == "" {
		channelType = "telegram"
	}
	if channelMeta == "" {
		channelMeta = "{}"
	}

	// Persist the run.
	run := store.WebhookRun{
		ID:          runID,
		Prompt:      req.Prompt,
		Payload:     string(req.Payload),
		Source:      req.Source,
		ChannelType: channelType,
		ChannelMeta: channelMeta,
		Status:      "running",
		StartedAt:   time.Now(),
	}
	if err := a.cfg.Store.CreateWebhookRun(run); err != nil {
		jsonError(w, http.StatusInternalServerError, "store error: "+err.Error())
		return
	}

	// Sync mode: block and return the result.
	sync := r.URL.Query().Get("sync") == "true"
	if sync {
		output, status := a.executeWebhook(r.Context(), runID, fullPrompt, channelType, channelMeta, false)
		jsonResp(w, http.StatusOK, webhookWakeResponse{
			RunID:  runID,
			Status: status,
			Output: output,
		})
		return
	}

	// Async mode: return 202 immediately, execute in background.
	jsonResp(w, http.StatusAccepted, webhookWakeResponse{
		RunID:  runID,
		Status: "running",
	})

	go a.executeWebhook(context.Background(), runID, fullPrompt, channelType, channelMeta, true)
}

// executeWebhook runs the LLM engine and updates the webhook run record.
// If sendToChannel is true, delivers output to the configured channel (Telegram).
// Returns the output and final status.
func (a *API) executeWebhook(ctx context.Context, runID, fullPrompt, channelType, channelMeta string, sendToChannel bool) (string, string) {
	// Build system prompt.
	sysPrompt := a.cfg.SysPrompt
	if a.cfg.WorkspaceDir != "" {
		ws := workspace.Load(a.cfg.WorkspaceDir, a.cfg.SkillsDir)
		sysPrompt = ws.SystemPrompt()
	}

	messages := []provider.Message{
		{Role: "system", Content: sysPrompt},
		{Role: "user", Content: fullPrompt},
	}

	result, err := a.engine.Run(ctx, messages, engine.RunOptions{
		Model:         a.cfg.Model,
		MaxIterations: 25,
	})

	var output, status string
	if err != nil {
		status = "failed"
		if result != nil && result.Content != "" {
			output = result.Content
		} else {
			output = err.Error()
		}
		log.Printf("[webhook] run %s failed: %v", runID, err)
	} else {
		status = "completed"
		output = result.Content
		log.Printf("[webhook] run %s completed (%s)", runID, result.Duration.Round(time.Millisecond))
	}

	// Update the run record.
	finished := time.Now()
	if updateErr := a.cfg.Store.UpdateWebhookRun(runID, status, output, finished); updateErr != nil {
		log.Printf("[webhook] update run %s: %v", runID, updateErr)
	}

	// Deliver to channel (async only).
	if sendToChannel && a.cfg.WebhookSender != nil && output != "" {
		prefix := "🔔 Webhook"
		// Include source in prefix if provided.
		run, _ := a.cfg.Store.GetWebhookRun(runID)
		if run != nil && run.Source != "" {
			prefix = "🔔 Webhook [" + run.Source + "]"
		}
		text := prefix + ":\n\n" + output
		if sendErr := a.cfg.WebhookSender.SendCronOutput(ctx, channelType, channelMeta, text); sendErr != nil {
			log.Printf("[webhook] send output for run %s: %v", runID, sendErr)
		}
	}

	return output, status
}

func (a *API) handleListWebhookRuns(w http.ResponseWriter, r *http.Request) {
	limit := 20
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	runs, err := a.cfg.Store.ListWebhookRuns(limit, offset)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResp(w, http.StatusOK, runs)
}

func (a *API) handleGetWebhookRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		jsonError(w, http.StatusBadRequest, "run id is required")
		return
	}

	run, err := a.cfg.Store.GetWebhookRun(id)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if run == nil {
		jsonError(w, http.StatusNotFound, "run not found")
		return
	}

	jsonResp(w, http.StatusOK, run)
}
