package agentstore

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// RegisterHandlers registers all HTTP handlers on the given mux.
// Memory handlers have moved to memory-store.
func RegisterHandlers(mux *http.ServeMux, s *Store) {
	h := &handler{s: s}

	mux.HandleFunc("GET /agents", h.listAgents)
	mux.HandleFunc("GET /agents/{slug}", h.getAgent)
	mux.HandleFunc("POST /agents", h.createAgent)
	mux.HandleFunc("PUT /agents/{slug}", h.updateAgent)
	mux.HandleFunc("DELETE /agents/{slug}", h.deleteAgent)

	mux.HandleFunc("GET /agents/{slug}/orchestrators", h.getOrchestrators)
	mux.HandleFunc("POST /agents/{slug}/orchestrators", h.createOrchestrator)

	mux.HandleFunc("GET /agents/{slug}/config", h.getAgentConfig)
	mux.HandleFunc("GET /configs", h.getAllConfigs)

	mux.HandleFunc("GET /reconcile", h.reconcile)
	mux.HandleFunc("GET /agents/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("GET /files", h.listFiles)
	mux.HandleFunc("GET /files/{id}", h.getFile)
	mux.HandleFunc("GET /files/{id}/content", h.getFileContent)
	mux.HandleFunc("PUT /files/{id}/content", h.putFileContent)
	mux.HandleFunc("POST /files/{id}/enable", h.enableFile)
	mux.HandleFunc("POST /files/{id}/disable", h.disableFile)
	mux.HandleFunc("POST /files/scan", h.scanFiles)
}

type handler struct {
	s *Store
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// === Agents ===

func (h *handler) listAgents(w http.ResponseWriter, r *http.Request) {
	// Fetch statuses for merging
	statuses, _ := h.s.ListStatuses()
	statusMap := make(map[string]AgentStatus)
	for _, st := range statuses {
		if existing, ok := statusMap[st.AgentSlug]; !ok || (existing.Status == "idle" && st.Status != "idle") {
			statusMap[st.AgentSlug] = st
		}
	}

	if r.URL.Query().Get("expanded") == "true" {
		expanded, err := h.s.ListAgentsExpanded()
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		type expandedResponse struct {
			Name            string  `json:"name"`
			DisplayName     string  `json:"display_name"`
			Orchestrator    string  `json:"orchestrator"`
			Emoji           string  `json:"emoji"`
			Project         string  `json:"project"`
			Enabled         bool    `json:"enabled"`
			IsDefault       bool    `json:"is_default"`
			OrchEmoji       string  `json:"orch_emoji"`
			Status          string  `json:"status"`
			StatusTask      *string `json:"status_task,omitempty"`
			StatusSessionID *string `json:"status_session_id,omitempty"`
			StatusSince     *int64  `json:"status_since,omitempty"`
		}
		out := make([]expandedResponse, len(expanded))
		for i, a := range expanded {
			name := a.OrchestratorName
			if name == "" {
				name = a.Slug
			}
			out[i] = expandedResponse{
				Name:         name,
				DisplayName:  a.DisplayName,
				Orchestrator: a.Orchestrator,
				Emoji:        a.Emoji,
				Project:      a.Projects,
				Enabled:      a.Enabled,
				IsDefault:    a.IsDefault,
				OrchEmoji:    a.OrchEmoji,
				Status:       "idle",
			}
			if st, ok := statusMap[a.Slug]; ok {
				out[i].Status = st.Status
				out[i].StatusTask = st.Task
				out[i].StatusSessionID = st.SessionID
				out[i].StatusSince = st.StartedAt
			}
		}
		writeJSON(w, 200, out)
		return
	}

	agents, err := h.s.ListAgents()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	type agentResponse struct {
		Agent
		Status          string  `json:"status"`
		StatusTask      *string `json:"status_task,omitempty"`
		StatusSessionID *string `json:"status_session_id,omitempty"`
		StatusSince     *int64  `json:"status_since,omitempty"`
	}
	out := make([]agentResponse, len(agents))
	for i, a := range agents {
		out[i] = agentResponse{Agent: a, Status: "idle"}
		if st, ok := statusMap[a.Slug]; ok {
			out[i].Status = st.Status
			out[i].StatusTask = st.Task
			out[i].StatusSessionID = st.SessionID
			out[i].StatusSince = st.StartedAt
		}
	}
	writeJSON(w, 200, out)
}

func (h *handler) getAgent(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	a, err := h.s.GetAgentBySlug(slug)
	if err != nil {
		writeErr(w, 404, "agent not found: "+slug)
		return
	}
	writeJSON(w, 200, a)
}

func (h *handler) createAgent(w http.ResponseWriter, r *http.Request) {
	var a Agent
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		writeErr(w, 400, "invalid json")
		return
	}
	if a.Slug == "" {
		writeErr(w, 400, "slug required")
		return
	}
	if err := h.s.UpsertAgent(&a); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, a)
}

