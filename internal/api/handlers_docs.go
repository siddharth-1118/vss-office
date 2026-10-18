package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

type createDocRequest struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// handleCreateDoc creates a new document, initializes its Hub room, and persists metadata and snapshot.
func (s *Server) handleCreateDoc(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req createDocRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed JSON")
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	docType := strings.ToLower(strings.TrimSpace(req.Type))
	if docType != "grid" && docType != "writer" {
		writeError(w, http.StatusBadRequest, "only 'grid' and 'writer' types are supported in this session ('present' is not supported)")
		return
	}

	docID := uuid.NewString()
	now := time.Now().UTC()
	meta := DocMetadata{
		ID:        docID,
		Name:      name,
		OwnerID:   user.ID,
		Type:      docType,
		CreatedAt: now,
	}

	if err := s.DocStore.Create(meta); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create document metadata: "+err.Error())
		return
	}

	// Initialize empty room in Hub
	room := s.Hub.GetOrCreateRoom(docID)

	// Persist empty canonical snapshot to data/docs/{id}/snapshot.json
	docDir := filepath.Join(s.DataDir, "docs", docID)
	_ = os.MkdirAll(docDir, 0o755)
	snapFile := filepath.Join(docDir, "snapshot.json")

	if docType == "writer" {
		writerSnapshot, err := room.Writer.ToSnapshotJSON()
		if err == nil {
			_ = os.WriteFile(snapFile, writerSnapshot, 0o644)
		}
	} else {
		gridSnapshot, err := room.Grid.ToCanonicalJSON()
		if err == nil {
			_ = os.WriteFile(snapFile, gridSnapshot, 0o644)
		}
	}

	writeJSON(w, http.StatusCreated, meta)
}

// handleListDocs lists documents owned by the authenticated user, sorted by CreatedAt descending.
func (s *Server) handleListDocs(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	docs, err := s.DocStore.List(user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list documents: "+err.Error())
		return
	}

	if docs == nil {
		docs = []DocMetadata{}
	}
	writeJSON(w, http.StatusOK, docs)
}

// handleGetDoc retrieves a single document's metadata if owned by the caller.
func (s *Server) handleGetDoc(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusNotFound, "document not found")
		return
	}

	meta, err := s.DocStore.Get(id)
	if err != nil || meta.OwnerID != user.ID {
		writeError(w, http.StatusNotFound, "document not found")
		return
	}

	writeJSON(w, http.StatusOK, meta)
}

// handleDeleteDoc deletes a document's metadata and disk files.
// Note: Does not evict the in-memory room from the Hub in this session (see limitation in docs/sheet-engine.md).
func (s *Server) handleDeleteDoc(w http.ResponseWriter, r *http.Request) {
	user, ok := userFromCtx(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusNotFound, "document not found")
		return
	}

	meta, err := s.DocStore.Get(id)
	if err != nil || meta.OwnerID != user.ID {
		writeError(w, http.StatusNotFound, "document not found")
		return
	}

	if err := s.DocStore.Delete(id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete document: "+err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleReadyz checks that all critical subsystems (e.g. DocStore) are healthy and ready to serve traffic.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if s.DocStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "not_ready",
			"error":  "docstore is not configured",
		})
		return
	}

	if _, err := s.DocStore.List(""); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "not_ready",
			"error":  err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ready",
	})
}
