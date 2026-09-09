package agentstore

import (
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
)

func (h *handler) listPromptCollections(w http.ResponseWriter, r *http.Request) {
	views, err := h.s.ListPromptCollectionViews()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, views)
}

func (h *handler) createPromptCollection(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Scope       string `json:"scope"`
		RootPath    string `json:"root_path"`
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	if err := decodeStrict(r, &body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if strings.TrimSpace(body.RootPath) == "" {
		writeErr(w, 400, "root_path is required")
		return
	}
	view, err := h.s.CreatePromptCollection(body.Scope, filepath.Clean(body.RootPath), strings.TrimSpace(body.Title), strings.TrimSpace(body.Description))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, view)
}

func (h *handler) createPromptSection(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, 400, "bad id")
		return
	}
	var body PromptSection
	if err := decodeStrict(r, &body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	result, err := h.s.CreatePromptSection(id, &body)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	h.broadcastMaterializedOutputs(result)
	writeJSON(w, 201, result)
}

func (h *handler) updatePromptSection(w http.ResponseWriter, r *http.Request) {
	id, err := parseIDPath(r, "id")
	if err != nil {
		writeErr(w, 400, "bad id")
		return
	}
	var body PromptSection
	if err := decodeStrict(r, &body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	result, err := h.s.UpdatePromptSection(id, &body)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	h.broadcastMaterializedOutputs(result)
	writeJSON(w, 200, result)
}

func (h *handler) deletePromptSection(w http.ResponseWriter, r *http.Request) {
	id, err := parseIDPath(r, "id")
	if err != nil {
		writeErr(w, 400, "bad id")
		return
	}
	result, err := h.s.DeletePromptSection(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	h.broadcastMaterializedOutputs(result)
	writeJSON(w, 200, result)
}

func (h *handler) compilePromptCollection(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, 400, "bad id")
		return
	}
	result, err := h.s.compilePromptCollectionMutation(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	h.broadcastMaterializedOutputs(result)
	writeJSON(w, 200, result)
}

func (h *handler) broadcastMaterializedOutputs(result *PromptCollectionMutationResult) {
	if h.onFileSaved == nil || result == nil {
		return
	}
	for _, materialized := range result.MaterializedOutputs {
		file := materialized
		if version, err := latestVersionForFile(h.s, file.ID); err == nil && version != nil {
			h.onFileSaved(&file, version)
		}
	}
}

func latestVersionForFile(store *Store, fileID int64) (*TrackedFileVersion, error) {
	return store.LatestVersion(fileID)
}

func parseIDPath(r *http.Request, key string) (int64, error) {
	return strconv.ParseInt(r.PathValue(key), 10, 64)
}