func (h *handler) updateAgent(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	existing, err := h.s.GetAgentBySlug(slug)
	if err != nil {
		writeErr(w, 404, "agent not found: "+slug)
		return
	}
	var a Agent
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		writeErr(w, 400, "invalid json")
		return
	}
	a.ID = existing.ID
	if a.Slug == "" {
		a.Slug = slug
	}
	if err := h.s.UpsertAgent(&a); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, a)
}

func (h *handler) deleteAgent(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	a, err := h.s.GetAgentBySlug(slug)
	if err != nil {
		writeErr(w, 404, "agent not found: "+slug)
		return
	}
	if err := h.s.DeleteAgent(a.ID); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

// === Orchestrators ===

func (h *handler) getOrchestrators(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	a, err := h.s.GetAgentBySlug(slug)
	if err != nil {
		writeErr(w, 404, "agent not found: "+slug)
		return
	}
	orchs, err := h.s.GetAgentOrchestrators(a.ID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, orchs)
}

func (h *handler) createOrchestrator(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	a, err := h.s.GetAgentBySlug(slug)
	if err != nil {
		writeErr(w, 404, "agent not found: "+slug)
		return
	}
	var ao AgentOrchestrator
	if err := json.NewDecoder(r.Body).Decode(&ao); err != nil {
		writeErr(w, 400, "invalid json")
		return
	}
	ao.AgentID = a.ID
	if err := h.s.UpsertAgentOrchestrator(&ao); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, ao)
}

// === Config ===

func (h *handler) getAgentConfig(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	orchestratorID := r.URL.Query().Get("orchestrator")
	if orchestratorID == "" {
		orchestratorID = "inber"
	}
	cfg, err := h.s.GetFullAgentConfig(slug, orchestratorID)
	if err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	writeJSON(w, 200, cfg)
}

func (h *handler) getAllConfigs(w http.ResponseWriter, r *http.Request) {
	orchestratorID := r.URL.Query().Get("orchestrator")
	if orchestratorID == "" {
		orchestratorID = "inber"
	}
	configs, defaultSlug, err := h.s.GetAllAgentConfigs(orchestratorID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"default": defaultSlug, "agents": configs})
}

// === Reconciliation ===

func (h *handler) reconcile(w http.ResponseWriter, r *http.Request) {
	agents, err := h.s.ListAgents()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}

	type AgentDiff struct {
		Slug          string   `json:"slug"`
		Orchestrators []string `json:"orchestrators"`
		Issues        []string `json:"issues"`
	}

	var diffs []AgentDiff
	allOrchestrators, _ := h.s.ListOrchestrators()

	for _, a := range agents {
		orchs, err := h.s.GetAgentOrchestrators(a.ID)
		if err != nil {
			continue
		}
		registered := make(map[string]bool)
		var orchNames []string
		for _, o := range orchs {
			registered[o.OrchestratorID] = true
			orchNames = append(orchNames, o.OrchestratorID)
		}
		var issues []string
		for _, o := range allOrchestrators {
			if !registered[o.ID] && a.Enabled {
				issues = append(issues, fmt.Sprintf("not registered in %s", o.ID))
			}
		}
		if len(issues) > 0 {
			diffs = append(diffs, AgentDiff{Slug: a.Slug, Orchestrators: orchNames, Issues: issues})
		}
	}

	modified, _ := h.s.GetModifiedFiles()
	var modifiedPaths []string
	for _, m := range modified {
		modifiedPaths = append(modifiedPaths, fmt.Sprintf("scan %d: %s", m.ID, m.DiffSummary))
	}

	writeJSON(w, 200, map[string]any{"agent_diffs": diffs, "modified_files": modifiedPaths})
}

var _ = strings.Join // keep import

// === Tracked files ===

func parseID(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}

func (h *handler) listFiles(w http.ResponseWriter, r *http.Request) {
	scope := r.URL.Query().Get("scope")
	agentSlug := r.URL.Query().Get("agent_slug")
	files, err := h.s.ListTrackedFiles(scope, agentSlug)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, files)
}

func (h *handler) getFile(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, 400, "bad id")
		return
	}
	f, err := h.s.GetTrackedFile(id)
	if err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	writeJSON(w, 200, f)
}

func (h *handler) getFileContent(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, 400, "bad id")
		return
	}
	f, data, err := h.s.ReadTrackedFile(id)
	if err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"id":      f.ID,
		"path":    f.Path,
		"enabled": f.Enabled,
		"content": string(data),
	})
}

func (h *handler) putFileContent(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, 400, "bad id")
		return
	}
	var body struct {
		Content string `json:"content"`
	}
	raw, _ := io.ReadAll(r.Body)
	if err := json.Unmarshal(raw, &body); err != nil {
		writeErr(w, 400, "invalid json")
		return
	}
	f, err := h.s.WriteTrackedFile(id, []byte(body.Content))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, f)
}

func (h *handler) enableFile(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, 400, "bad id")
		return
	}
	f, err := h.s.SetTrackedFileEnabled(id, true)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, f)
}

func (h *handler) disableFile(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, 400, "bad id")
		return
	}
	f, err := h.s.SetTrackedFileEnabled(id, false)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, f)
}

func (h *handler) scanFiles(w http.ResponseWriter, r *http.Request) {
	res, err := h.s.Scan()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, res)
}

