package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/vss-suite/vss/server/internal/collab"
	"github.com/vss-suite/vss/server/internal/crdt"
	"github.com/vss-suite/vss/server/internal/identity"
	"github.com/vss-suite/vss/server/internal/sheet"
)

// Server encapsulates the HTTP API and WebSocket collaboration server.
type Server struct {
	Hub  *collab.Hub
	Mux  *http.ServeMux
	Auth *identity.Service
}

// NewServer initializes a new Server with all registered endpoints.
func NewServer(hub *collab.Hub, auth ...*identity.Service) *Server {
	var authSvc *identity.Service
	if len(auth) > 0 {
		authSvc = auth[0]
	} else if hub != nil && hub.Auth != nil {
		if as, ok := hub.Auth.(*identity.Service); ok {
			authSvc = as
		}
	}
	if authSvc == nil {
		authSvc = identity.NewService(identity.NewMemoryUserStore(), []byte("vss-dev-secret-key-32-bytes-long"))
	}
	if hub == nil {
		hub = collab.NewHub(authSvc)
	} else if hub.Auth == nil {
		hub.Auth = authSvc
	}

	s := &Server{
		Hub:  hub,
		Mux:  http.NewServeMux(),
		Auth: authSvc,
	}
	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	s.Mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.Mux.HandleFunc("GET /ws", s.handleWS)
	s.Mux.HandleFunc("GET /api/docs/", s.handleDocExport)
	s.Mux.HandleFunc("POST /api/docs/", s.handleDocImport)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	collab.ServeWS(s.Hub, w, r)
}

func (s *Server) handleDocExport(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/docs/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[1] != "export" {
		http.NotFound(w, r)
		return
	}
	docID := parts[0]
	room := s.Hub.GetOrCreateRoom(docID)

	data, err := room.Grid.ToCanonicalJSON()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleDocImport(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/docs/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[1] != "import" {
		http.NotFound(w, r)
		return
	}
	docID := parts[0]

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	grid, err := crdt.FromCanonicalJSON(body)
	if err != nil {
		http.Error(w, "invalid canonical json: "+err.Error(), http.StatusBadRequest)
		return
	}

	room := s.Hub.GetOrCreateRoom(docID)
	room.Grid = grid
	room.Engine = sheet.NewEngine(grid)
	_, _ = room.Engine.RecalculateAll()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "imported", "doc_id": docID})
}
